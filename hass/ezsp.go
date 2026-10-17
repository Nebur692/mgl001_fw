// What the Zigbee chip says about its firmware, read from the traffic ZHA already has with it: when ZHA
// connects it asks for the EZSP version, the stack version (getValue VERSION_INFO) and, on firmwares that
// have one, the build string (XNCP). The bridge only reads a copy of those bytes after passing them on:
// it never sends anything of its own to the chip, and nothing here can hold up or change the traffic.
// The answer goes to a small file in RAM that mgl001-hass shows in Home Assistant.
//
// Lo que el chip Zigbee dice de su firmware, leído del tráfico que ZHA ya tiene con él: al conectar, ZHA
// pregunta la versión de EZSP, la de la pila (getValue VERSION_INFO) y, si el firmware la tiene, la cadena
// de compilación (XNCP). El puente solo lee una copia de esos bytes después de pasarlos: nunca manda nada
// propio al chip y nada de aquí puede frenar ni cambiar el tráfico. El resultado va a un fichero en RAM.
package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"sync"
)

// ASH (UG101): frames end with 0x7E; 0x7D escapes the next byte (xor 0x20); 0x1A cancels the frame so far,
// 0x18 marks it bad, 0x11/0x13 are flow control. A DATA frame's payload is xored with a fixed sequence.
const (
	ashFlag   = 0x7E
	ashEscape = 0x7D
	ashCancel = 0x1A
	ashSub    = 0x18
	ashXON    = 0x11
	ashXOFF   = 0x13
	ashMax    = 256 // longer than any ASH frame / más largo que cualquier trama ASH
)

var ashRandom = func() (s [ashMax]byte) {
	r := byte(0x42)
	for i := range s {
		s[i] = r
		if r&1 == 0 {
			r >>= 1
		} else {
			r = (r >> 1) ^ 0xB8
		}
	}
	return
}()

func ashCRC(b []byte) uint16 { // CRC-CCITT, init 0xFFFF
	crc := uint16(0xFFFF)
	for _, c := range b {
		crc ^= uint16(c) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// ashReader turns one direction of the byte stream into the EZSP frames inside its DATA frames.
type ashReader struct {
	buf     []byte
	escaped bool
	bad     bool
	frame   func(ezsp []byte)
}

func (a *ashReader) feed(p []byte) {
	for _, c := range p {
		switch {
		case c == ashFlag:
			if !a.bad && len(a.buf) >= 3 && a.buf[0]&0x80 == 0 && // DATA frame
				ashCRC(a.buf[:len(a.buf)-2]) == binary.BigEndian.Uint16(a.buf[len(a.buf)-2:]) {
				d := a.buf[1 : len(a.buf)-2]
				for i := range d {
					d[i] ^= ashRandom[i]
				}
				a.frame(d)
			}
			a.buf, a.escaped, a.bad = a.buf[:0], false, false
		case c == ashCancel:
			a.buf, a.escaped, a.bad = a.buf[:0], false, false
		case c == ashSub:
			a.bad = true
		case c == ashXON || c == ashXOFF:
		case c == ashEscape:
			a.escaped = true
		default:
			if a.escaped {
				c ^= 0x20
				a.escaped = false
			}
			if len(a.buf) >= ashMax {
				a.bad = true
				continue
			}
			a.buf = append(a.buf, c)
		}
	}
}

// ezspFrame splits an EZSP frame: sequence, response or command, frame id and parameters. The first
// "version" exchange always uses the old 3-byte header (seq, frame control, id). After it, the header
// depends on the protocol the chip answered: EZSP 5-7 extend it as (seq, fc, 0xFF, 0x00, id), EZSP 8+
// use a 2-byte frame control whose format version is 1 and a 2-byte id. proto is 0 while not known.
func ezspFrame(b []byte, proto int) (seq byte, response bool, id uint16, params []byte, ok bool) {
	if len(b) < 3 {
		return
	}
	seq, response = b[0], b[1]&0x80 != 0
	switch {
	case len(b) >= 5 && (proto >= 8 && b[2]&0x03 == 0x01 || proto == 0 && b[2] == 0x01):
		return seq, response, binary.LittleEndian.Uint16(b[3:5]), b[5:], true
	case len(b) >= 5 && proto < 8 && b[2] == 0xFF:
		return seq, response, uint16(b[4]), b[5:], true
	}
	return seq, response, uint16(b[2]), b[3:], true
}

const (
	ezspVersion     = 0x0000
	ezspGetValue    = 0x00AA
	ezspCustomFrame = 0x0047
	ezspValueInfo   = 0x11   // EZSP_VALUE_VERSION_INFO
	xncpBuildString = 0x8003 // GET_BUILD_STRING_RSP
)

// ZigbeeFirmware is what is known so far; empty fields were not seen yet.
type ZigbeeFirmware struct {
	Protocol int    // EZSP protocol version
	Stack    string // e.g. "7.4.4.0 build 0"
	Build    string // XNCP build string, if the firmware has one
}

// Text is how Home Assistant shows the firmware: "EmberZNet 7.4.4.0 build 0 (build string)".
func (f ZigbeeFirmware) Text() string {
	if f.Stack == "" {
		return ""
	}
	s := "EmberZNet " + f.Stack
	if f.Build != "" {
		s += " (" + f.Build + ")"
	}
	return s
}

func (f ZigbeeFirmware) ProtocolText() string {
	if f.Protocol == 0 {
		return ""
	}
	return fmt.Sprintf("EZSP %d", f.Protocol)
}

// ezspWatcher reads both directions of one connection.
type ezspWatcher struct {
	mu       sync.Mutex
	fw       ZigbeeFirmware
	infoSeqs map[byte]bool // getValue VERSION_INFO asked with these sequence numbers
	changed  func(ZigbeeFirmware)

	toChip, fromChip ashReader
}

func newEZSPWatcher(changed func(ZigbeeFirmware)) *ezspWatcher {
	w := &ezspWatcher{infoSeqs: map[byte]bool{}, changed: changed}
	w.toChip.frame = w.command
	w.fromChip.frame = w.response
	return w
}

// FromHost and FromChip get a copy of what was already passed on. A bad frame is skipped; a panic here
// would only lose the version, never the traffic.
func (w *ezspWatcher) FromHost(p []byte) { w.safely(func() { w.toChip.feed(p) }) }
func (w *ezspWatcher) FromChip(p []byte) { w.safely(func() { w.fromChip.feed(p) }) }

func (w *ezspWatcher) safely(f func()) {
	defer func() { _ = recover() }()
	w.mu.Lock()
	defer w.mu.Unlock()
	f()
}

func (w *ezspWatcher) command(b []byte) {
	seq, resp, id, params, ok := ezspFrame(b, w.fw.Protocol)
	if ok && !resp && id == ezspGetValue && len(params) >= 1 && params[0] == ezspValueInfo {
		if len(w.infoSeqs) > 16 {
			w.infoSeqs = map[byte]bool{}
		}
		w.infoSeqs[seq] = true
	}
}

func (w *ezspWatcher) response(b []byte) {
	seq, resp, id, p, ok := ezspFrame(b, w.fw.Protocol)
	if !ok || !resp {
		return
	}
	fw := w.fw
	switch id {
	case ezspVersion: // protocolVersion, stackType, stackVersion (u16, one nibble per digit)
		if len(p) >= 4 {
			fw.Protocol = int(p[0])
			if fw.Stack == "" {
				v := binary.LittleEndian.Uint16(p[2:4])
				fw.Stack = fmt.Sprintf("%d.%d.%d.%d", v>>12, v>>8&0xF, v>>4&0xF, v&0xF)
			}
		}
	case ezspGetValue: // status, length, build (u16), major, minor, patch, special, type
		if w.infoSeqs[seq] {
			delete(w.infoSeqs, seq)
			if len(p) >= 8 && p[0] == 0 && p[1] >= 6 {
				fw.Stack = fmt.Sprintf("%d.%d.%d.%d build %d", p[4], p[5], p[6], p[7], binary.LittleEndian.Uint16(p[2:4]))
			}
		}
	case ezspCustomFrame: // status, reply length, reply = XNCP command id (u16), status, payload
		if len(p) >= 5 && p[0] == 0 && int(p[1]) <= len(p)-2 {
			r := p[2 : 2+int(p[1])]
			if len(r) >= 3 && binary.LittleEndian.Uint16(r[0:2]) == xncpBuildString && r[2] == 0 {
				fw.Build = printable(string(r[3:]))
			}
		}
	}
	if fw != w.fw {
		w.fw = fw
		w.changed(fw)
	}
}

// printable keeps what Home Assistant can show: printable text, at most 100 characters.
func printable(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F || r == 0xFFFD {
			return -1
		}
		return r
	}, s)
	if r := []rune(strings.TrimSpace(s)); len(r) > 100 {
		return string(r[:100])
	}
	return strings.TrimSpace(s)
}

// The two processes share the answer through a file in RAM: "firmware\nprotocol\n".
// Los dos procesos comparten la respuesta por un fichero en RAM.
func saveZigbeeFirmware(path string, fw ZigbeeFirmware) error {
	return writeFileAtomic(path, []byte(fw.Text()+"\n"+fw.ProtocolText()+"\n"), 0644)
}

func loadZigbeeFirmware(path string) (firmware, protocol string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	l := strings.SplitN(string(b), "\n", 3)
	if len(l) < 2 {
		return "", ""
	}
	return l[0], l[1]
}
