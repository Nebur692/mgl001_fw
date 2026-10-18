package main

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"strings"
	"testing"
)

// simRescue replicates rescate.c (bt-firmware patch): the checks of every command and the flash rules
// (writes only clear bits, erase is 4 KB, nothing in the running bank but clearing its mark).
type simRescue struct {
	r       *rescueLink
	flash   []byte
	running uint32
	writes  []uint32 // address of every write, in order
	garble  int      // answer this many frames with "bad CRC" first
}

func newSimRescue(running uint32) *simRescue {
	s := &simRescue{flash: bytes.Repeat([]byte{0xFF}, rscFlashSize), running: running}
	s.r = &rescueLink{w: s, in: make(chan byte, 1<<16)}
	return s
}

func (s *simRescue) answer(cmd, st byte, d []byte) {
	f := []byte{rscStart, cmd | 0x80, byte(len(d) + 1), byte((len(d) + 1) >> 8), st}
	f = append(f, d...)
	f = binary.LittleEndian.AppendUint32(f, crc32.ChecksumIEEE(f))
	for _, b := range f {
		s.r.in <- b
	}
}

func (s *simRescue) other() uint32 { return rescueInfo{Running: s.running}.other() }

func (s *simRescue) inOther(a, n uint32) bool {
	return n <= rscBankMaxFw && a >= s.other() && a+n <= s.other()+rscBankMaxFw
}

func (s *simRescue) Write(f []byte) (int, error) {
	n := int(binary.LittleEndian.Uint16(f[2:]))
	cmd, p := f[1], f[4:4+n]
	if crc32.ChecksumIEEE(f[:4+n]) != binary.LittleEndian.Uint32(f[4+n:]) || s.garble > 0 {
		s.garble--
		s.answer(cmd, 1, nil)
		return len(f), nil
	}
	var a uint32
	if n >= 4 {
		a = binary.LittleEndian.Uint32(p)
	}
	switch cmd {
	case rscRead:
		k := uint32(binary.LittleEndian.Uint16(p[4:]))
		s.answer(cmd, 0, s.flash[a:a+k])
	case rscErase:
		if a&0xFFF != 0 || !s.inOther(a, 0x1000) {
			s.answer(cmd, 2, nil)
			return len(f), nil
		}
		copy(s.flash[a:a+0x1000], bytes.Repeat([]byte{0xFF}, 0x1000))
		s.answer(cmd, 0, nil)
	case rscWrite:
		d := p[4:]
		if !s.inOther(a, uint32(len(d))) {
			s.answer(cmd, 2, nil)
			return len(f), nil
		}
		s.writes = append(s.writes, a)
		for i, b := range d {
			s.flash[a+uint32(i)] &= b
		}
		s.answer(cmd, 0, nil)
	case rscInvalidate:
		s.flash[s.running+8] = 0
		s.answer(cmd, 0, nil)
	case rscReboot:
		s.answer(cmd, 0, nil)
	default:
		s.answer(cmd, 4, nil)
	}
	return len(f), nil
}

func testImage(n int, fill byte) []byte {
	img := bytes.Repeat([]byte{fill}, n-4)
	copy(img[8:], "KNLT")
	return SealImage(img)
}

// The 17-10-2026 state: our hung image boots from bank 1, Xiaomi's bank 0 has its mark at 0x00
func TestRescueWriteImageIntoTheOtherBank(t *testing.T) {
	s := newSimRescue(rscBankSize)
	hung := testImage(0x8000, 0x11)
	copy(s.flash[rscBankSize:], hung)
	copy(s.flash, testImage(0x9000, 0x22))
	s.flash[8] = 0
	xiaomi := testImage(82708, 0x33)
	info := rescueInfo{Running: rscBankSize}
	if err := s.r.WriteImage(info, xiaomi, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(s.flash[:len(xiaomi)], xiaomi) {
		t.Fatal("bank 0 does not hold the image")
	}
	if !bytes.Equal(s.flash[len(xiaomi):rscBankMaxFw], bytes.Repeat([]byte{0xFF}, rscBankMaxFw-len(xiaomi))) {
		t.Fatal("the rest of bank 0 is not erased (old image left behind)")
	}
	if s.flash[8] != 0x4B || s.flash[rscBankSize+8] != 0x00 {
		t.Fatalf("boot marks %#x / %#x, want 0x4b / 0x00", s.flash[8], s.flash[rscBankSize+8])
	}
	if s.writes[len(s.writes)-1] != 0 {
		t.Fatalf("the first 16 bytes (boot mark) must be written last, last write at %#x", s.writes[len(s.writes)-1])
	}
	for _, w := range s.writes[:len(s.writes)-1] {
		if w < 16 {
			t.Fatalf("a write before the end touched the boot mark area (%#x)", w)
		}
	}
}

func TestRescueRunningFromBank0WritesBank1(t *testing.T) {
	s := newSimRescue(0)
	img := testImage(0x4000, 0x44)
	if err := s.r.WriteImage(rescueInfo{Running: 0}, img, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(s.flash[rscBankSize:rscBankSize+len(img)], img) || s.flash[8] != 0 {
		t.Fatal("bank 1 not written or bank 0 mark not cleared")
	}
}

func TestRescueRefusesBadImages(t *testing.T) {
	s := newSimRescue(rscBankSize)
	bad := testImage(0x4000, 0x55)
	bad[100] ^= 1
	if err := s.r.WriteImage(rescueInfo{Running: rscBankSize}, bad, func(string) {}); err == nil {
		t.Fatal("an image with a bad CRC was written")
	}
	big := testImage(rscBankMaxFw+4, 0x66)
	if err := s.r.WriteImage(rescueInfo{Running: rscBankSize}, big, func(string) {}); err == nil || !strings.Contains(err.Error(), "does not fit") {
		t.Fatalf("an image bigger than a bank: %v", err)
	}
	if len(s.writes) != 0 {
		t.Fatal("something was written")
	}
}

func TestRescueRefusalStops(t *testing.T) {
	s := newSimRescue(rscBankSize)
	// the session thinks bank 0 runs: every erase lands in the chip's running bank and is refused
	err := s.r.WriteImage(rescueInfo{Running: 0}, testImage(0x4000, 0x77), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "address refused") {
		t.Fatalf("got %v", err)
	}
	if len(s.writes) != 0 {
		t.Fatal("it went on writing after a refusal")
	}
}

func TestRescueGarbledFrameIsSentAgain(t *testing.T) {
	s := newSimRescue(rscBankSize)
	s.flash[0x1234] = 0x5A
	s.garble = 2
	d, err := s.r.ReadFlash(0x1234, 1)
	if err != nil || d[0] != 0x5A {
		t.Fatalf("got %x %v", d, err)
	}
	s.garble = 3
	if _, err := s.r.ReadFlash(0x1234, 1); err == nil {
		t.Fatal("three garbled frames in a row must fail")
	}
}

func TestRescueInfoParse(t *testing.T) {
	i, err := parseRescueInfo([]byte{1, 0, 0, 2, 0, 0x85, 0x60, 0x13, 0, 0x00, 0x4B, 3, 0})
	if err != nil || i.Running != rscBankSize || i.MID != 0x136085 || i.Mark0 != 0 || i.Mark1 != 0x4B || i.Revision != 3 || i.other() != 0 {
		t.Fatalf("%+v %v", i, err)
	}
	if _, err := parseRescueInfo([]byte{1, 2}); err == nil {
		t.Fatal("short info accepted")
	}
}
