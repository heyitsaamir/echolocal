package athan

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Settings is everything the athan keeps, saved whole in the runtime store.
type Settings struct {
	Version int `json:"version"`

	Enabled bool `json:"enabled"`

	// FromHA follows Home Assistant's home zone, keeping Lat and Lng as the last place it reported.
	FromHA bool `json:"from_ha"`

	// Lat and Lng are text so an unset place stays empty instead of reading as 0,0.
	Lat string `json:"lat"`
	Lng string `json:"lng"`

	Method string `json:"method"`
	School School `json:"school"`

	Voice     string `json:"voice"`
	FajrVoice string `json:"fajr_voice"`
	CustomURL string `json:"custom_url"`

	Volume  int `json:"volume"`
	Warning int `json:"warning"`

	Off []Prayer `json:"off,omitempty"`
}

const settingsVersion = 2

func Defaults() Settings {
	return Settings{
		Version:   settingsVersion,
		Enabled:   true,
		FromHA:    true,
		Method:    Methods[0].Name,
		School:    Shafi,
		Voice:     voices[0].Name,
		FajrVoice: SameVoice,
		Volume:    70,
		Warning:   15,
	}
}

// usable fills anything a saved copy from an older build lacks.
func (s Settings) usable() Settings {
	if s.Version == 0 {
		return Defaults()
	}
	if s.Version == 1 {
		// Version 1 had no way to follow Home Assistant; its place was copied from there by hand.
		s.FromHA, s.Version = true, 2
	}
	d := Defaults()
	if _, ok := methodNamed(s.Method); !ok {
		s.Method = d.Method
	}
	if !slices.Contains(Schools, s.School) {
		s.School = d.School
	}
	if !slices.Contains(VoiceNames(false), s.Voice) {
		s.Voice = d.Voice
	}
	if !slices.Contains(VoiceNames(true), s.FajrVoice) {
		s.FajrVoice = d.FajrVoice
	}
	s.Volume = min(max(s.Volume, 0), 100)
	s.Warning = min(max(s.Warning, 0), MaxWarning)
	return s
}

const MaxWarning = 60

func (s Settings) Place() (lat, lng float64, err error) {
	lat, err = coordinate(s.Lat, 90)
	if err != nil {
		return 0, 0, fmt.Errorf("latitude: %w", err)
	}
	lng, err = coordinate(s.Lng, 180)
	if err != nil {
		return 0, 0, fmt.Errorf("longitude: %w", err)
	}
	return lat, lng, nil
}

// homePlace is Home Assistant's home zone as the athan keeps it: three decimals, about 100 m, which
// prayer times do not need finer than.
func homePlace(lat, lng float64) (string, string, bool) {
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 || (lat == 0 && lng == 0) {
		return "", "", false
	}
	return strconv.FormatFloat(lat, 'f', 3, 64), strconv.FormatFloat(lng, 'f', 3, 64), true
}

func coordinate(v string, most float64) (float64, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, fmt.Errorf("not set")
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", v)
	}
	if f < -most || f > most {
		return 0, fmt.Errorf("%v is outside ±%v", f, most)
	}
	return f, nil
}

func (s Settings) method() Method {
	m, _ := methodNamed(s.Method)
	return m
}

func methodNamed(name string) (Method, bool) {
	for _, m := range Methods {
		if m.Name == name {
			return m, true
		}
	}
	return Methods[0], false
}

func MethodNames() []string {
	out := make([]string, len(Methods))
	for i, m := range Methods {
		out[i] = m.Name
	}
	return out
}

func SchoolNames() []string {
	out := make([]string, len(Schools))
	for i, s := range Schools {
		out[i] = string(s)
	}
	return out
}

func (s Settings) Calls(p Prayer) bool { return !slices.Contains(s.Off, p) }

func (s *Settings) SetCalls(p Prayer, on bool) {
	s.Off = slices.DeleteFunc(s.Off, func(o Prayer) bool { return o == p })
	if !on {
		s.Off = append(s.Off, p)
	}
}

// URL is the recording for a prayer, empty when Custom is chosen without a link.
func (s Settings) URL(p Prayer) string {
	name := s.Voice
	if p == Fajr && s.FajrVoice != SameVoice {
		name = s.FajrVoice
	}
	if name == CustomVoice {
		return strings.TrimSpace(s.CustomURL)
	}
	for _, v := range voices {
		if v.Name == name {
			return v.URL
		}
	}
	return voices[0].URL
}

type voice struct{ Name, URL string }

const (
	SameVoice   = "Same as other prayers"
	CustomVoice = "Custom URL"
)

// voices are IslamCan's public recordings; the files carry no reciter names, so they are numbered.
var voices = func() []voice {
	out := make([]voice, 20)
	for i := range out {
		out[i] = voice{
			Name: fmt.Sprintf("IslamCan %d", i+1),
			URL:  fmt.Sprintf("https://www.islamcan.com/audio/adhan/azan%d.mp3", i+1),
		}
	}
	return out
}()

func VoiceNames(fajr bool) []string {
	var out []string
	if fajr {
		out = append(out, SameVoice)
	}
	for _, v := range voices {
		out = append(out, v.Name)
	}
	return append(out, CustomVoice)
}
