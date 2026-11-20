package athan

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ygelfand/libcountertop/pkg/fetch"

	"github.com/ygelfand/echolocal/internal/hardware/speaker"
	"github.com/ygelfand/echolocal/internal/layout"
)

// Reciter is whose recitation the surah dropdown plays.
const Reciter = "Nasser Al-Qatami"

// SurahURL is tvquran.com's file for surah n of Nasser Al-Qatami's complete recitation.
func SurahURL(n int) string { return fmt.Sprintf("%srecitations/90/25/%03d.mp3", tvquran, n) }

// SurahNames are the dropdown's choices, numbered as the mushaf numbers them.
func SurahNames() []string {
	out := make([]string, len(surahs))
	for i := range surahs {
		out[i] = surahName(i + 1)
	}
	return out
}

func surahName(n int) string {
	if n < 1 || n > len(surahs) {
		return ""
	}
	return strconv.Itoa(n) + ". " + surahs[n-1]
}

// surahNumber reads a dropdown choice back as its surah number.
func surahNumber(name string) (int, bool) {
	num, _, ok := strings.Cut(name, ". ")
	n, err := strconv.Atoi(num)
	if !ok || err != nil || surahName(n) != name {
		return 0, false
	}
	return n, true
}

// QuranDir keeps recently played surahs, so a surah played again plays without the internet.
var QuranDir = filepath.Join(layout.StateDir, "quran")

// QuranLimit bounds the kept surahs; the whole Quran is 747 MB and the device has about 1 GB free.
const QuranLimit = 200 << 20

// stall is how long a read from tvquran.com may wait before the recitation gives up.
const stall = 30 * time.Second

// Recitation plays a surah from the cache, or from the network while keeping it for next time.
func Recitation(ctx context.Context, p *speaker.Player, url string, level float64) error {
	path := filepath.Join(QuranDir, filepath.Base(cached(url)))

	if f, err := os.Open(path); err == nil {
		defer f.Close()
		now := time.Now()
		_ = os.Chtimes(path, now, now) // the cache drops what was played longest ago
		return decode(ctx, p, f, level)
	}

	fetchCtx, giveUp := context.WithCancel(ctx)
	defer giveUp()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := fetch.Client(0).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}

	if err := os.MkdirAll(QuranDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(QuranDir, "fetch-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	body := &watched{r: io.TeeReader(resp.Body, tmp), timer: time.AfterFunc(stall, giveUp)}
	defer body.timer.Stop()

	err = decode(ctx, p, body, level)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if fetchCtx.Err() != nil && ctx.Err() == nil {
			return fmt.Errorf("%s: nothing arrived for %s", url, stall)
		}
		return err
	}

	// Only a whole file is kept; a stop part way leaves nothing behind.
	info, err := os.Stat(tmp.Name())
	if err != nil || (resp.ContentLength > 0 && info.Size() != resp.ContentLength) {
		return nil
	}
	Evict(QuranDir, QuranLimit-info.Size())
	if err := os.Rename(tmp.Name(), path); err != nil {
		slog.Warn("could not keep the surah", "err", err)
		return nil
	}
	slog.Info("surah kept", "url", url, "bytes", info.Size())
	return nil
}

// watched puts off giving up each time a read returns.
type watched struct {
	r     io.Reader
	timer *time.Timer
}

func (w *watched) Read(p []byte) (int, error) {
	n, err := w.r.Read(p)
	w.timer.Reset(stall)
	return n, err
}

// Evict deletes the least recently played surahs until those left take at most limit bytes, and
// clears any download a stop or a restart left part way.
func Evict(dir string, limit int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type kept struct {
		path string
		size int64
		at   time.Time
	}
	var all []kept
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if strings.HasPrefix(e.Name(), "fetch-") {
			if time.Since(info.ModTime()) > time.Hour {
				_ = os.Remove(path)
			}
			continue
		}
		all = append(all, kept{path, info.Size(), info.ModTime()})
		total += info.Size()
	}
	slices.SortFunc(all, func(a, b kept) int { return a.at.Compare(b.at) })
	for _, k := range all {
		if total <= limit {
			return
		}
		if err := os.Remove(k.path); err == nil {
			total -= k.size
			slog.Info("surah dropped from the cache", "file", filepath.Base(k.path))
		}
	}
}
