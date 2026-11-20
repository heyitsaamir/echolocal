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

	// Surah is the dropdown's choice, 1 to 114, and QuranVolume how loud it recites.
	Surah       int `json:"surah"`
	QuranVolume int `json:"quran_volume"`

	Off []Prayer `json:"off,omitempty"`
}

const settingsVersion = 4

func Defaults() Settings {
	return Settings{
		Version:   settingsVersion,
		Enabled:   true,
		FromHA:    true,
		Method:    Methods[0].Name,
		School:    Shafi,
		Voice:     athans[0].Name,
		FajrVoice: fajrs[0].Name,
		Volume:    70,
		Warning:   15,

		Surah:       1,
		QuranVolume: 60,
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
	if s.Version == 2 {
		// Version 2 offered IslamCan's recordings, none with Fajr's words, so Fajr shared the others'.
		s.Voice, s.FajrVoice, s.Version = athans[0].Name, fajrs[0].Name, 3
	}
	if s.Version == 3 {
		// Version 3 could not recite.
		s.Surah, s.QuranVolume, s.Version = 1, 60, 4
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
	if s.Surah < 1 || s.Surah > len(surahs) {
		s.Surah = 1
	}
	s.QuranVolume = min(max(s.QuranVolume, 0), 100)
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
	for _, v := range slices.Concat(fajrs, athans) {
		if v.Name == name {
			return v.URL
		}
	}
	return athans[0].URL
}

type voice struct{ Name, URL string }

const (
	SameVoice   = "Same as other prayers"
	CustomVoice = "Custom URL"
)

const tvquran = "https://download.tvquran.com/download/"

// athans are tvquran.com's Adhan and Takbir collection (www.tvquran.com/en/selections/category/9),
// less its Eid takbir.
var athans = []voice{
	{"Nasser Al-Qatami", tvquran + "TvQuran.com__Athan/TvQuran.com__08.athan.mp3"},
	{"Mishary Alafasi", tvquran + "TvQuran.com__Athan/TvQuran.com__04.athan.mp3"},
	{"Mishary Alafasi, with dua", tvquran + "selections/180/58b0d9f97eeec.mp3"},
	{"Hamad Daghriry", tvquran + "TvQuran.com__Athan/TvQuran.com__06.athan.mp3"},
	{"Majid Al-Hamthany", tvquran + "TvQuran.com__Athan/TvQuran.com__05.athan.mp3"},
	{"Ghazi Asaadoun", tvquran + "TvQuran.com__Athan/TvQuran.com__02.athan.mp3"},
	{"Ibrahim Al-Arkani", tvquran + "TvQuran.com__Athan/TvQuran.com__03.athan.mp3"},
	{"Islam Sobhi", tvquran + "selections/315/5cc9f578195e0.mp3"},
}

// fajrs carry Fajr's "as-salatu khayrun min an-nawm", so they are offered for Fajr alone.
var fajrs = []voice{
	{"Mansoor Az-Zahrani (Fajr)", tvquran + "TvQuran.com__Athan/TvQuran.com__01.athan.mp3"},
	{"Mishary Alafasi (Fajr), with dua", tvquran + "selections/180/58b0dac02106f.mp3"},
	{"Malek Chibat Al-Hamd (Fajr)", tvquran + "TvQuran.com__Athan/TvQuran.com__07.athan-fjer.mp3"},
	{"Ahmed Abdulrahman Darar (Fajr)", tvquran + "selections/380/6088017b417a3.mp3"},
}

// VoiceNames are the choices for the other prayers, or for Fajr: its own athans first, then the rest.
func VoiceNames(fajr bool) []string {
	var out []string
	if fajr {
		for _, v := range fajrs {
			out = append(out, v.Name)
		}
		out = append(out, SameVoice)
	}
	for _, v := range athans {
		out = append(out, v.Name)
	}
	return append(out, CustomVoice)
}

// Recordings are every built-in recording, the chosen ones first, so the device can keep them all.
func (s Settings) Recordings() []string {
	out := []string{s.URL(Fajr), s.URL(Dhuhr)}
	for _, v := range slices.Concat(fajrs, athans) {
		out = append(out, v.URL)
	}
	return unique(out...)
}
