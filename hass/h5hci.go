// HCI commands straight over the H5 link (for DFU and diagnosis, without the BLE library).
// Comandos HCI directamente sobre el enlace H5 (para el DFU y el diagnóstico, sin la librería BLE).
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"os"
	"time"
)

type h5HCI struct{ h *H5 }

func (x h5HCI) Command(op uint16, params []byte, timeout time.Duration) (byte, []byte, error) {
	pkt := []byte{byte(op), byte(op >> 8), byte(len(params))}
	pkt = append(pkt, params...)
	if err := x.h.SendReliable(h5Cmd, pkt); err != nil {
		return 0, nil, err
	}
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-x.h.rx:
			if !ok {
				return 0, nil, errors.New("H5 link closed")
			}
			if len(ev) < 3 || ev[0] != h5Event {
				continue
			}
			e := ev[1:]
			switch {
			case e[0] == 0x0E && len(e) >= 6 && binary.LittleEndian.Uint16(e[3:]) == op: // Command Complete
				return e[5], e[6:], nil
			case e[0] == 0x0F && len(e) >= 6 && binary.LittleEndian.Uint16(e[4:]) == op: // Command Status
				return e[2], nil, nil
			}
		case <-deadline:
			return 0, nil, fmt.Errorf("no answer to %#04x", op)
		}
	}
}

func openHCI() (h5HCI, error) {
	resetController()
	h, err := NewH5(*flagUART)
	if err != nil {
		return h5HCI{}, err
	}
	x := h5HCI{h}
	if st, _, err := x.Command(opReset, nil, 3*time.Second); err != nil || st != 0 {
		return x, fmt.Errorf("HCI reset: status %#02x err %v", st, err)
	}
	return x, nil
}

func readVersion(x h5HCI) (hciVer byte, hciRev uint16, manuf uint16, err error) {
	st, r, err := x.Command(opReadLocalVer, nil, 3*time.Second)
	if err != nil || st != 0 || len(r) < 8 {
		return 0, 0, 0, fmt.Errorf("read version: status %#02x err %v", st, err)
	}
	return r[0], binary.LittleEndian.Uint16(r[1:]), binary.LittleEndian.Uint16(r[4:]), nil
}

// btInfo: read-only. btDFU: flashes a sealed image, then checks the new version answers.
func btInfo() error {
	x, err := openHCI()
	if err != nil {
		return err
	}
	v, rev, m, err := readVersion(x)
	if err != nil {
		return err
	}
	log.Printf("controller: HCI version %d, revision %#04x, manufacturer %#04x", v, rev, m)
	return nil
}

func btDFU(path string, version uint16) error {
	img, err := readFile(path)
	if err != nil {
		return err
	}
	if err := CheckImage(img); err != nil {
		return err
	}
	x, err := openHCI()
	if err != nil {
		return err
	}
	_, rev, _, err := readVersion(x)
	if err != nil {
		return err
	}
	log.Printf("btdfu: current revision %#04x, writing %d bytes (version %#04x)", rev, len(img), version)
	last := -1
	err = FlashTelink(x, img, version, func(done, total int) {
		if pct := done * 100 / total; pct/10 != last/10 {
			last = pct
			log.Printf("btdfu: %d %%", pct)
		}
	})
	if err != nil {
		return err
	}
	log.Printf("btdfu: written and accepted; the controller restarts")
	time.Sleep(3 * time.Second)
	x.h.f.Close()
	x2, err := openHCI()
	if err != nil {
		return fmt.Errorf("after the update the controller does not answer: %v", err)
	}
	_, rev2, _, err := readVersion(x2)
	if err != nil {
		return err
	}
	log.Printf("btdfu: controller answers, revision now %#04x", rev2)
	return nil
}

func readFile(p string) ([]byte, error) { return os.ReadFile(p) }
