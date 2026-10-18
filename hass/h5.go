// User-space Bluetooth "three-wire UART" (H5) link to the Telink controller on /dev/ttyS2, bridged
// to an H4 TCP socket on 127.0.0.1 for the BLE library. Xiaomi's own stack also runs H5 in user
// space; the kernel 4.9 H5 driver loses ACL packets with this controller ("Out-of-order packet").
//
// Enlace Bluetooth H5 en espacio de usuario con el controlador Telink de /dev/ttyS2, con un puente
// a un socket H4 en 127.0.0.1 para la librería BLE. La pila de Xiaomi también hace H5 en espacio de
// usuario; el driver H5 del kernel 4.9 pierde paquetes ACL con este controlador.
package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

const (
	h5Ack    = 0
	h5Cmd    = 1
	h5ACL    = 2
	h5Event  = 4
	h5Vendor = 14
	h5Link   = 15

	h5Retransmit = 250 * time.Millisecond
)

var (
	h5Sync       = []byte{0x01, 0x7E}
	h5SyncResp   = []byte{0x02, 0x7D}
	h5ConfigResp = []byte{0x04, 0x7B}
	// CONFIG: sliding window 1, no out-of-frame flow control, no CRC, version 0
	h5Config = []byte{0x03, 0xFC, 0x01}
)

type H5 struct {
	f      *os.File
	wmu    sync.Mutex
	txSeq  uint8 // next sequence number we send
	txAck  uint8 // next sequence number we expect (sent as ack)
	acked  chan uint8
	rx     chan []byte // H4 packets (type byte + payload) for the host
	active chan struct{}
	once   sync.Once
	errs   chan error
	sync   atomic.Bool
	ln     net.Listener // the local H4 bridge (ServeH4)
}

// Close ends the link: the UART and the local H4 bridge. Opening again resets the controller and re-syncs.
// Close cierra el enlace: el UART y el puente H4 local. Al volver a abrir se reinicia el chip y se sincroniza.
func (h *H5) Close() {
	if h.ln != nil {
		_ = h.ln.Close()
	}
	_ = h.f.Close()
}

func openUART(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	t, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	if err != nil {
		f.Close()
		return nil, err
	}
	// raw 8N1 at 1 000 000 baud, no hardware flow control (as Xiaomi's miio_bt sets it)
	t.Iflag = 0
	t.Oflag = 0
	t.Lflag = 0
	t.Cflag = unix.CS8 | unix.CREAD | unix.CLOCAL | unix.B1000000
	t.Ispeed = unix.B1000000
	t.Ospeed = unix.B1000000
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(int(f.Fd()), unix.TCSETS, t); err != nil {
		f.Close()
		return nil, err
	}
	_ = unix.IoctlSetInt(int(f.Fd()), unix.TCFLSH, unix.TCIOFLUSH)
	return f, nil
}

// resetController pulses the Telink reset line (GPIO 60, low = reset), as Xiaomi's radio_silence.sh does.
func resetController() { pulseReset(500 * time.Millisecond) }

// pulseReset holds the reset line low for 200 ms, then waits settle. -bt-reset-gpio points elsewhere on the bench.
func pulseReset(settle time.Duration) {
	gpio := *flagBtReset
	if _, err := os.Stat(gpio); err != nil && gpio == "/sys/class/gpio/gpio60" {
		_ = os.WriteFile("/sys/class/gpio/export", []byte("60"), 0200)
		_ = os.WriteFile(gpio+"/direction", []byte("out"), 0644)
	}
	_ = os.WriteFile(gpio+"/value", []byte("0"), 0644)
	time.Sleep(200 * time.Millisecond)
	_ = os.WriteFile(gpio+"/value", []byte("1"), 0644)
	time.Sleep(settle)
}

func NewH5(path string) (*H5, error) {
	f, err := openUART(path)
	if err != nil {
		return nil, err
	}
	h := &H5{f: f, acked: make(chan uint8, 16), rx: make(chan []byte, 256), active: make(chan struct{}), errs: make(chan error, 1)}
	go h.readLoop()
	// Link establishment: SYNC until SYNC RESPONSE, then CONFIG until CONFIG RESPONSE.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-h.active:
			log.Printf("h5: link active")
			return h, nil
		case err := <-h.errs:
			f.Close()
			return nil, err
		case <-time.After(250 * time.Millisecond):
			if h.synced() {
				_ = h.writePacket(h5Link, false, h5Config)
			} else {
				_ = h.writePacket(h5Link, false, h5Sync)
			}
		}
	}
	f.Close()
	return nil, errors.New("h5: no link with the Bluetooth controller")
}

func (h *H5) synced() bool { return h.sync.Load() }

// SLIP encoding of one H5 packet
func slip(pkt []byte) []byte {
	out := []byte{0xC0}
	for _, b := range pkt {
		switch b {
		case 0xC0:
			out = append(out, 0xDB, 0xDC)
		case 0xDB:
			out = append(out, 0xDB, 0xDD)
		default:
			out = append(out, b)
		}
	}
	return append(out, 0xC0)
}

func (h *H5) writePacket(typ uint8, reliable bool, payload []byte) error {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	return h.writeLocked(typ, reliable, h.txSeq, payload)
}

func (h *H5) writeLocked(typ uint8, reliable bool, seq uint8, payload []byte) error {
	n := len(payload)
	b0 := (h.txAck & 7) << 3
	if reliable {
		b0 |= 0x80 | (seq & 7)
	}
	b1 := typ&0x0F | uint8(n&0x0F)<<4
	b2 := uint8(n >> 4)
	b3 := ^(b0 + b1 + b2)
	pkt := append([]byte{b0, b1, b2, b3}, payload...)
	_, err := h.f.Write(slip(pkt))
	return err
}

// SendReliable sends one reliable packet and waits for its acknowledgement (window 1).
func (h *H5) SendReliable(typ uint8, payload []byte) error {
	h.wmu.Lock()
	seq := h.txSeq
	h.wmu.Unlock()
	want := (seq + 1) & 7
	for try := 0; try < 20; try++ {
		h.wmu.Lock()
		err := h.writeLocked(typ, true, seq, payload)
		h.wmu.Unlock()
		if err != nil {
			return err
		}
		t := time.NewTimer(h5Retransmit)
	wait:
		for {
			select {
			case a := <-h.acked:
				if a == want {
					t.Stop()
					h.wmu.Lock()
					h.txSeq = want
					h.wmu.Unlock()
					return nil
				}
			case <-t.C:
				break wait
			}
		}
	}
	return fmt.Errorf("h5: packet not acknowledged")
}

func (h *H5) readLoop() {
	buf := make([]byte, 4096)
	var frame []byte
	in, esc := false, false
	for {
		n, err := h.f.Read(buf)
		if err != nil {
			select {
			case h.errs <- err:
			default:
			}
			close(h.rx)
			return
		}
		for _, b := range buf[:n] {
			// A frame never ends on a noisy line: drop it past the largest H5 packet (header + 4095 + CRC)
			// Una trama que no termina en una línea con ruido se tira al pasar el paquete H5 más grande
			if len(frame) > 4+4095+2 {
				frame, in, esc = frame[:0], false, false
			}
			switch {
			case b == 0xC0:
				if in && len(frame) > 0 {
					h.handle(frame)
				}
				frame, in, esc = frame[:0], true, false
			case !in:
			case esc:
				esc = false
				switch b {
				case 0xDC:
					frame = append(frame, 0xC0)
				case 0xDD:
					frame = append(frame, 0xDB)
				default:
					frame = append(frame, b)
				}
			case b == 0xDB:
				esc = true
			default:
				frame = append(frame, b)
			}
		}
	}
}

func (h *H5) handle(p []byte) {
	if len(p) < 4 || p[0]+p[1]+p[2]+p[3] != 0xFF {
		return // bad header checksum
	}
	reliable := p[0]&0x80 != 0
	seq := p[0] & 7
	ack := (p[0] >> 3) & 7
	typ := p[1] & 0x0F
	n := int(p[1]>>4) | int(p[2])<<4
	if len(p) < 4+n {
		return
	}
	payload := p[4 : 4+n]
	select {
	case h.acked <- ack:
	default:
	}
	if typ == h5Link {
		h.link(payload)
		return
	}
	if reliable {
		h.wmu.Lock()
		if seq != h.txAck {
			// out of order or repeated: re-acknowledge what we expect and drop it
			_ = h.writeLocked(h5Ack, false, 0, nil)
			h.wmu.Unlock()
			return
		}
		h.txAck = (h.txAck + 1) & 7
		_ = h.writeLocked(h5Ack, false, 0, nil)
		h.wmu.Unlock()
	}
	switch typ {
	case h5Event, h5ACL:
		if typ == h5Event {
			logLinkEvent(payload)
		}
		h.rx <- append([]byte{typ}, payload...)
	}
}

func (h *H5) link(p []byte) {
	switch {
	case len(p) >= 2 && p[0] == 0x01 && p[1] == 0x7E: // SYNC from the controller (also: it has just reset)
		// A controller that reset starts its sequence numbers again at 0: so do we
		// Un controlador que se ha reiniciado vuelve a numerar desde 0: nosotros también
		h.wmu.Lock()
		h.txSeq, h.txAck = 0, 0
		h.wmu.Unlock()
		_ = h.writePacket(h5Link, false, h5SyncResp)
	case len(p) >= 2 && p[0] == 0x02 && p[1] == 0x7D: // SYNC RESPONSE
		h.sync.Store(true)
	case len(p) >= 2 && p[0] == 0x03 && p[1] == 0xFC: // CONFIG from the controller
		_ = h.writePacket(h5Link, false, append(append([]byte{}, h5ConfigResp...), 0x01))
	case len(p) >= 2 && p[0] == 0x04 && p[1] == 0x7B: // CONFIG RESPONSE: link is active
		h.wmu.Lock()
		h.txSeq, h.txAck = 0, 0
		h.wmu.Unlock()
		h.once.Do(func() { close(h.active) })
	}
}

// ServeH4 bridges one H4 TCP client (the BLE library) to the H5 link.
func (h *H5) ServeH4(addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	h.ln = ln
	go func() {
		var cur net.Conn
		var mu sync.Mutex
		// controller -> host
		go func() {
			for pkt := range h.rx {
				mu.Lock()
				c := cur
				mu.Unlock()
				if c != nil {
					_, _ = c.Write(pkt)
				}
			}
		}()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if cur != nil {
				cur.Close()
			}
			cur = c
			mu.Unlock()
			go h.fromHost(c)
		}
	}()
	return ln.Addr().String(), nil
}

// host -> controller: split the H4 stream into packets and send each one reliably
func (h *H5) fromHost(c net.Conn) {
	defer c.Close()
	hdr := make([]byte, 1)
	for {
		if _, err := io.ReadFull(c, hdr); err != nil {
			return
		}
		var body []byte
		switch hdr[0] {
		case h5Cmd: // opcode(2) + len(1)
			h3 := make([]byte, 3)
			if _, err := io.ReadFull(c, h3); err != nil {
				return
			}
			body = make([]byte, 3+int(h3[2]))
			copy(body, h3)
			if _, err := io.ReadFull(c, body[3:]); err != nil {
				return
			}
		case h5ACL: // handle(2) + len(2, LE)
			h4 := make([]byte, 4)
			if _, err := io.ReadFull(c, h4); err != nil {
				return
			}
			body = make([]byte, 4+(int(h4[2])|int(h4[3])<<8))
			copy(body, h4)
			if _, err := io.ReadFull(c, body[4:]); err != nil {
				return
			}
		default:
			log.Printf("h5: unknown H4 packet type %d from host", hdr[0])
			return
		}
		if err := h.SendReliable(hdr[0], body); err != nil {
			log.Printf("h5: %v", err)
		}
	}
}

// logLinkEvent logs connection setup and teardown, with the reason code, for diagnosis.
// Registra el alta y la baja de las conexiones, con su motivo, para diagnosticar.
func logLinkEvent(p []byte) {
	if len(p) < 2 {
		return
	}
	switch {
	case p[0] == 0x05 && len(p) >= 6: // Disconnection Complete
		log.Printf("h5: disconnection handle %#04x reason %#02x", uint16(p[3])|uint16(p[4])<<8, p[5])
	case p[0] == 0x3E && len(p) >= 21 && p[2] == 0x01: // LE Connection Complete
		log.Printf("h5: connection status %#02x handle %#04x interval %d x1.25ms latency %d timeout %d x10ms",
			p[3], uint16(p[4])|uint16(p[5])<<8, uint16(p[14])|uint16(p[15])<<8, uint16(p[16])|uint16(p[17])<<8, uint16(p[18])|uint16(p[19])<<8)
	case p[0] == 0x3E && len(p) >= 12 && p[2] == 0x03: // LE Connection Update Complete
		log.Printf("h5: connection update status %#02x interval %d latency %d timeout %d",
			p[3], uint16(p[6])|uint16(p[7])<<8, uint16(p[8])|uint16(p[9])<<8, uint16(p[10])|uint16(p[11])<<8)
	}
}
