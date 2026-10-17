package main

import (
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

// Made by bellows 1.1.0's own ASH encoder (the library ZHA uses), not by ours: an RST, then version
// (old and EZSP 8+ headers), getValue VERSION_INFO (seq 2) and getValue of something else (seq 3).
// The chip answers EZSP 13 / stack 7.4.4.0, with a broken-CRC answer saying EZSP 99 in between, the
// other getValue first (all 9s), then build 123, an XNCP chip-info reply and the XNCP build string.
// Some bytes are escaped (0x7D).
const (
	vecHost = "1ac038bc7e004221a8597c057e104321a9542a7d3821bb7e204021a9fe2a041f367e304121a9fe2a147fdc7e"
	vecChip = "c1020b0a527e0142a1a8592855c6a6c87e1243a1a9542a76b049f11d167e1243a1a9542a7d38b019e0ea307e" +
		"2341a1a9fe2a15b5509d432ca35c9b0a7d5e7e3440a1a9fe2a15b522944d21ae5538600c7e4546a1a97d332a15bf5c14" +
		"4a25aa54924cd9087598df9c5d7e5647a1a97d332a15ae5a144a76e675a079ae7a099dc3fc4bab83b82de89e0b7cc698" +
		"aca07d3181107e"
)

func runVectors(t *testing.T, chunk int) []ZigbeeFirmware {
	host, _ := hex.DecodeString(vecHost)
	chip, _ := hex.DecodeString(vecChip)
	var seen []ZigbeeFirmware
	w := newEZSPWatcher(func(fw ZigbeeFirmware) { seen = append(seen, fw) })
	feed := func(f func([]byte), b []byte) {
		for len(b) > 0 {
			n := min(chunk, len(b))
			f(b[:n])
			b = b[n:]
		}
	}
	feed(w.FromHost, host) // ZHA asks first, as on the wire
	feed(w.FromChip, chip)
	return seen
}

func TestEZSPFirmwareFromTraffic(t *testing.T) {
	for _, chunk := range []int{1, 7, 4096} { // byte by byte, in pieces, all at once
		seen := runVectors(t, chunk)
		if len(seen) == 0 {
			t.Fatalf("chunk %d: nothing seen", chunk)
		}
		last := seen[len(seen)-1]
		if got := last.Text(); got != "EmberZNet 7.4.4.0 build 123 (SL 2024.6.2, ~~NabuCasa~~)" {
			t.Errorf("chunk %d: firmware %q", chunk, got)
		}
		if got := last.ProtocolText(); got != "EZSP 13" {
			t.Errorf("chunk %d: protocol %q", chunk, got)
		}
		// Before VERSION_INFO, the stack version from the version answer alone
		if seen[0].Text() != "EmberZNet 7.4.4.0" {
			t.Errorf("chunk %d: first %q", chunk, seen[0].Text())
		}
		for _, fw := range seen {
			if fw.Protocol == 99 || strings.Contains(fw.Stack, "9.9.9") || strings.Contains(fw.Build, "EFR32") {
				t.Errorf("chunk %d: took a frame it should have skipped: %+v", chunk, fw)
			}
		}
	}
}

// Garbage, a cancelled frame and an overlong one never panic or invent a version.
func TestEZSPGarbage(t *testing.T) {
	w := newEZSPWatcher(func(fw ZigbeeFirmware) { t.Errorf("invented %+v", fw) })
	w.FromChip([]byte{0x7E, 0x7E, 0x00, 0x7E, 0x7D, 0x7E, 0x01, 0x02, 0x1A, 0x7E})
	w.FromChip(make([]byte, 1000))
	w.FromChip([]byte{0x7E})
	w.FromHost([]byte{0x7D, 0x7D, 0x18, 0x7E})
}

func TestZigbeeFirmwareFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "info")
	if f, pr := loadZigbeeFirmware(p); f != "" || pr != "" {
		t.Fatal("values without a file")
	}
	fw := ZigbeeFirmware{Protocol: 13, Stack: "7.4.4.0 build 0"}
	if err := saveZigbeeFirmware(p, fw); err != nil {
		t.Fatal(err)
	}
	if f, pr := loadZigbeeFirmware(p); f != "EmberZNet 7.4.4.0 build 0" || pr != "EZSP 13" {
		t.Fatalf("read back %q %q", f, pr)
	}
}

func TestBluetoothTexts(t *testing.T) {
	if s := btFirmwareText(0x0211, 0x0001); s != "Telink, revision 0x0001" {
		t.Errorf("%q", s)
	}
	if s := btFirmwareText(0x000F, 0x1234); s != "manufacturer 0x000f, revision 0x1234" {
		t.Errorf("%q", s)
	}
	if s := btVersionText(9); s != "Bluetooth 5.0 (HCI 9)" {
		t.Errorf("%q", s)
	}
	if s := btVersionText(42); s != "HCI 42" {
		t.Errorf("%q", s)
	}
}
