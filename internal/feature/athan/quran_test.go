package athan

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEverySurahNameReadsBackAsItsNumber(t *testing.T) {
	names := SurahNames()
	if len(names) != 114 || names[17] != "18. Al-Kahf (The Cave)" {
		t.Fatalf("got %d names, 18th %q", len(names), names[17])
	}
	for i, name := range names {
		if n, ok := surahNumber(name); !ok || n != i+1 {
			t.Fatalf("%q read back as %d %v", name, n, ok)
		}
	}
	for _, bad := range []string{"", "18. Al-Kahf", "115. Nothing", "Al-Kahf (The Cave)"} {
		if _, ok := surahNumber(bad); ok {
			t.Errorf("%q was taken", bad)
		}
	}
}

func TestSurahURLsAreTvqurans(t *testing.T) {
	if got := SurahURL(18); got != "https://download.tvquran.com/download/recitations/90/25/018.mp3" {
		t.Fatal(got)
	}
}

func TestTheCacheDropsWhatWasPlayedLongestAgo(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	put := func(name string, size int, ago time.Duration) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		at := now.Add(-ago)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	put("old.mp3", 400, 3*time.Hour)
	put("mid.mp3", 400, 2*time.Hour)
	put("new.mp3", 400, time.Hour)
	put("fetch-stale", 50, 2*time.Hour)
	put("fetch-live", 50, time.Minute)

	Evict(dir, 800)

	for name, want := range map[string]bool{
		"old.mp3": false, "mid.mp3": true, "new.mp3": true, "fetch-stale": false, "fetch-live": true,
	} {
		_, err := os.Stat(filepath.Join(dir, name))
		if got := err == nil; got != want {
			t.Errorf("%s kept = %v, want %v", name, got, want)
		}
	}
}

func TestABuildThatCouldNotReciteStartsAtAlFatihah(t *testing.T) {
	old := Defaults()
	old.Version, old.Surah, old.QuranVolume = 3, 0, 0
	got := old.usable()
	if got.Surah != 1 || got.QuranVolume != 60 || got.Version != settingsVersion {
		t.Fatalf("not carried forward: %+v", got)
	}
	got.Surah = 200
	if got.usable().Surah != 1 {
		t.Fatal("an impossible surah was kept")
	}
}
