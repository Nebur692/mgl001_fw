package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostLabel(t *testing.T) {
	for v, want := range map[string]bool{
		"BurbuHub-A1B2": true, "x": true, "a1-b2": true, strings.Repeat("a", 63): true,
		"": false, "-a": false, "a-": false, "a_b": false, "a b": false, "ñandú": false, "a.b": false,
		strings.Repeat("a", 64): false,
	} {
		if got := hostLabel.MatchString(v); got != want {
			t.Errorf("%q: %v, want %v", v, got, want)
		}
	}
	if n := sanitizeNodeName("Mijia_Hub_V2-A1B2"); n != "mijia-hub-v2-a1b2" {
		t.Errorf("node name %q", n)
	}
}

// The ESPHome device name is fixed the first time and never follows the host name again.
func TestDeviceNameIsKept(t *testing.T) {
	dir := t.TempDir()
	old := *flagNodeFile
	*flagNodeFile = filepath.Join(dir, "node_name")
	defer func() { *flagNodeFile = old }()

	h, _ := os.Hostname()
	if n := deviceName(); n != sanitizeNodeName(h) {
		t.Fatalf("first start: %q", n)
	}
	_ = os.WriteFile(*flagNodeFile, []byte("mijia-hub-v2-a1b2\n"), 0644)
	if n := deviceName(); n != "mijia-hub-v2-a1b2" {
		t.Fatalf("saved name not used: %q", n)
	}
	_ = os.WriteFile(*flagNodeFile, []byte("Not Valid!\n"), 0644) // broken file: made again
	if n := deviceName(); n != sanitizeNodeName(h) {
		t.Fatalf("broken file: %q", n)
	}
}

func TestSetHostnameSavesOnlyValid(t *testing.T) {
	dir := t.TempDir()
	oldF, oldD := *flagHostFile, *flagHostDry
	*flagHostFile, *flagHostDry = filepath.Join(dir, "hostname"), true
	defer func() { *flagHostFile, *flagHostDry = oldF, oldD }()

	if err := setHostname(" BurbuHub-A1B2 "); err != nil {
		t.Fatal(err)
	}
	if hostnameState().State != "BurbuHub-A1B2" {
		t.Fatalf("state %q", hostnameState().State)
	}
	for _, bad := range []string{"", "-x", "a_b", "con espacio", strings.Repeat("a", 64)} {
		if err := setHostname(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if b, _ := os.ReadFile(*flagHostFile); string(b) != "BurbuHub-A1B2\n" {
		t.Fatalf("a refused name changed the file: %q", b)
	}
}
