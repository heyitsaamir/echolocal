// Package athan calls the athan at each prayer time, works the times out on the device, warns on the
// ring as one nears, and lets the action button stop or skip it.
package athan

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"

	"github.com/ygelfand/libcountertop/pkg/audio/sound"
	sharedcomponent "github.com/ygelfand/libcountertop/pkg/runtime/component"
	"github.com/ygelfand/libcountertop/pkg/runtime/remember"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"

	"github.com/ygelfand/echolocal/internal/component"
	featureremember "github.com/ygelfand/echolocal/internal/feature/remember"
	"github.com/ygelfand/echolocal/internal/feature/states"
	"github.com/ygelfand/echolocal/internal/feature/timesync"
	"github.com/ygelfand/echolocal/internal/hardware/buttons"
	"github.com/ygelfand/echolocal/internal/hardware/led"
	"github.com/ygelfand/echolocal/internal/hardware/speaker"
)

func init() {
	component.Register(sharedcomponent.Device, Get, sharedcomponent.Order(33))
}

var (
	warnColor = led.Color{R: 0xFF}
	callColor = led.Color{R: 0xFF, G: 0x10}
)

// Status is what the athan is doing, as Home Assistant shows it.
const (
	StatusNoPlace  = "Set latitude and longitude"
	StatusOff      = "Off"
	StatusClock    = "Waiting for the clock"
	StatusIdle     = "Idle"
	StatusNear     = "Approaching %s"
	StatusPlaying  = "Playing %s"
	StatusSkipping = "Skipping %s"
)

type Athan struct {
	saved remember.Slot[Settings]

	mu       sync.Mutex
	settings Settings
	fired    map[string]bool
	skipped  string
	playing  *sound.Claim
	calling  Prayer

	warn *led.Claim
	call *led.Claim
	lit  []led.Color

	fetching atomic.Bool
	fetched  time.Time

	home struct{ lat, lng *states.Value }

	enabled  *esphome.Switch
	fromHA   *esphome.Switch
	lat, lng *esphome.Text
	method   *esphome.Select
	school   *esphome.Select
	voice    *esphome.Select
	fajr     *esphome.Select
	custom   *esphome.Text
	volume   *esphome.Number
	warning  *esphome.Number
	per      map[Prayer]*esphome.Switch

	status *esphome.TextSensor
	next   *esphome.TextSensor
	times  map[Prayer]*esphome.TextSensor

	play, stop, skip *esphome.Button
}

var get = sync.OnceValue(build)

func Get() *Athan { return get() }

func build() *Athan {
	a := &Athan{
		saved: remember.NewSlot[Settings](featureremember.Get(), "athan"),
		fired: map[string]bool{},
		warn:  led.Get().Claim(led.PriorityTimer),
		call:  led.Get().Claim(led.PriorityAlarm),
		per:   map[Prayer]*esphome.Switch{},
		times: map[Prayer]*esphome.TextSensor{},
	}
	a.settings = a.saved.Get().usable()
	a.entities()
	a.publish(a.settings)
	buttons.Get().Take(a.take)

	// Home Assistant keeps its home location as attributes of zone.home and reports them on connect.
	a.home.lat = states.Get().Follow(HomeZone, "latitude")
	a.home.lng = states.Get().Follow(HomeZone, "longitude")
	a.home.lat.Changed.Listen(func(string) { a.followHome() })
	a.home.lng.Changed.Listen(func(string) { a.followHome() })
	return a
}

func (a *Athan) Name() string { return "athan" }

func setting(id, name, icon string) esphome.Base {
	return esphome.Base{ObjectID: "athan_" + id, Name: name, Icon: icon, Category: esphome.CategoryConfig}
}

func shown(id, name, icon string) esphome.Base {
	return esphome.Base{ObjectID: "athan_" + id, Name: name, Icon: icon}
}

func (a *Athan) entities() {
	a.enabled = &esphome.Switch{Base: setting("enabled", "Athan", "mdi:mosque"),
		OnCommand: func(v bool) { a.change(func(s *Settings) { s.Enabled = v }) }}

	a.fromHA = &esphome.Switch{Base: setting("location_from_ha", "Athan location from Home Assistant", "mdi:home-map-marker"),
		OnCommand: func(v bool) {
			a.change(func(s *Settings) { s.FromHA = v })
			a.followHome()
		}}

	a.lat = &esphome.Text{Base: setting("latitude", "Athan latitude", "mdi:latitude"), MaxLength: 16}
	a.lat.OnCommand = func(v string) { a.place(v, true) }
	a.lng = &esphome.Text{Base: setting("longitude", "Athan longitude", "mdi:longitude"), MaxLength: 16}
	a.lng.OnCommand = func(v string) { a.place(v, false) }

	a.method = &esphome.Select{Base: setting("method", "Athan calculation method", "mdi:calculator"),
		Options: MethodNames(), OnCommand: func(v string) { a.change(func(s *Settings) { s.Method = v }) }}
	a.school = &esphome.Select{Base: setting("school", "Athan Asr school", "mdi:weather-sunset-down"),
		Options: SchoolNames(), OnCommand: func(v string) { a.change(func(s *Settings) { s.School = School(v) }) }}

	a.voice = &esphome.Select{Base: setting("voice", "Athan voice", "mdi:account-voice"),
		Options: VoiceNames(false), OnCommand: func(v string) { a.change(func(s *Settings) { s.Voice = v }) }}
	a.fajr = &esphome.Select{Base: setting("fajr_voice", "Fajr athan voice", "mdi:weather-night"),
		Options: VoiceNames(true), OnCommand: func(v string) { a.change(func(s *Settings) { s.FajrVoice = v }) }}
	a.custom = &esphome.Text{Base: setting("custom_url", "Athan custom URL", "mdi:link"), MaxLength: 255}
	a.custom.OnCommand = func(v string) { a.change(func(s *Settings) { s.CustomURL = v }) }

	a.volume = &esphome.Number{Base: setting("volume", "Athan volume", "mdi:volume-high"),
		Min: 0, Max: 100, Step: 5, Unit: "%", Mode: api.NumberMode_NUMBER_MODE_SLIDER,
		OnCommand: func(v float32) { a.change(func(s *Settings) { s.Volume = int(math.Round(float64(v))) }) }}
	a.warning = &esphome.Number{Base: setting("warning", "Athan red ring lead", "mdi:clock-alert"),
		Min: 0, Max: MaxWarning, Step: 1, Unit: "min", Mode: api.NumberMode_NUMBER_MODE_BOX,
		OnCommand: func(v float32) { a.change(func(s *Settings) { s.Warning = int(math.Round(float64(v))) }) }}

	for _, p := range Calls {
		a.per[p] = &esphome.Switch{Base: setting(lower(p), string(p)+" athan", "mdi:bell-ring"),
			OnCommand: func(v bool) { a.change(func(s *Settings) { s.SetCalls(p, v) }) }}
	}

	a.status = &esphome.TextSensor{Base: shown("status", "Athan status", "mdi:mosque")}
	a.next = &esphome.TextSensor{Base: shown("next", "Next prayer", "mdi:clock-outline")}
	for _, p := range Prayers {
		a.times[p] = &esphome.TextSensor{Base: shown("time_"+lower(p), string(p)+" time", "mdi:clock-time-four"),
			DeviceClass: "timestamp"}
	}

	a.play = &esphome.Button{Base: shown("play", "Play athan now", "mdi:play"), OnPress: a.Test}
	a.stop = &esphome.Button{Base: shown("stop", "Stop athan", "mdi:stop"), OnPress: a.Stop}
	a.skip = &esphome.Button{Base: shown("skip", "Skip next athan", "mdi:skip-next"), OnPress: a.Skip}
}

func (a *Athan) Entities() []esphome.Entity {
	out := []esphome.Entity{
		a.enabled, a.fromHA, a.lat, a.lng, a.method, a.school, a.voice, a.fajr, a.custom, a.volume, a.warning,
	}
	for _, p := range Calls {
		out = append(out, a.per[p])
	}
	out = append(out, a.status, a.next)
	for _, p := range Prayers {
		out = append(out, a.times[p])
	}
	return append(out, a.play, a.stop, a.skip)
}

func (a *Athan) publish(s Settings) {
	a.enabled.Set(s.Enabled)
	a.fromHA.Set(s.FromHA)
	a.lat.Set(s.Lat)
	a.lng.Set(s.Lng)
	a.method.Set(s.Method)
	a.school.Set(string(s.School))
	a.voice.Set(s.Voice)
	a.fajr.Set(s.FajrVoice)
	a.custom.Set(s.CustomURL)
	a.volume.Set(float32(s.Volume))
	a.warning.Set(float32(s.Warning))
	for p, sw := range a.per {
		sw.Set(s.Calls(p))
	}
}

func (a *Athan) Settings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

// change applies an edit, saves it, and shows Home Assistant what stuck.
func (a *Athan) change(edit func(*Settings)) {
	a.mu.Lock()
	s := a.settings
	edit(&s)
	s = s.usable()
	a.settings = s
	a.fetched = time.Time{}
	a.mu.Unlock()

	if err := a.saved.Set(s); err != nil {
		slog.Error("saving the athan settings failed", "err", err)
	}
	a.publish(s)
	a.tick(time.Now())
}

func (a *Athan) place(v string, lat bool) {
	most := 180.0
	if lat {
		most = 90
	}
	if v != "" {
		if _, err := coordinate(v, most); err != nil {
			slog.Warn("ignoring a bad athan coordinate", "value", v, "err", err)
			a.publish(a.Settings())
			return
		}
	}
	a.change(func(s *Settings) {
		s.FromHA = false
		if lat {
			s.Lat = v
		} else {
			s.Lng = v
		}
	})
}

// HomeZone is the Home Assistant entity whose latitude and longitude the athan follows.
const HomeZone = "zone.home"

// followHome takes Home Assistant's home location when following it and both halves have arrived.
func (a *Athan) followHome() {
	s := a.Settings()
	if !s.FromHA {
		return
	}
	lat, ok1 := a.home.lat.Float()
	lng, ok2 := a.home.lng.Float()
	if !ok1 || !ok2 {
		return
	}
	la, lo, ok := homePlace(lat, lng)
	if !ok || (s.Lat == la && s.Lng == lo) {
		return
	}
	slog.Info("athan location now follows Home Assistant", "zone", HomeZone)
	a.change(func(s *Settings) { s.Lat, s.Lng = la, lo })
}

func (a *Athan) Run(ctx context.Context) error {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		a.tick(time.Now())
		select {
		case <-ctx.Done():
			a.Stop()
			return nil
		case <-t.C:
		}
	}
}

func (a *Athan) tick(now time.Time) {
	s := a.Settings()
	calls := Schedule(s, now)
	a.showTimes(s, now, calls)
	a.keepRecordings(s, now)

	a.mu.Lock()
	defer a.mu.Unlock()

	playing := a.playing != nil && !a.playing.Finished()
	switch {
	case calls == nil:
		a.say(StatusNoPlace)
		a.ring(nil)
		return
	case !s.Enabled:
		a.say(StatusOff)
		a.ring(nil)
		return
	case !timesync.Get().Valid():
		a.say(StatusClock)
		return
	case playing:
		a.say(fmt.Sprintf(StatusPlaying, a.calling))
		a.ring(nil)
		return
	}

	if c, ok := Due(calls, now); ok && !a.fired[c.Key()] {
		a.fired[c.Key()] = true
		if a.skipped != c.Key() {
			a.start(s, c.Prayer)
			return
		}
	}
	a.forget(now)

	next, ok := Next(calls, now)
	if !ok {
		a.say(StatusIdle)
		a.ring(nil)
		return
	}
	if a.skipped == next.Key() {
		a.say(fmt.Sprintf(StatusSkipping, next.Prayer))
		a.ring(nil)
		return
	}
	near := Nearness(next, now, time.Duration(s.Warning)*time.Minute)
	if near < 0 {
		a.say(StatusIdle)
		a.ring(nil)
		return
	}
	a.say(fmt.Sprintf(StatusNear, next.Prayer))
	a.ring(frame(near))
}

// forget drops calls long past, so the map stays small on a device that runs for months.
func (a *Athan) forget(now time.Time) {
	for key := range a.fired {
		at, err := time.Parse("2006-01-02T15:04", key[:16])
		if err == nil && now.Sub(at) > 48*time.Hour {
			delete(a.fired, key)
		}
	}
}

func (a *Athan) say(v string) {
	if a.status.Get() != v {
		a.status.Set(v)
	}
}

func (a *Athan) showTimes(s Settings, now time.Time, calls []Call) {
	today := Day(s, now)
	for _, p := range Prayers {
		v := ""
		if t, ok := today[p]; ok {
			v = t.Format(time.RFC3339)
		}
		if a.times[p].Get() != v {
			a.times[p].Set(v)
		}
	}
	v := ""
	if next, ok := Next(calls, now); ok {
		v = fmt.Sprintf("%s %s", next.Prayer, next.At.Format("3:04 PM"))
	}
	if a.next.Get() != v {
		a.next.Set(v)
	}
}

// keepRecordings fetches the chosen recordings into the cache, again every few hours if one failed.
func (a *Athan) keepRecordings(s Settings, now time.Time) {
	a.mu.Lock()
	due := now.Sub(a.fetched) > 3*time.Hour
	if due {
		a.fetched = now
	}
	a.mu.Unlock()
	if !due || !a.fetching.CompareAndSwap(false, true) {
		return
	}

	safe.Go("athan recordings", func() {
		defer a.fetching.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		for _, url := range unique(s.URL(Fajr), s.URL(Dhuhr)) {
			if err := Keep(ctx, url); err != nil {
				slog.Warn("could not keep an athan recording", "url", url, "err", err)
				a.mu.Lock()
				a.fetched = now.Add(-3*time.Hour + 10*time.Minute)
				a.mu.Unlock()
				continue
			}
			slog.Info("athan recording kept", "url", url, "path", cached(url))
		}
	})
}

// ring shows the warning; nil clears it.
func (a *Athan) ring(f []led.Color) {
	if !a.warn.Present() || equal(a.lit, f) {
		return
	}
	a.lit = f
	if f == nil {
		a.warn.Clear()
		return
	}
	a.warn.Paint(f)
}

// frame fills the ring red clockwise as the athan nears, the newest segment fading in.
func frame(near float64) []led.Color {
	lit := float64(led.Segments) * near
	out := make([]led.Color, led.Segments)
	for i := range out {
		switch part := lit - float64(i); {
		case part >= 1:
			out[i] = warnColor
		case part > 0:
			out[i] = dim(warnColor, part)
		}
	}
	// One segment always shows, so the warning is seen from its first second.
	if out[0] == (led.Color{}) {
		out[0] = dim(warnColor, 0.25)
	}
	return out
}

func dim(c led.Color, by float64) led.Color {
	f := func(v byte) byte { return byte(math.Round(float64(v) * by)) }
	return led.Color{R: f(c.R), G: f(c.G), B: f(c.B)}
}

func equal(a, b []led.Color) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// start plays the athan for a prayer; the caller holds a.mu.
func (a *Athan) start(s Settings, p Prayer) {
	url, level := s.URL(p), float64(s.Volume)/100
	slog.Info("athan", "prayer", p, "url", url, "level", level)

	a.calling = p
	a.ring(nil)
	a.say(fmt.Sprintf(StatusPlaying, p))

	snd := speaker.Sound()
	snd.Backgrounds().Duck(true)
	a.call.Play(led.EffectPulse, callColor)

	claim := snd.Claim("athan", func(ctx context.Context, spk *speaker.Player) error {
		err := Stream(ctx, spk, url, level)
		if err != nil && ctx.Err() == nil {
			slog.Error("the athan recording would not play, chiming instead", "url", url, "err", err)
			spk.Chime(configAlerts, level, fallback...)
			return nil
		}
		return err
	})
	a.playing = claim

	safe.Go("athan end", func() {
		<-claim.Done()
		a.call.Clear()
		snd.Backgrounds().Duck(false)
		slog.Info("athan ended", "prayer", p, "stopped", claim.Stopped())
	})
}

// Test plays the athan for the next prayer now, from Home Assistant.
func (a *Athan) Test() {
	s := a.Settings()
	p := Dhuhr
	if next, ok := Next(Schedule(s, time.Now()), time.Now()); ok {
		p = next.Prayer
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.start(s, p)
}

// Stop silences an athan that is playing.
func (a *Athan) Stop() {
	a.mu.Lock()
	c := a.playing
	a.mu.Unlock()
	if c != nil && !c.Finished() {
		speaker.Sound().Silence()
	}
}

// Skip passes over the next athan, or stops one already playing.
func (a *Athan) Skip() {
	if a.Playing() {
		a.Stop()
		return
	}
	now := time.Now()
	next, ok := Next(Schedule(a.Settings(), now), now)
	if !ok {
		return
	}
	a.mu.Lock()
	a.skipped = next.Key()
	a.mu.Unlock()
	slog.Info("athan skipped", "prayer", next.Prayer, "at", next.At)
	a.tick(now)
}

func (a *Athan) Playing() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.playing != nil && !a.playing.Finished()
}

func (a *Athan) approaching() bool {
	now := time.Now()
	s := a.Settings()
	next, ok := Next(Schedule(s, now), now)
	if !ok || !s.Enabled {
		return false
	}
	a.mu.Lock()
	skipped := a.skipped == next.Key()
	a.mu.Unlock()
	return !skipped && Nearness(next, now, time.Duration(s.Warning)*time.Minute) >= 0
}

// take lets the action button answer the athan before the assistant hears it: a tap stops one playing, and
// a double tap while the ring is red skips the one coming. Anything else passes on.
func (a *Athan) take(e buttons.Event) bool {
	if e.Name != buttons.Action {
		return false
	}
	switch {
	case e.Kind == buttons.Tap && a.Playing():
		a.Stop()
		return true
	case e.Kind == buttons.DoubleTap && a.approaching():
		a.Skip()
		return true
	}
	return false
}

func lower(p Prayer) string {
	b := []byte(p)
	if len(b) > 0 && b[0] >= 'A' && b[0] <= 'Z' {
		b[0] += 'a' - 'A'
	}
	return string(b)
}

func unique(v ...string) []string {
	var out []string
	for _, s := range v {
		if s == "" {
			continue
		}
		seen := false
		for _, o := range out {
			seen = seen || o == s
		}
		if !seen {
			out = append(out, s)
		}
	}
	return out
}
