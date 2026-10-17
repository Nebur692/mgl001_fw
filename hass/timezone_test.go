package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestTZifFromPOSIX(t *testing.T) {
	cases := []struct {
		rule string
		at   string // UTC
		name string
		off  int
	}{
		// Madrid: CET/CEST, change on the last Sunday of March at 01:00 UTC
		{"CET-1CEST,M3.5.0,M10.5.0/3", "2026-01-15T12:00:00Z", "CET", 3600},
		{"CET-1CEST,M3.5.0,M10.5.0/3", "2026-07-15T12:00:00Z", "CEST", 7200},
		{"CET-1CEST,M3.5.0,M10.5.0/3", "2026-03-29T00:59:59Z", "CET", 3600},
		{"CET-1CEST,M3.5.0,M10.5.0/3", "2026-03-29T01:00:00Z", "CEST", 7200},
		{"CET-1CEST,M3.5.0,M10.5.0/3", "2026-10-25T01:00:00Z", "CET", 3600},
		// New York: west of Greenwich (positive offset in POSIX)
		{"EST5EDT,M3.2.0,M11.1.0", "2026-01-15T12:00:00Z", "EST", -5 * 3600},
		{"EST5EDT,M3.2.0,M11.1.0", "2026-07-15T12:00:00Z", "EDT", -4 * 3600},
		// Quoted name, no DST; and plain UTC
		{"<+03>-3", "2026-07-15T12:00:00Z", "+03", 3 * 3600},
		{"UTC0", "2026-07-15T12:00:00Z", "UTC", 0},
		// Before 1970 the file's own type applies (not the rule): its offset must be right too
		{"CET-1CEST,M3.5.0,M10.5.0/3", "1960-07-15T12:00:00Z", "CET", 3600},
		{"EST5EDT,M3.2.0,M11.1.0", "1960-07-15T12:00:00Z", "EST", -5 * 3600},
		// Minutes in the offset (India)
		{"IST-5:30", "2026-07-15T12:00:00Z", "IST", 5*3600 + 1800},
	}
	for _, c := range cases {
		data, err := tzifFromPOSIX(c.rule)
		if err != nil {
			t.Fatalf("%s: %v", c.rule, err)
		}
		loc, err := time.LoadLocationFromTZData("x", data)
		if err != nil {
			t.Fatalf("%s: not a valid TZif: %v", c.rule, err)
		}
		at, _ := time.Parse(time.RFC3339, c.at)
		name, off := at.In(loc).Zone()
		if name != c.name || off != c.off {
			t.Errorf("%s at %s: %s %d, want %s %d", c.rule, c.at, name, off, c.name, c.off)
		}
		if got := tzifFooter(data); got != c.rule {
			t.Errorf("footer %q, want %q", got, c.rule)
		}
	}
	for _, bad := range []string{"", "X1", "CET", "CET-1\nX", "<+03"} {
		if _, err := tzifFromPOSIX(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestApplyWritesOnlyWhenItChanges(t *testing.T) {
	dir := t.TempDir()
	file, link := filepath.Join(dir, "localtime"), filepath.Join(dir, "link")
	z := NewTimeZone(file, link)
	if z.Text() != "" {
		t.Fatal("text before any answer")
	}
	z.Apply("CET-1CEST,M3.5.0,M10.5.0/3", 0)
	if target, err := os.Readlink(link); err != nil || target != file {
		t.Fatalf("link -> %q (%v), want %q", target, err, file)
	}
	st1, _ := os.Stat(file)
	time.Sleep(20 * time.Millisecond)
	z.Apply("CET-1CEST,M3.5.0,M10.5.0/3", 0)
	st2, _ := os.Stat(file)
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("the same zone was written to flash again")
	}
	if ok, _ := regexp.MatchString(`^(CET|CEST) \(UTC\+0[12]:00\)$`, z.Text()); !ok {
		t.Fatalf("text %q", z.Text())
	}
	// A new process finds the saved zone / un proceso nuevo encuentra la zona guardada
	if z2 := NewTimeZone(file, link); z2.Text() != z.Text() {
		t.Fatalf("after a restart: %q, want %q", z2.Text(), z.Text())
	}
	z.Apply("EST5EDT,M3.2.0,M11.1.0", 0)
	if b, _ := os.ReadFile(file); tzifFooter(b) != "EST5EDT,M3.2.0,M11.1.0" {
		t.Fatal("a new zone was not written")
	}
	// A bad rule changes nothing / una regla mala no cambia nada
	z.Apply("garbage", 0)
	if b, _ := os.ReadFile(file); tzifFooter(b) != "EST5EDT,M3.2.0,M11.1.0" {
		t.Fatal("a bad rule replaced the zone")
	}
}

// A link that cannot be made is reported, and a missing one is not an error.
func TestRelinkErrors(t *testing.T) {
	dir := t.TempDir()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	z := NewTimeZone(filepath.Join(dir, "localtime"), filepath.Join(dir, "link"))
	z.Apply("UTC0", 0)
	if strings.Contains(logs.String(), "time zone:") {
		t.Fatalf("a link that did not exist yet was logged as an error: %s", logs.String())
	}
	logs.Reset()
	z2 := NewTimeZone(filepath.Join(dir, "localtime"), filepath.Join(dir, "no-such-dir", "link"))
	z2.Apply("CET-1CEST,M3.5.0,M10.5.0/3", 0)
	if !strings.Contains(logs.String(), "time zone:") {
		t.Fatal("a link that could not be made was not reported")
	}
}
