package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"testing"
	"time"
)

// simTelink replicates vendor/common/hci_transport/hci_dfu.c of tc_ble_sdk (DFU_*CmdHandler).
type simTelink struct {
	flash      []byte // 0x40000: bank 0 at 0, bank 1 at 0x20000
	next       int    // nextFwAddrStart
	maxSize    int
	started    bool
	size, off  int
	crc        uint32
	resend     int
	corruptAt  int // offset whose first send gets a bad CRC (-1: none)
	corrupted  bool
	failAfter  int // stop answering after this many data packets (-1: never)
	dataPkts   int
	terminated bool
}

func newSim(oldImg []byte) *simTelink {
	s := &simTelink{flash: bytes.Repeat([]byte{0xFF}, 0x40000), next: 0x20000, maxSize: 0x20000 - 0x1000, corruptAt: -1, failAfter: -1}
	copy(s.flash, oldImg)
	return s
}

func (s *simTelink) Command(op uint16, p []byte, _ time.Duration) (byte, []byte, error) {
	switch op {
	case opStartDFU:
		if s.started {
			return 0x0C, nil, nil
		}
		size := int(binary.LittleEndian.Uint32(p[3:]))
		if size > s.maxSize {
			return 0x12, nil, nil // HCI_ERR_INVALID_PARAM
		}
		if p[0] != dfuModeAlways {
			return 0x12, nil, nil
		}
		s.started, s.size, s.off, s.crc, s.resend = true, size, 0, 0xFFFFFFFF, 0
		for i := s.next; i < s.next+0x20000; i++ { // DFU_Reset erases the new area
			s.flash[i] = 0xFF
		}
		return 0, []byte{dfuPayload, 0}, nil
	case opFwData:
		s.dataPkts++
		if s.failAfter >= 0 && s.dataPkts > s.failAfter {
			return 0, nil, errors.New("no answer (link lost)")
		}
		if s.resend >= 3 {
			return 0, nil, errors.New("ignored after 3 resends")
		}
		if !s.started {
			return 0xA1, nil, nil
		}
		data := append([]byte(nil), p...)
		if s.off == s.corruptAt && !s.corrupted {
			s.corrupted = true
			data[0] ^= 0xFF
		}
		n := len(data) - 4
		if n > dfuPayload {
			return 0xA2, nil, nil
		}
		if sdkCrc32(0xFFFFFFFF, data) != 0 {
			s.resend++
			return 0xA3, nil, nil
		}
		s.resend = 0
		if s.off >= s.size {
			return 0xA5, nil, nil
		}
		chunk := data[:n]
		boot := byte(0)
		if s.off == 0 && n >= 8 {
			boot, chunk[8] = chunk[8], 0xFF
		}
		copy(s.flash[s.next+s.off:], chunk)
		rd := append([]byte(nil), s.flash[s.next+s.off:s.next+s.off+n]...)
		if s.off == 0 {
			rd[8] = boot
		}
		s.crc = sdkCrc32(s.crc, rd)
		s.off += n
		return 0, nil, nil
	case opEndDFU:
		if p[0] == dfuEndTerminate {
			s.terminated, s.started = true, false
			return 0, nil, nil
		}
		if s.off != s.size {
			return 0xA5, nil, nil
		}
		if s.crc != 0 {
			return 0xA4, nil, nil
		}
		s.flash[s.next+8] = 0x4B
		old := 0
		if s.next == 0 {
			old = 0x20000
		}
		s.flash[old+8] = 0x00
		s.started = false
		return 0, nil, nil
	}
	return 0x01, nil, nil
}

func xiaomiImage(t *testing.T) []byte {
	b, err := os.ReadFile("testdata/xiaomi_0x3d.bin")
	if err != nil {
		t.Skip("testdata/xiaomi_0x3d.bin not present")
	}
	return b
}

func ourImage(t *testing.T) []byte {
	b, err := os.ReadFile("testdata/ble_controller.bin")
	if err != nil {
		t.Skip("testdata/ble_controller.bin not present")
	}
	return SealImage(b)
}

func TestSealReproducesXiaomi(t *testing.T) {
	x := xiaomiImage(t)
	if got := SealImage(x[:len(x)-4]); !bytes.Equal(got, x) {
		t.Fatal("SealImage of Xiaomi's image without its CRC does not give Xiaomi's file back")
	}
	if err := CheckImage(x); err != nil {
		t.Fatal(err)
	}
}

func TestFlashOK(t *testing.T) {
	old, img := xiaomiImage(t), ourImage(t)
	s := newSim(old)
	if err := FlashTelink(s, img, 3, nil); err != nil {
		t.Fatal(err)
	}
	if s.flash[0x20008] != 0x4B || s.flash[8] != 0x00 {
		t.Fatalf("boot flags new=%#x old=%#x", s.flash[0x20008], s.flash[8])
	}
	if !bytes.Equal(s.flash[0x20000:0x20000+len(img)], img) {
		t.Fatal("flashed bank differs from the image")
	}
}

func TestCorruptChunkIsResent(t *testing.T) {
	old, img := xiaomiImage(t), ourImage(t)
	s := newSim(old)
	s.corruptAt = 640
	if err := FlashTelink(s, img, 3, nil); err != nil {
		t.Fatal(err)
	}
	if !s.corrupted || s.flash[0x20008] != 0x4B {
		t.Fatal("corrupted chunk not exercised or flags wrong")
	}
}

func TestTooBigIsRefused(t *testing.T) {
	old := xiaomiImage(t)
	big := SealImage(append(append([]byte(nil), old[:0x20]...), make([]byte, 0x20000)...))
	s := newSim(old)
	if err := FlashTelink(s, big, 3, nil); err == nil {
		t.Fatal("an image bigger than the bank was accepted")
	}
	if s.flash[8] != 0x4B {
		t.Fatal("old firmware flag touched")
	}
}

func TestUnsealedIsRefused(t *testing.T) {
	old := xiaomiImage(t)
	raw, _ := os.ReadFile("testdata/ble_controller.bin")
	if raw == nil {
		t.Skip()
	}
	s := newSim(old)
	if err := FlashTelink(s, raw, 3, nil); err == nil {
		t.Fatal("unsealed image accepted")
	}
	if s.dataPkts != 0 {
		t.Fatal("data sent for an unsealed image")
	}
}

func TestCutHalfwayKeepsOldFirmware(t *testing.T) {
	old, img := xiaomiImage(t), ourImage(t)
	s := newSim(old)
	s.failAfter = 500
	if err := FlashTelink(s, img, 3, nil); err == nil {
		t.Fatal("cut update reported success")
	}
	if s.flash[8] != 0x4B || s.flash[0x20008] == 0x4B {
		t.Fatalf("after a cut the old firmware must stay active: old=%#x new=%#x", s.flash[8], s.flash[0x20008])
	}
}

// sdkCrc32 is DFU_Crc32Calc of hci_dfu.c, transcribed on its own so the double does not share the
// tool's code (a double that shares it would agree with any mistake).
func sdkCrc32(crc uint32, data []byte) uint32 {
	for _, d := range data {
		crc ^= uint32(d)
		for k := 0; k < 8; k++ {
			if crc&1 == 1 {
				crc = (crc >> 1) ^ 0xEDB88320
			} else {
				crc = crc >> 1
			}
		}
	}
	return crc
}
