package main

import (
	"os"
	"path/filepath"
	"testing"
)

func fakeSys(t *testing.T, files map[string]string) {
	dir := t.TempDir()
	for p, v := range files {
		f := filepath.Join(dir, p)
		_ = os.MkdirAll(filepath.Dir(f), 0755)
		if err := os.WriteFile(f, []byte(v), 0644); err != nil {
			t.Fatal(err)
		}
	}
	old := sysRoot
	sysRoot = dir
	t.Cleanup(func() { sysRoot = old })
}

func TestSystemReaders(t *testing.T) {
	// As the gateway shows them (17-10-2026) / Como los da la pasarela
	fakeSys(t, map[string]string{
		"sys/class/mstar/msys/TEMP_R":      "Temperature 48\n",
		"sys/class/mstar/msys/REBOOT_TYPE": "Reboot_Type: 2 (SW)\n",
		"sys/class/net/eth0/carrier":       "1\n",
		"sys/class/net/eth0/speed":         "100\n",
	})
	if v, ok := cpuTemp(); !ok || v != 48 {
		t.Errorf("cpu temp %v %v", v, ok)
	}
	if r := rebootReason(); r != "Software" {
		t.Errorf("reboot %q", r)
	}
	if !ethLink() || ethSpeed() != 100 {
		t.Errorf("ethernet %v %v", ethLink(), ethSpeed())
	}

	for raw, want := range map[string]string{
		"Reboot_Type: 1 (WDT)\n":     "Watchdog",
		"Reboot_Type: 0 (COLD)\n":    "Power on",
		"Reboot_Type: 3 (HW)\n":      "Power on",
		"Reboot_Type: 9 (STRANGE)\n": "STRANGE",
	} {
		fakeSys(t, map[string]string{"sys/class/mstar/msys/REBOOT_TYPE": raw})
		if r := rebootReason(); r != want {
			t.Errorf("%q -> %q, want %q", raw, r, want)
		}
	}

	// Link down: the kernel says -1 / Enlace caído: el kernel dice -1
	fakeSys(t, map[string]string{"sys/class/net/eth0/carrier": "0\n", "sys/class/net/eth0/speed": "-1\n",
		"sys/class/mstar/msys/TEMP_R": "garbage\n"})
	if ethLink() || ethSpeed() != 0 {
		t.Error("a down link is seen as up")
	}
	if _, ok := cpuTemp(); ok {
		t.Error("garbage read as a temperature")
	}
	// Nothing there (not a SigmaStar) / Nada (no es un SigmaStar)
	fakeSys(t, map[string]string{})
	if _, ok := cpuTemp(); ok || rebootReason() != "" || ethLink() {
		t.Error("readers invent values")
	}
}
