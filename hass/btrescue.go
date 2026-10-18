// Rescue window of our Bluetooth controller image (vendor/ble_controller/rescate.c in the bt-firmware patch):
// for 300 ms after reset, before its BLE stack, the chip listens for "RESCATE!" on the UART and then runs a
// tiny flash session that needs nothing of HCI. A controller image that boots but does not answer HCI can
// always be rewritten this way. Xiaomi's image and the one flashed on 17-10-2026 do not have it.
//
// Ventana de rescate de nuestra imagen del chip Bluetooth: 300 ms tras el reset, antes de la pila, el chip
// escucha «RESCATE!» y abre una sesión mínima de flash que no necesita HCI. La de Xiaomi y la del 17-10 no la
// tienen.
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

const (
	rscMagic     = "RESCATE!"
	rscStart     = 0xA5
	rscMaxData   = 256
	rscBankSize  = 0x20000
	rscBankMaxFw = 0x1F000 // as DFU_NEW_FW_MAX_SIZE: the top 4K of each bank stay out
	rscFlashSize = 0x80000

	rscInfo       = 'I'
	rscRead       = 'R'
	rscErase      = 'E'
	rscWrite      = 'W'
	rscInvalidate = 'X'
	rscReboot     = 'B'
)

var rscStatus = []string{"ok", "bad CRC", "address refused", "bad length", "unknown command", "read-back differs"}

func rscStatusText(s byte) string {
	if int(s) < len(rscStatus) {
		return rscStatus[s]
	}
	return fmt.Sprintf("status %d", s)
}

type rescueInfo struct {
	Proto        byte
	Running      uint32 // bank the chip booted from
	MID          uint32 // flash JEDEC id
	Mark0, Mark1 byte   // boot mark (+8) of bank 0 and bank 1: 0x4B bootable
	Revision     uint16
}

// other is the bank the session may write: never the one the code runs from
func (i rescueInfo) other() uint32 {
	if i.Running == 0 {
		return rscBankSize
	}
	return 0
}

func (i rescueInfo) String() string {
	return fmt.Sprintf("running bank %#05x, flash id %#06x, boot marks bank 0 0x%02x / bank 1 0x%02x, revision %#04x, protocol %d",
		i.Running, i.MID, i.Mark0, i.Mark1, i.Revision, i.Proto)
}

func parseRescueInfo(d []byte) (rescueInfo, error) {
	if len(d) < 13 {
		return rescueInfo{}, fmt.Errorf("rescue: short info (%d bytes)", len(d))
	}
	return rescueInfo{Proto: d[0], Running: binary.LittleEndian.Uint32(d[1:]), MID: binary.LittleEndian.Uint32(d[5:]),
		Mark0: d[9], Mark1: d[10], Revision: binary.LittleEndian.Uint16(d[11:])}, nil
}

// rscFrame: 0xA5, cmd, len (2, LE), payload, CRC-32 IEEE (LE) of everything before it
func rscFrame(cmd byte, payload []byte) []byte {
	f := []byte{rscStart, cmd, byte(len(payload)), byte(len(payload) >> 8)}
	f = append(f, payload...)
	return binary.LittleEndian.AppendUint32(f, crc32.ChecksumIEEE(f))
}

type rescueLink struct {
	w  io.Writer
	in chan byte
}

func (r *rescueLink) readN(n int, d time.Duration) ([]byte, bool) {
	out := make([]byte, 0, n)
	for len(out) < n {
		select {
		case b, ok := <-r.in:
			if !ok {
				return nil, false
			}
			out = append(out, b)
		case <-time.After(d):
			return nil, false
		}
	}
	return out, true
}

// answer waits for the next well-formed answer to cmd; anything else on the line (the flood's tail, H5) is skipped
func (r *rescueLink) answer(cmd byte, timeout time.Duration) (byte, []byte, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b, ok := r.readN(1, time.Until(deadline))
		if !ok {
			break
		}
		if b[0] != rscStart {
			continue
		}
		h, ok := r.readN(3, 50*time.Millisecond)
		if !ok || h[0] != cmd|0x80 {
			continue
		}
		n := int(binary.LittleEndian.Uint16(h[1:]))
		if n < 1 || n > 1+rscMaxData {
			continue
		}
		rest, ok := r.readN(n+4, 200*time.Millisecond)
		if !ok {
			continue
		}
		f := append(append([]byte{rscStart}, h...), rest[:n]...)
		if crc32.ChecksumIEEE(f) != binary.LittleEndian.Uint32(rest[n:]) {
			continue
		}
		return rest[0], rest[1:n], nil
	}
	return 0, nil, fmt.Errorf("rescue: no answer to %q", cmd)
}

// Command sends one frame and returns its status and data; a lost or garbled frame is sent again (3 tries)
func (r *rescueLink) Command(cmd byte, payload []byte) (byte, []byte, error) {
	var err error
	for try := 0; try < 3; try++ {
		if _, err = r.w.Write(rscFrame(cmd, payload)); err != nil {
			return 0, nil, err
		}
		var st byte
		var d []byte
		st, d, err = r.answer(cmd, 2*time.Second)
		if err == nil && st == 1 { // bad CRC on the way in: send again
			err = errors.New("rescue: the chip got a garbled frame")
			continue
		}
		if err == nil {
			return st, d, nil
		}
	}
	return 0, nil, err
}

func (r *rescueLink) must(cmd byte, payload []byte) ([]byte, error) {
	st, d, err := r.Command(cmd, payload)
	if err != nil {
		return nil, err
	}
	if st != 0 {
		return nil, fmt.Errorf("rescue: %q refused: %s", cmd, rscStatusText(st))
	}
	return d, nil
}

func rscAddr(a uint32, extra ...byte) []byte {
	return append(binary.LittleEndian.AppendUint32(nil, a), extra...)
}

func (r *rescueLink) ReadFlash(addr uint32, n int) ([]byte, error) {
	var out []byte
	for len(out) < n {
		k := min(rscMaxData, n-len(out))
		d, err := r.must(rscRead, rscAddr(addr+uint32(len(out)), byte(k), byte(k>>8)))
		if err != nil {
			return nil, err
		}
		if len(d) != k {
			return nil, fmt.Errorf("rescue: read %d bytes at %#x, got %d", k, addr+uint32(len(out)), len(d))
		}
		out = append(out, d...)
	}
	return out, nil
}

func (r *rescueLink) WriteFlash(addr uint32, data []byte) error {
	for o := 0; o < len(data); o += rscMaxData {
		k := min(rscMaxData, len(data)-o)
		if _, err := r.must(rscWrite, rscAddr(addr+uint32(o), data[o:o+k]...)); err != nil {
			return err
		}
	}
	return nil
}

// WriteImage puts a sealed image in the bank that is not running and makes it the one that boots. Order, so a
// power cut at any point leaves a bootable chip: erase, everything but the first 16 bytes (they hold the boot
// mark), read back, the first 16 bytes, then clear the running bank's mark (only clears bits: no erase).
// Pone la imagen en el banco que no corre y la deja como la que arranca; un corte en cualquier punto deja un
// chip que arranca.
func (r *rescueLink) WriteImage(info rescueInfo, img []byte, progress func(string)) error {
	if err := CheckImage(img); err != nil {
		return err
	}
	if len(img) > rscBankMaxFw {
		return fmt.Errorf("image of %d bytes does not fit a bank (%d)", len(img), rscBankMaxFw)
	}
	b := info.other()
	progress(fmt.Sprintf("erasing bank %#05x", b))
	for a := b; a < b+rscBankMaxFw; a += 0x1000 {
		if _, err := r.must(rscErase, rscAddr(a)); err != nil {
			return err
		}
	}
	progress(fmt.Sprintf("writing %d bytes", len(img)))
	if err := r.WriteFlash(b+16, img[16:]); err != nil {
		return err
	}
	back, err := r.ReadFlash(b+16, len(img)-16)
	if err != nil {
		return err
	}
	if !bytes.Equal(back, img[16:]) {
		return errors.New("rescue: the image read back differs; the boot mark was not written")
	}
	if err := r.WriteFlash(b, img[:16]); err != nil {
		return err
	}
	progress(fmt.Sprintf("boot mark moved to bank %#05x", b))
	_, err = r.must(rscInvalidate, nil)
	return err
}

func (r *rescueLink) Reboot() error {
	_, err := r.must(rscReboot, nil)
	return err
}

// openRescue resets the controller and floods the magic until the chip answers with its info
func openRescue() (*rescueLink, rescueInfo, func(), error) {
	f, err := openUART(*flagUART)
	if err != nil {
		return nil, rescueInfo{}, nil, err
	}
	r := &rescueLink{w: f, in: make(chan byte, 1<<16)}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			if err != nil {
				close(r.in)
				return
			}
			for _, b := range buf[:n] {
				r.in <- b
			}
		}
	}()
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_, _ = f.Write([]byte(rscMagic))
			}
		}
	}()
	pulseReset(0)
	st, d, err := r.answer(rscInfo, 1500*time.Millisecond)
	close(stop)
	if err != nil {
		f.Close()
		return nil, rescueInfo{}, nil, errors.New("rescue: the controller did not open its rescue window (its image has none, like Xiaomi's or the 17-10-2026 one, or the reset line did not move)")
	}
	info, err := parseRescueInfo(d)
	if err == nil && st != 0 {
		err = fmt.Errorf("rescue: info %s", rscStatusText(st))
	}
	if err != nil {
		f.Close()
		return nil, rescueInfo{}, nil, err
	}
	time.Sleep(50 * time.Millisecond) // the chip drops the flood's tail after 20 ms of silence
	for len(r.in) > 0 {
		<-r.in
	}
	return r, info, func() { f.Close() }, nil
}

func btRescueInfo() error {
	r, info, done, err := openRescue()
	if err != nil {
		return err
	}
	defer done()
	log.Printf("rescue: %s", info)
	return r.Reboot()
}

func btRescueRead(path string) error {
	r, info, done, err := openRescue()
	if err != nil {
		return err
	}
	defer done()
	log.Printf("rescue: %s; reading %d KB", info, rscFlashSize/1024)
	d, err := r.ReadFlash(0, rscFlashSize)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, d, 0600); err != nil {
		return err
	}
	log.Printf("rescue: flash saved to %s", path)
	return r.Reboot()
}

func btRescueWrite(path string) error {
	img, err := readFile(path)
	if err != nil {
		return err
	}
	if err := CheckImage(img); err != nil {
		return err
	}
	r, info, done, err := openRescue()
	if err != nil {
		return err
	}
	log.Printf("rescue: %s", info)
	err = r.WriteImage(info, img, func(s string) { log.Printf("rescue: %s", s) })
	if err == nil {
		err = r.Reboot()
	}
	done()
	if err != nil {
		return err
	}
	log.Printf("rescue: written, verified and boot mark moved; the controller restarts")
	time.Sleep(time.Second)
	x, err := openHCI()
	if err != nil {
		return fmt.Errorf("written, but the new image does not answer HCI: %v", err)
	}
	defer x.h.Close()
	_, rev, _, err := readVersion(x)
	if err != nil {
		return fmt.Errorf("written, but the new image does not answer HCI: %v", err)
	}
	log.Printf("rescue: controller answers HCI, revision %#04x", rev)
	return nil
}

// btRescueCmd sends one raw rescue command, "<letter>:<payload hex>" (diagnosis), e.g. R:0000000010 00
func btRescueCmd(spec string) error {
	c, h, _ := strings.Cut(spec, ":")
	p, err := hex.DecodeString(strings.ReplaceAll(h, " ", ""))
	if err != nil || len(c) != 1 {
		return fmt.Errorf("rescue command %q: want <letter>:<hex>", spec)
	}
	r, info, done, err := openRescue()
	if err != nil {
		return err
	}
	defer done()
	log.Printf("rescue: %s", info)
	st, d, err := r.Command(c[0], p)
	if err != nil {
		return err
	}
	log.Printf("rescue: %q -> %s, %d bytes %x", c[0], rscStatusText(st), len(d), d)
	if c[0] == rscReboot {
		return nil
	}
	return r.Reboot()
}

// btHCI sends one HCI command, "<opcode hex>:<params hex>", and prints the answer (diagnosis)
func btHCI(spec string) error {
	o, h, _ := strings.Cut(spec, ":")
	op, err1 := hex.DecodeString(o)
	p, err2 := hex.DecodeString(strings.ReplaceAll(h, " ", ""))
	if err1 != nil || err2 != nil || len(op) != 2 {
		return fmt.Errorf("HCI command %q: want <opcode, 4 hex>:<params hex>", spec)
	}
	x, err := openHCI()
	if err != nil {
		return err
	}
	defer x.h.Close()
	opcode := uint16(op[0])<<8 | uint16(op[1])
	st, r, err := x.Command(opcode, p, 3*time.Second)
	if err != nil {
		return err
	}
	log.Printf("hci: %#04x -> status %#02x, %d bytes %x", opcode, st, len(r), r)
	return nil
}
