// Zigbee bridge: the coordinator chip's UART (/dev/ttyS1, EZSP over ASH) on a TCP port for ZHA
// (socket://GATEWAY:6638, announced by mDNS). One client at a time; a new one replaces the old (Home Assistant restarted).
// The port is set up completely (raw, 115200, RTS/CTS) and used through Go's poller, so closing it always
// unblocks a read or a write, even when the chip holds CTS.
//
// Puente Zigbee: el UART del chip coordinador en un puerto TCP para ZHA. Un cliente a la vez; uno nuevo
// sustituye al anterior. El puerto se configura entero y se usa con el sondeo de Go: cerrarlo desbloquea
// siempre una lectura o escritura, aunque el chip no dé CTS.
package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// openZigbeeUART opens the chip's UART raw at 115200 with hardware flow control, non-blocking (Go poller).
func openZigbeeUART(path string, flow bool) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	t.Iflag, t.Oflag, t.Lflag = 0, 0, 0
	t.Cflag = unix.CS8 | unix.CREAD | unix.CLOCAL | unix.B115200
	if flow {
		t.Cflag |= unix.CRTSCTS
	}
	t.Ispeed, t.Ospeed = unix.B115200, unix.B115200
	for i := range t.Cc {
		t.Cc[i] = 0
	}
	t.Cc[unix.VMIN], t.Cc[unix.VTIME] = 1, 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, t); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("termios: %v", err)
	}
	_ = unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIOFLUSH)
	return os.NewFile(uintptr(fd), path), nil
}

type ZigbeeBridge struct {
	ports []int // the port announced to ZHA (6638) / el puerto anunciado a ZHA
	uart  string
	flow  bool

	mu  sync.Mutex
	cur net.Conn // the client being served, on any port / el cliente atendido, en cualquier puerto
}

// Run listens on every port; one client at a time across all of them.
func (z *ZigbeeBridge) Run() {
	for _, p := range z.ports {
		if p != 0 {
			go z.listen(p)
		}
	}
}

// listen serves one port forever. If it is taken (an older openmiio_agent started from /data/run.sh) it
// tries again every 30 s.
// Atiende un puerto para siempre. Si está ocupado (un openmiio_agent de /data/run.sh) reintenta cada 30 s.
func (z *ZigbeeBridge) listen(port int) {
	var ln net.Listener
	for {
		var err error
		ln, err = net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			break
		}
		log.Printf("zigbee: port %d: %v; retrying in 30 s", port, err)
		time.Sleep(30 * time.Second)
	}
	log.Printf("zigbee: bridge %s on :%d", z.uart, port)
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Printf("zigbee: accept: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.SetNoDelay(true)
			_ = tc.SetKeepAlive(true)
			_ = tc.SetKeepAlivePeriod(30 * time.Second)
		}
		z.mu.Lock()
		if z.cur != nil {
			log.Printf("zigbee: %s replaces %s", c.RemoteAddr(), z.cur.RemoteAddr())
			_ = z.cur.Close() // its serve() closes the UART before the new one opens it
		}
		z.cur = c
		z.mu.Unlock()
		go z.serve(c)
	}
}

var zigbeeUARTMu sync.Mutex // one user of the UART at a time / un solo usuario del UART a la vez

func (z *ZigbeeBridge) serve(c net.Conn) {
	defer func() {
		_ = c.Close()
		z.mu.Lock()
		if z.cur == c {
			z.cur = nil
		}
		z.mu.Unlock()
	}()
	zigbeeUARTMu.Lock()
	defer zigbeeUARTMu.Unlock()
	f, err := openZigbeeUART(z.uart, z.flow)
	if err != nil {
		log.Printf("zigbee: %s: %v", z.uart, err)
		return
	}
	defer f.Close()
	log.Printf("zigbee: %s connected", c.RemoteAddr())
	done := make(chan struct{}, 2)
	// Plain copies (no splice/sendfile shortcuts between a socket and a tty)
	go func() { _, _ = io.Copy(struct{ io.Writer }{f}, struct{ io.Reader }{c}); done <- struct{}{} }() // ZHA -> chip
	go func() { _, _ = io.Copy(struct{ io.Writer }{c}, struct{ io.Reader }{f}); done <- struct{}{} }() // chip -> ZHA
	<-done
	// Whichever side ended, close both so the other copy returns too
	// Acabe el lado que acabe, se cierran los dos para que la otra copia también vuelva
	_ = c.Close()
	_ = f.Close()
	<-done
	log.Printf("zigbee: %s disconnected", c.RemoteAddr())
}
