package main

import "testing"

func TestLanVersion(t *testing.T) {
	for name, want := range map[string]string{
		"mgl001_1.0.8_0013_lan20261017":   "2026.10.17",
		"mgl001_1.0.8_0013_lan20261017.2": "2026.10.17.2",
		"mgl001_1.0.9_0002_lan20270105":   "2027.1.5",
		"mgl001_1.0.8_0013_mod20261016.2": "",
		"mgl001_1.0.8_0013":               "",
		"stock":                           "",
	} {
		if got := lanVersion(name); got != want {
			t.Errorf("lanVersion(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestVersionNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"2026.10.18", "2026.10.17", true},
		{"2026.10.17.2", "2026.10.17", true},
		{"2026.10.17", "2026.10.17.2", false},
		{"2026.10.17", "2026.10.17", false},
		{"2027.1.5", "2026.12.31", true},
		{"2026.9.30", "2026.10.1", false}, // compared as text, "9" > "1" would say true
	}
	for _, c := range cases {
		if got := versionNewer(c.a, c.b); got != c.want {
			t.Errorf("versionNewer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestNewestLAN(t *testing.T) {
	list := "# name|kind|kernel size|kernel sha256|rootfs size|rootfs sha256\n" +
		"mgl001_1.0.8_0013_lan20261017|custom|1|a|2|b\n" +
		"mgl001_1.0.8_0013_lan20261102|custom|1|a|2|b\n" +
		"mgl001_1.0.8_0013_lan20261017.2|custom|1|a|2|b\n" +
		"mgl001_1.0.8_0013_mod20261231|custom|1|a|2|b\n" +
		"mgl001_1.0.8_0013_lan20261231|stock|1|a|2|b\n" +
		"mgl001_1.0.8_0013|stock|1|a|2|b\n"
	if got := newestLAN(list); got != "mgl001_1.0.8_0013_lan20261102" {
		t.Errorf("newestLAN = %q", got)
	}
	if got := newestLAN("# only a header\n"); got != "" {
		t.Errorf("newestLAN of an empty list = %q", got)
	}
}

// The entity never offers an older build, nor anything when the running firmware is not a LAN build.
func TestStateNeverOffersOlder(t *testing.T) {
	cases := []struct {
		current, latest, wantLatest string
	}{
		{"mgl001_1.0.8_0013_lan20261017", "mgl001_1.0.8_0013_lan20261102", "2026.11.2"},
		{"mgl001_1.0.8_0013_lan20261102", "mgl001_1.0.8_0013_lan20261017", "2026.11.2"},
		{"mgl001_1.0.8_0013_lan20261017", "", "2026.10.17"},
		{"mgl001_1.0.8_0013_mod20261016.2", "mgl001_1.0.8_0013_lan20261102", "mgl001_1.0.8_0013_mod20261016.2"},
	}
	for _, c := range cases {
		u := &Updater{current: c.current, latest: c.latest}
		if s := u.State(); s.LatestVersion != c.wantLatest {
			t.Errorf("current %s, newest %q: latest_version %q, want %q", c.current, c.latest, s.LatestVersion, c.wantLatest)
		}
	}
}
