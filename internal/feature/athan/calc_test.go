package athan

import (
	"testing"
	"time"
)

// Expected times come from api.aladhan.com (timezonestring=UTC), the service SalatTimesBar uses.
func TestTimesMatchAladhan(t *testing.T) {
	cases := []struct {
		name     string
		date     time.Time
		lat, lng float64
		method   int
		school   School
		want     map[Prayer]string
	}{
		{
			name: "Seattle ISNA Shafi", date: day(2026, 11, 19), lat: 47.6062, lng: -122.3321,
			method: 0, school: Shafi,
			want: map[Prayer]string{Fajr: "11-19 13:50", Sunrise: "11-19 15:21", Dhuhr: "11-19 19:55",
				Asr: "11-19 22:08", Maghrib: "11-20 00:28", Isha: "11-20 01:59"},
		},
		{
			name: "Seattle ISNA Hanafi", date: day(2026, 11, 19), lat: 47.6062, lng: -122.3321,
			method: 0, school: Hanafi,
			want: map[Prayer]string{Asr: "11-19 22:44"},
		},
		{
			name: "Seattle midsummer, high latitude rule", date: day(2026, 6, 21), lat: 47.6062, lng: -122.3321,
			method: 0, school: Shafi,
			want: map[Prayer]string{Fajr: "06-21 10:11", Sunrise: "06-21 12:12", Dhuhr: "06-21 20:11",
				Asr: "06-22 00:26", Maghrib: "06-22 04:11", Isha: "06-22 06:11"},
		},
		{
			name: "Makkah MWL", date: day(2026, 11, 19), lat: 21.4225, lng: 39.8262,
			method: 1, school: Shafi,
			want: map[Prayer]string{Fajr: "11-19 02:17", Sunrise: "11-19 03:34", Dhuhr: "11-19 09:06",
				Asr: "11-19 12:16", Maghrib: "11-19 14:38", Isha: "11-19 15:51"},
		},
		{
			name: "Cairo Egypt", date: day(2026, 11, 19), lat: 30.0444, lng: 31.2357,
			method: 2, school: Shafi,
			want: map[Prayer]string{Fajr: "11-19 02:53", Sunrise: "11-19 04:23", Dhuhr: "11-19 09:40",
				Asr: "11-19 12:38", Maghrib: "11-19 14:57", Isha: "11-19 16:18"},
		},
		{
			name: "Karachi Hanafi", date: day(2026, 11, 19), lat: 24.8607, lng: 67.0011,
			method: 4, school: Hanafi,
			want: map[Prayer]string{Fajr: "11-19 00:32", Sunrise: "11-19 01:51", Dhuhr: "11-19 07:17",
				Asr: "11-19 11:08", Maghrib: "11-19 12:43", Isha: "11-19 14:03"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Times(tc.date.Year(), tc.date.Month(), tc.date.Day(), tc.lat, tc.lng, Methods[tc.method], tc.school)
			for p, want := range tc.want {
				w, err := time.ParseInLocation("2006-01-02 15:04", "2026-"+want, time.UTC)
				if err != nil {
					t.Fatal(err)
				}
				g, ok := got[p]
				if !ok {
					t.Fatalf("%s: no time", p)
				}
				// aladhan and this differ by rounding at most.
				if d := g.Sub(w); d < -time.Minute || d > time.Minute {
					t.Errorf("%s = %s, want %s (off by %s)", p, g.Format("01-02 15:04"), want, d)
				}
			}
		})
	}
}

func TestUmmAlQuraCallsIshaNinetyMinutesAfterMaghrib(t *testing.T) {
	got := Times(2026, 11, 19, 21.4225, 39.8262, Methods[3], Shafi)
	if d := got[Isha].Sub(got[Maghrib]); d != 90*time.Minute {
		t.Fatalf("Isha is %s after Maghrib, want 90m", d)
	}
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
