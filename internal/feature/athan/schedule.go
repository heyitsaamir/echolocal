package athan

import (
	"slices"
	"time"
)

// Call is one prayer on one day.
type Call struct {
	Prayer Prayer
	At     time.Time
}

// Key names a call uniquely, so it is played or skipped once.
func (c Call) Key() string { return c.At.UTC().Format("2006-01-02T15:04") + " " + string(c.Prayer) }

// Grace is how late an athan may still start, for a tick or a reboot that lands just after the time.
const Grace = 2 * time.Minute

// Day is one local calendar day's times, in the zone now carries.
func Day(s Settings, now time.Time) map[Prayer]time.Time {
	lat, lng, err := s.Place()
	if err != nil {
		return nil
	}
	y, m, d := now.Date()
	out := Times(y, m, d, lat, lng, s.method(), s.School)
	for p, t := range out {
		out[p] = t.In(now.Location())
	}
	return out
}

// Schedule is the calls the settings want around now: yesterday's for the grace window, today's and tomorrow's.
func Schedule(s Settings, now time.Time) []Call {
	if _, _, err := s.Place(); err != nil {
		return nil
	}
	var out []Call
	for _, offset := range []int{-1, 0, 1} {
		times := Day(s, now.AddDate(0, 0, offset))
		for _, p := range Calls {
			if t, ok := times[p]; ok && s.Calls(p) {
				out = append(out, Call{Prayer: p, At: t})
			}
		}
	}
	slices.SortFunc(out, func(a, b Call) int { return a.At.Compare(b.At) })
	return out
}

// Due is the call that has just come, if one has.
func Due(calls []Call, now time.Time) (Call, bool) {
	for _, c := range calls {
		if !now.Before(c.At) && now.Before(c.At.Add(Grace)) {
			return c, true
		}
	}
	return Call{}, false
}

// Next is the first call still to come.
func Next(calls []Call, now time.Time) (Call, bool) {
	for _, c := range calls {
		if c.At.After(now) {
			return c, true
		}
	}
	return Call{}, false
}

// Nearness is how far through the warning window now is: 0 at its start, 1 at the athan, -1 outside it.
func Nearness(c Call, now time.Time, warning time.Duration) float64 {
	left := c.At.Sub(now)
	if warning <= 0 || left <= 0 || left > warning {
		return -1
	}
	return 1 - float64(left)/float64(warning)
}
