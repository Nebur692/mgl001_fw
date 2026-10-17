package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

var (
	ashRST    = []byte{0x1A, 0xC0, 0x38, 0xBC, 0x7E}
	ashRSTACK = []byte{0x1A, 0xC1, 0x02, 0x0B, 0x0A, 0x52, 0x7E}
)

// pty gives a pseudo-terminal: the master plays the Zigbee chip, the slave path is the "UART".
func pty(t *testing.T) (*os.File, string) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skip("no /dev/ptmx:", err)
	}
	if err := unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave := fmt.Sprintf("/dev/pts/%d", n)
	// As after boot: canonical mode with echo (what hung the bridge on 17-10-2026)
	s, err := os.OpenFile(slave, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	tt, _ := unix.IoctlGetTermios(int(s.Fd()), unix.TCGETS)
	tt.Lflag |= unix.ICANON | unix.ECHO
	tt.Iflag |= unix.ICRNL | unix.IXON
	_ = unix.IoctlSetTermios(int(s.Fd()), unix.TCSETS, tt)
	s.Close()
	t.Cleanup(func() { m.Close() })
	return m, slave
}

// fakeNCP answers RSTACK to every ASH RST and echoes anything else (bytes, not frames).
func fakeNCP(m *os.File) {
	buf := make([]byte, 4096)
	var acc []byte
	for {
		n, err := m.Read(buf)
		if err != nil {
			if n == 0 {
				time.Sleep(20 * time.Millisecond) // EIO while no one has the slave open
				continue
			}
		}
		acc = append(acc, buf[:n]...)
		if i := bytes.Index(acc, ashRST); i >= 0 {
			_, _ = m.Write(ashRSTACK)
			acc = acc[i+len(ashRST):]
			continue
		}
		if !bytes.HasPrefix(ashRST, acc[:min(len(acc), len(ashRST))]) {
			_, _ = m.Write(acc)
			acc = acc[:0]
		}
	}
}

func startBridge(t *testing.T, slave string) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	(&ZigbeeBridge{ports: []int{port}, uart: slave, flow: false}).Run()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			time.Sleep(100 * time.Millisecond)
			return addr
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("bridge did not listen")
	return ""
}

func readN(c net.Conn, n int, d time.Duration) ([]byte, error) {
	_ = c.SetReadDeadline(time.Now().Add(d))
	b := make([]byte, n)
	_, err := io.ReadFull(c, b)
	return b, err
}

func TestZigbeeBridge(t *testing.T) {
	m, slave := pty(t)
	go fakeNCP(m)
	addr := startBridge(t, slave)

	// 1. ZHA's first words: ASH RST -> RSTACK, on a port left canonical with echo
	c1, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c1.Write(ashRST)
	if b, err := readN(c1, len(ashRSTACK), 3*time.Second); err != nil || !bytes.Equal(b, ashRSTACK) {
		t.Fatalf("RST through the bridge: got % x, %v", b, err)
	}

	// 2. A second client replaces the first, which is closed
	c2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = c1.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := c1.Read(make([]byte, 1)); err == nil {
		t.Fatal("the first client was not closed when the second came")
	}
	_, _ = c2.Write(ashRST)
	if b, err := readN(c2, len(ashRSTACK), 3*time.Second); err != nil || !bytes.Equal(b, ashRSTACK) {
		t.Fatalf("second client: got % x, %v", b, err)
	}

	// 3. After a disconnect, a new client works again
	c2.Close()
	time.Sleep(300 * time.Millisecond)
	c3, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c3.Close()
	_, _ = c3.Write(ashRST)
	if b, err := readN(c3, len(ashRSTACK), 3*time.Second); err != nil || !bytes.Equal(b, ashRSTACK) {
		t.Fatalf("after a reconnect: got % x, %v", b, err)
	}
	// The "Zigbee bridge" sensor and the update auto-keep look at the bridge's own port
	port := c3.RemoteAddr().(*net.TCPAddr).Port
	if !tcpEstablished(port) {
		t.Fatalf("a client on the bridge port %d is not seen as connected", port)
	}
	if tcpEstablished(8888) && port != 8888 {
		t.Fatal("8888 seen as connected")
	}

	// 4. 64 KB both ways, intact (every byte value, including 0x0D, 0x11, 0x13, 0x7F)
	data := make([]byte, 64*1024)
	for i := range data {
		data[i] = byte(i*7 + i/256)
	}
	go func() {
		for off := 0; off < len(data); off += 1024 {
			_, _ = c3.Write(data[off : off+1024])
			time.Sleep(time.Millisecond)
		}
	}()
	got, err := readN(c3, len(data), 15*time.Second)
	if err != nil || !bytes.Equal(got, data) {
		i := 0
		for i < len(got) && got[i] == data[i] {
			i++
		}
		t.Fatalf("64 KB echo: first difference at %d of %d (%v)", i, len(data), err)
	}
}
