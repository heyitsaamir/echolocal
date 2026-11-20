package athan

import (
	"slices"
	"testing"
	"time"
)

func seattle() Settings {
	s := Defaults()
	s.Lat, s.Lng = "47.6062", "-122.3321"
	return s
}

func pacific(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no zone data:", err)
	}
	return loc
}

func TestNoPlaceMeansNoCalls(t *testing.T) {
	if got := Schedule(Defaults(), time.Now()); got != nil {
		t.Fatalf("got %v without a place", got)
	}
}

func TestNextIsTheComingPrayerInLocalTime(t *testing.T) {
	loc := pacific(t)
	now := time.Date(2026, 11, 19, 12, 30, 0, 0, loc)

	next, ok := Next(Schedule(seattle(), now), now)
	if !ok {
		t.Fatal("no next call")
	}
	if next.Prayer != Asr || next.At.Format("15:04") != "14:08" {
		t.Fatalf("next = %s at %s, want Asr at 14:08", next.Prayer, next.At.Format("15:04"))
	}
}

func TestIshaIsFollowedByTomorrowsFajr(t *testing.T) {
	loc := pacific(t)
	now := time.Date(2026, 11, 19, 22, 0, 0, 0, loc)

	next, ok := Next(Schedule(seattle(), now), now)
	if !ok || next.Prayer != Fajr || next.At.Day() != 20 {
		t.Fatalf("next = %+v, want Fajr on the 20th", next)
	}
}

func TestAPrayerTurnedOffIsPassedOver(t *testing.T) {
	loc := pacific(t)
	now := time.Date(2026, 11, 19, 12, 30, 0, 0, loc)
	s := seattle()
	s.SetCalls(Asr, false)

	next, _ := Next(Schedule(s, now), now)
	if next.Prayer != Maghrib {
		t.Fatalf("next = %s, want Maghrib with Asr off", next.Prayer)
	}
}

func TestSunriseHasNoAthan(t *testing.T) {
	loc := pacific(t)
	for _, c := range Schedule(seattle(), time.Date(2026, 11, 19, 0, 0, 0, 0, loc)) {
		if c.Prayer == Sunrise {
			t.Fatal("sunrise was scheduled")
		}
	}
}

func TestDueOnlyWithinTheGrace(t *testing.T) {
	at := time.Date(2026, 11, 19, 14, 8, 0, 0, time.UTC)
	calls := []Call{{Prayer: Asr, At: at}}

	for _, tc := range []struct {
		now  time.Time
		want bool
	}{
		{at.Add(-time.Second), false},
		{at, true},
		{at.Add(Grace - time.Second), true},
		{at.Add(Grace), false},
	} {
		if _, got := Due(calls, tc.now); got != tc.want {
			t.Errorf("due at %s = %v, want %v", tc.now.Sub(at), got, tc.want)
		}
	}
}

func TestNearnessFillsAcrossTheWarning(t *testing.T) {
	at := time.Date(2026, 11, 19, 14, 8, 0, 0, time.UTC)
	c := Call{Prayer: Asr, At: at}
	w := 15 * time.Minute

	for _, tc := range []struct {
		before time.Duration
		want   float64
	}{
		{20 * time.Minute, -1},
		{15 * time.Minute, 0},
		{7*time.Minute + 30*time.Second, 0.5},
		{0, -1},
	} {
		if got := Nearness(c, at.Add(-tc.before), w); got != tc.want {
			t.Errorf("%s before: %v, want %v", tc.before, got, tc.want)
		}
	}
	if got := Nearness(c, at.Add(-time.Minute), 0); got != -1 {
		t.Errorf("no warning window still lit: %v", got)
	}
}

func TestFajrHasItsOwnAthanByDefault(t *testing.T) {
	s := Defaults()
	if s.URL(Fajr) == s.URL(Dhuhr) {
		t.Fatal("Fajr shares the other prayers' athan, which lacks Fajr's words")
	}
	s.FajrVoice = SameVoice
	if s.URL(Fajr) != s.URL(Dhuhr) {
		t.Fatal("Same as other prayers did not follow them")
	}
	s.Voice, s.CustomURL = CustomVoice, " http://10.0.0.113:8123/local/athan.mp3 "
	if got := s.URL(Dhuhr); got != "http://10.0.0.113:8123/local/athan.mp3" {
		t.Fatalf("custom url = %q", got)
	}
}

func TestFajrAthansAreOfferedForFajrAlone(t *testing.T) {
	for _, v := range fajrs {
		if slices.Contains(VoiceNames(false), v.Name) {
			t.Errorf("%q is offered for the other prayers", v.Name)
		}
		if !slices.Contains(VoiceNames(true), v.Name) {
			t.Errorf("%q is not offered for Fajr", v.Name)
		}
	}
}

func TestEveryRecordingIsKeptOnceWithTheChosenFirst(t *testing.T) {
	s := Defaults()
	s.Voice = athans[3].Name
	got := s.Recordings()
	if len(got) != len(athans)+len(fajrs) || got[0] != s.URL(Fajr) || got[1] != athans[3].URL {
		t.Fatalf("got %d recordings starting %v", len(got), got[:2])
	}
}

func TestIslamCanChoicesMoveToTheNewDefaults(t *testing.T) {
	old := Defaults()
	old.Version, old.Voice, old.FajrVoice = 2, "IslamCan 1", SameVoice
	got := old.usable()
	if got.Voice != athans[0].Name || got.FajrVoice != fajrs[0].Name || got.Version != settingsVersion {
		t.Fatalf("not moved: %+v", got)
	}
}

func TestOldOrDamagedSettingsBecomeUsable(t *testing.T) {
	if got := (Settings{}).usable(); got.Version != settingsVersion || !got.Enabled {
		t.Fatalf("an empty store did not take the defaults: %+v", got)
	}
	s := Defaults()
	s.Method, s.Voice, s.Volume, s.Warning = "gone", "gone", 400, -3
	got := s.usable()
	if got.Method != Methods[0].Name || got.Voice != athans[0].Name || got.Volume != 100 || got.Warning != 0 {
		t.Fatalf("not repaired: %+v", got)
	}
}

func TestHomeAssistantsPlaceIsKeptToAHundredMetres(t *testing.T) {
	lat, lng, ok := homePlace(47.49317, -122.14881)
	if !ok || lat != "47.493" || lng != "-122.149" {
		t.Fatalf("got %q %q %v", lat, lng, ok)
	}
	for _, bad := range [][2]float64{{0, 0}, {91, 0}, {0, -181}} {
		if _, _, ok := homePlace(bad[0], bad[1]); ok {
			t.Errorf("%v was taken", bad)
		}
	}
}

func TestAPlaceSavedBeforeFollowingHomeAssistantNowFollowsIt(t *testing.T) {
	old := Defaults()
	old.Version, old.FromHA, old.Lat, old.Lng = 1, false, "47.493", "-122.149"
	got := old.usable()
	if !got.FromHA || got.Version != settingsVersion || got.Lat != "47.493" {
		t.Fatalf("not carried forward: %+v", got)
	}
	if !Defaults().FromHA {
		t.Fatal("a new device should follow Home Assistant")
	}
}
