package athan

import (
	"math"
	"time"
)

// Prayer names one of the daily times. Sunrise is not a prayer, but it bounds Fajr and the night.
type Prayer string

const (
	Fajr    Prayer = "Fajr"
	Sunrise Prayer = "Sunrise"
	Dhuhr   Prayer = "Dhuhr"
	Asr     Prayer = "Asr"
	Maghrib Prayer = "Maghrib"
	Isha    Prayer = "Isha"
)

// Prayers is the day in order, as SalatTimesBar lists it.
var Prayers = []Prayer{Fajr, Sunrise, Dhuhr, Asr, Maghrib, Isha}

// Calls are the prayers an athan is called for.
var Calls = []Prayer{Fajr, Dhuhr, Asr, Maghrib, Isha}

// Method is a calculation convention: the sun's depression for Fajr and Isha, or a fixed Isha delay.
type Method struct {
	Name       string
	Fajr       float64
	Isha       float64
	IshaMinute int
}

// Methods match aladhan.com's, which SalatTimesBar used, so the Dot agrees with the Mac app.
var Methods = []Method{
	{Name: "ISNA (North America)", Fajr: 15, Isha: 15},
	{Name: "Muslim World League", Fajr: 18, Isha: 17},
	{Name: "Egyptian General Authority", Fajr: 19.5, Isha: 17.5},
	{Name: "Umm al-Qura (Makkah)", Fajr: 18.5, IshaMinute: 90},
	{Name: "University of Karachi", Fajr: 18, Isha: 18},
}

// School sets the Asr shadow length: Shafi when a shadow equals its object, Hanafi at twice.
type School string

const (
	Shafi  School = "Shafi (earlier Asr)"
	Hanafi School = "Hanafi (later Asr)"
)

var Schools = []School{Shafi, Hanafi}

func (s School) factor() float64 {
	if s == Hanafi {
		return 2
	}
	return 1
}

// riseSet is the sun's centre below the horizon at sunrise and sunset, refraction included.
const riseSet = 0.833

// Times works out one local calendar day's times at a place, each in UTC and rounded to the minute.
func Times(year int, month time.Month, day int, lat, lng float64, m Method, s School) map[Prayer]time.Time {
	c := calc{lat: lat, lng: lng, jd: julian(year, int(month), day) - lng/(15*24)}

	// One pass from rough guesses, then a second from the first, as PrayTimes does.
	h := map[Prayer]float64{Fajr: 5, Sunrise: 6, Dhuhr: 12, Asr: 13, Maghrib: 18, Isha: 18}
	for range 2 {
		h = c.pass(h, m, s)
	}

	for p := range h {
		h[p] -= lng / 15
	}
	adjustHighLatitudes(h, m)
	if m.IshaMinute > 0 {
		h[Isha] = h[Maghrib] + float64(m.IshaMinute)/60
	}

	midnight := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	out := make(map[Prayer]time.Time, len(h))
	for p, hours := range h {
		if math.IsNaN(hours) {
			continue
		}
		out[p] = midnight.Add(time.Duration(hours * float64(time.Hour))).Round(time.Minute)
	}
	return out
}

type calc struct {
	lat, lng, jd float64
}

func (c calc) pass(guess map[Prayer]float64, m Method, s School) map[Prayer]float64 {
	part := func(p Prayer) float64 { return guess[p] / 24 }
	out := map[Prayer]float64{
		Fajr:    c.angleTime(m.Fajr, part(Fajr), true),
		Sunrise: c.angleTime(riseSet, part(Sunrise), true),
		Dhuhr:   c.midDay(part(Dhuhr)),
		Asr:     c.asrTime(s.factor(), part(Asr)),
		Maghrib: c.angleTime(riseSet, part(Maghrib), false),
	}
	if m.IshaMinute == 0 {
		out[Isha] = c.angleTime(m.Isha, part(Isha), false)
	} else {
		out[Isha] = out[Maghrib]
	}
	return out
}

func (c calc) midDay(t float64) float64 {
	_, eqt := sun(c.jd + t)
	return fixHour(12 - eqt)
}

// angleTime is when the sun is angle degrees below the horizon, before noon when rising.
func (c calc) angleTime(angle, t float64, rising bool) float64 {
	decl, _ := sun(c.jd + t)
	noon := c.midDay(t)
	cos := (-sin(angle) - sin(decl)*sin(c.lat)) / (cosd(decl) * cosd(c.lat))
	if cos < -1 || cos > 1 {
		return math.NaN()
	}
	d := acos(cos) / 15
	if rising {
		return noon - d
	}
	return noon + d
}

func (c calc) asrTime(factor, t float64) float64 {
	decl, _ := sun(c.jd + t)
	angle := -acot(factor + tan(math.Abs(c.lat-decl)))
	return c.angleTime(angle, t, false)
}

// adjustHighLatitudes keeps Fajr and Isha within the angle's share of the night, aladhan's default.
func adjustHighLatitudes(h map[Prayer]float64, m Method) {
	night := fixHour(h[Sunrise] - h[Maghrib])

	if portion := m.Fajr / 60 * night; math.IsNaN(h[Fajr]) || fixHour(h[Sunrise]-h[Fajr]) > portion {
		h[Fajr] = h[Sunrise] - portion
	}
	if m.IshaMinute > 0 {
		return
	}
	if portion := m.Isha / 60 * night; math.IsNaN(h[Isha]) || fixHour(h[Isha]-h[Maghrib]) > portion {
		h[Isha] = h[Maghrib] + portion
	}
}

// sun is the sun's declination and the equation of time, in degrees and hours, on a Julian date.
func sun(jd float64) (decl, eqt float64) {
	d := jd - 2451545.0
	g := fixAngle(357.529 + 0.98560028*d)
	q := fixAngle(280.459 + 0.98564736*d)
	l := fixAngle(q + 1.915*sin(g) + 0.020*sin(2*g))
	e := 23.439 - 0.00000036*d

	ra := atan2(cosd(e)*sin(l), cosd(l)) / 15
	return asin(sin(e) * sin(l)), q/15 - fixHour(ra)
}

func julian(year, month, day int) float64 {
	if month <= 2 {
		year--
		month += 12
	}
	a := math.Floor(float64(year) / 100)
	b := 2 - a + math.Floor(a/4)
	return math.Floor(365.25*float64(year+4716)) + math.Floor(30.6001*float64(month+1)) +
		float64(day) + b - 1524.5
}

const rad = math.Pi / 180

func sin(d float64) float64  { return math.Sin(d * rad) }
func cosd(d float64) float64 { return math.Cos(d * rad) }
func tan(d float64) float64  { return math.Tan(d * rad) }
func asin(x float64) float64 { return math.Asin(x) / rad }
func acos(x float64) float64 { return math.Acos(x) / rad }
func acot(x float64) float64 { return math.Atan(1/x) / rad }

func atan2(y, x float64) float64 { return math.Atan2(y, x) / rad }

func fixAngle(a float64) float64 { return fix(a, 360) }
func fixHour(a float64) float64  { return fix(a, 24) }

func fix(a, b float64) float64 {
	a -= b * math.Floor(a/b)
	if a < 0 {
		a += b
	}
	return a
}
