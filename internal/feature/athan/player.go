package athan

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	gomp3 "github.com/hajimehoshi/go-mp3"

	"github.com/ygelfand/libcountertop/pkg/audio/resample"
	"github.com/ygelfand/libcountertop/pkg/audio/tone"
	"github.com/ygelfand/libcountertop/pkg/fetch"

	"github.com/ygelfand/echolocal/internal/config"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/layout"
)

// CacheDir keeps each chosen recording on the device, so the athan still sounds when the internet is down.
var CacheDir = filepath.Join(layout.StateDir, "athan")

const downloadTimeout = 2 * time.Minute

// cached is where a recording lives once fetched.
func cached(url string) string {
	sum := sha1.Sum([]byte(url))
	return filepath.Join(CacheDir, hex.EncodeToString(sum[:8])+".mp3")
}

// Keep fetches a recording into the cache unless it is already there.
func Keep(ctx context.Context, url string) error {
	if url == "" {
		return errors.New("no recording chosen")
	}
	path := cached(url)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(CacheDir, 0o700); err != nil {
		return err
	}

	body, err := open(ctx, url)
	if err != nil {
		return err
	}
	defer body.Close()

	tmp, err := os.CreateTemp(CacheDir, "fetch-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := io.Copy(tmp, body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := check(tmp.Name()); err != nil {
		return fmt.Errorf("%s: %w", url, err)
	}
	return os.Rename(tmp.Name(), path)
}

// Prune deletes cached recordings no longer offered, such as a voice list a newer build replaced.
func Prune(keep []string) {
	wanted := map[string]bool{}
	for _, url := range keep {
		wanted[filepath.Base(cached(url))] = true
	}
	entries, err := os.ReadDir(CacheDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || wanted[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(CacheDir, e.Name())); err == nil {
			slog.Info("athan recording dropped", "file", e.Name())
		}
	}
}

// check refuses a download that is not MP3, such as an error page, before it replaces anything.
func check(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := gomp3.NewDecoder(f); err != nil {
		return fmt.Errorf("not an MP3: %w", err)
	}
	return nil
}

func open(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := fetch.Client(downloadTimeout).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return resp.Body, nil
}

// source is the cached copy when there is one, else the network.
func source(ctx context.Context, url string) (io.ReadCloser, error) {
	if f, err := os.Open(cached(url)); err == nil {
		return f, nil
	}
	return open(ctx, url)
}

// ahead is how much decoded sound waits on the speaker, enough to ride out a slow read.
const ahead = speaker.Rate / 2

// Stream decodes an MP3 onto the alerts lane at level, a little ahead of the speaker, until it ends or ctx stops it.
func Stream(ctx context.Context, p *speaker.Player, url string, level float64) error {
	body, err := source(ctx, url)
	if err != nil {
		return err
	}
	defer body.Close()
	return decode(ctx, p, body, level)
}

// decode plays an MP3 onto the alerts lane until it ends, a read fails, or ctx stops it.
func decode(ctx context.Context, p *speaker.Player, body io.Reader, level float64) error {
	dec, err := gomp3.NewDecoder(body)
	if err != nil {
		return fmt.Errorf("mp3: %w", err)
	}

	var rs *resample.Rational
	if dec.SampleRate() != speaker.Rate {
		rs = resample.NewRational(dec.SampleRate(), speaker.Rate, speaker.Channels)
	}

	lane := p.Lane(config.StreamAlerts)
	buf := make([]byte, 16*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := io.ReadFull(dec, buf)
		if n >= 4 {
			pcm := samples(buf[:n-n%4])
			if rs != nil {
				pcm = rs.Run(pcm)
			}
			scale(pcm, level)
			if err := wait(ctx, lane); err != nil {
				return err
			}
			lane.Play(pcm)
		}
		switch {
		case err == nil:
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			return nil
		default:
			return err
		}
	}
}

func wait(ctx context.Context, lane speaker.Lane) error {
	for lane.Queued() > ahead {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil
}

// samples reads go-mp3's output: 16-bit little-endian stereo.
func samples(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8)
	}
	return out
}

func scale(pcm []int16, level float64) {
	for i, v := range pcm {
		pcm[i] = int16(float64(v) * level)
	}
}

// fallback is what sounds when no recording can be had: three rising tones, twice.
var fallback = []tone.Note{
	{Freq: 523, Ms: 400}, {Freq: 659, Ms: 400}, {Freq: 784, Ms: 700}, {Ms: 600},
	{Freq: 523, Ms: 400}, {Freq: 659, Ms: 400}, {Freq: 784, Ms: 900},
}

var configAlerts = config.StreamAlerts
