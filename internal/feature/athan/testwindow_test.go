//go:build athanfind

package athan

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestFindTestLongitude finds a longitude whose next athan is eight minutes away, to watch the ring and
// the call on a real device: go test -tags athanfind -v -run TestFindTestLongitude ./internal/feature/athan/
func TestFindTestLongitude(t *testing.T) {
	loc, _ := time.LoadLocation("America/Los_Angeles")
	now := time.Now().In(loc).Add(40 * time.Second) // time for the settings to reach the Dot
	target := now.Add(8 * time.Minute).Truncate(time.Minute)
	s := Defaults()
	s.Lat = "47.493"
	for lng := -122.149; lng > -179; lng -= 0.01 {
		s.Lng = fmt.Sprintf("%.2f", lng)
		calls := Schedule(s, now)
		if _, due := Due(calls, now); due {
			continue
		}
		next, ok := Next(calls, now)
		if ok && next.At.Equal(target) {
			fmt.Fprintf(os.Stdout, "LNG=%s NEXT=%s AT=%s NOW=%s\n", s.Lng, next.Prayer, next.At.Format("15:04"), time.Now().In(loc).Format("15:04:05"))
			return
		}
	}
	t.Fatal("none found")
}
