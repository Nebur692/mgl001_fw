// Telink HCI DFU (the vendor commands Xiaomi's miio_bt and Telink's ble_controller use) over our H5 link.
// DFU de Telink por HCI (los comandos de fabricante que usan miio_bt y el ble_controller de Telink).
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

const (
	opReset         = 0x0C03
	opReadLocalVer  = 0x1001
	opStartDFU      = 0xFF00
	opEndDFU        = 0xFF01
	opFwData        = 0xFF02
	dfuModeAlways   = 0x01
	dfuEndComplete  = 0x00
	dfuEndTerminate = 0x01
	dfuPayload      = 64
)

// telinkCRC is Telink's CRC32: reflected 0x04C11DB7 (0xEDB88320), init given, no final XOR.
func telinkCRC(crc uint32, data []byte) uint32 {
	for _, b := range data {
		crc ^= uint32(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xEDB88320
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// SealImage does what Telink's tl_check_fw does: size (with the CRC) at 0x18, CRC32 appended.
func SealImage(img []byte) []byte {
	out := append([]byte(nil), img...)
	binary.LittleEndian.PutUint32(out[0x18:], uint32(len(out)+4))
	c := telinkCRC(0xFFFFFFFF, out)
	return binary.LittleEndian.AppendUint32(out, c)
}

// CheckImage verifies a sealed image: Telink marker, size field and CRC residue 0.
func CheckImage(img []byte) error {
	if len(img) < 0x20 || string(img[8:12]) != "KNLT" {
		return errors.New("not a Telink image (no KNLT at 8)")
	}
	if int(binary.LittleEndian.Uint32(img[0x18:])) != len(img) {
		return fmt.Errorf("size field %d != file size %d", binary.LittleEndian.Uint32(img[0x18:]), len(img))
	}
	if telinkCRC(0xFFFFFFFF, img) != 0 {
		return errors.New("image CRC does not check")
	}
	return nil
}

// HCI is what DFU needs from the controller: send a command and get its Command Complete/Status.
type HCI interface {
	Command(op uint16, params []byte, timeout time.Duration) (status byte, ret []byte, err error)
}

type DFUProgress func(done, total int)

// FlashTelink writes img (sealed) with mode "always" and checks every step.
func FlashTelink(h HCI, img []byte, version uint16, progress DFUProgress) error {
	if err := CheckImage(img); err != nil {
		return err
	}
	p := make([]byte, 7)
	p[0] = dfuModeAlways
	binary.LittleEndian.PutUint16(p[1:], version)
	binary.LittleEndian.PutUint32(p[3:], uint32(len(img)))
	st, ret, err := h.Command(opStartDFU, p, 5*time.Second)
	if err != nil {
		return fmt.Errorf("start DFU: %v", err)
	}
	if st != 0 {
		return fmt.Errorf("start DFU refused: status %#02x", st)
	}
	max := dfuPayload
	if len(ret) >= 1 && ret[0] > 0 && int(ret[0]) < max {
		max = int(ret[0])
	}
	for off := 0; off < len(img); {
		n := min(max, len(img)-off)
		chunk := append([]byte(nil), img[off:off+n]...)
		chunk = binary.LittleEndian.AppendUint32(chunk, telinkCRC(0xFFFFFFFF, chunk))
		var st byte
		for try := 0; try < 3; try++ {
			st, _, err = h.Command(opFwData, chunk, 3*time.Second)
			if err == nil && st == 0 {
				break
			}
		}
		if err != nil || st != 0 {
			_, _, _ = h.Command(opEndDFU, []byte{dfuEndTerminate}, 3*time.Second)
			return fmt.Errorf("data at offset %d: status %#02x err %v (update terminated, old firmware kept)", off, st, err)
		}
		off += n
		if progress != nil {
			progress(off, len(img))
		}
	}
	st, _, err = h.Command(opEndDFU, []byte{dfuEndComplete}, 10*time.Second)
	if err != nil || st != 0 {
		return fmt.Errorf("end DFU: status %#02x err %v (old firmware kept)", st, err)
	}
	return nil
}
