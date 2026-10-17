package main

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"

	"mgl001hass/api"
)

func testConn() *Conn {
	a, b := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, b) }()
	return &Conn{nc: a, out: make(chan []byte, outQueue), closed: make(chan struct{}), Remote: "test"}
}

// The message id at the head of a queued frame.
func queuedIDs(c *Conn) []uint16 {
	var ids []uint16
	for len(c.out) > 0 {
		p := <-c.out
		ids = append(ids, uint16(p[0])<<8|uint16(p[1]))
	}
	return ids
}

func idOf(m proto.Message) uint16 { return uint16(idByName[m.ProtoReflect().Descriptor().FullName()]) }

func TestPingAfterSilence(t *testing.T) {
	loadMessageIDs()
	old := pingAfter
	pingAfter = 90 * time.Millisecond
	defer func() { pingAfter = old }()
	c := testConn()
	c.lastRx.Store(time.Now().UnixNano())
	go c.pinger()
	defer c.Close()
	time.Sleep(400 * time.Millisecond)
	ids := queuedIDs(c)
	if len(ids) != 1 || ids[0] != idOf(&api.PingRequest{}) {
		t.Fatalf("one ping per silence expected, got %v", ids)
	}
	c.lastRx.Store(time.Now().UnixNano()) // the client answered: a new silence gets a new ping
	time.Sleep(400 * time.Millisecond)
	if ids := queuedIDs(c); len(ids) != 1 {
		t.Fatalf("after an answer, %v", ids)
	}
}

func TestAdvertsDroppedNotTheSession(t *testing.T) {
	loadMessageIDs()
	c := testConn()
	for i := 0; i <= outQueue*3/4; i++ {
		if err := c.Send(&api.PingRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	n := len(c.out)
	if err := c.Send(&api.BluetoothLERawAdvertisementsResponse{}); err != nil || len(c.out) != n {
		t.Fatalf("advert: err %v, queue %d -> %d", err, n, len(c.out))
	}
	select {
	case <-c.closed:
		t.Fatal("the session was closed for an advert")
	default:
	}
	if err := c.Send(&api.PingRequest{}); err != nil || len(c.out) != n+1 {
		t.Fatal("other messages must still fit")
	}
}

func TestGoodbyeSendsDisconnect(t *testing.T) {
	loadMessageIDs()
	s := &Server{conns: map[*Conn]bool{}}
	c := testConn()
	s.conns[c] = true
	start := time.Now()
	s.Goodbye()
	if d := time.Since(start); d > 2*goodbyeWithin {
		t.Fatalf("goodbye took %v", d)
	}
	if ids := queuedIDs(c); len(ids) != 1 || ids[0] != idOf(&api.DisconnectRequest{}) {
		t.Fatalf("got %v", ids)
	}
	var none *Server
	none.Goodbye() // no server (Zigbee-only process): nothing to do
}

func TestLEDColorBrightness(t *testing.T) {
	g := &Gateway{led: ledValue{On: true, Brightness: 1, R: 1, ColorBrightness: 0.5}}
	if m := g.ledStateMsg(); m.ColorBrightness != 0.5 || m.Red != 1 {
		t.Fatalf("%v %v", m.ColorBrightness, m.Red)
	}
	g.led.ColorBrightness = 0 // a file saved by an older version
	if m := g.ledStateMsg(); m.ColorBrightness != 1 {
		t.Fatalf("old file: %v", m.ColorBrightness)
	}
}

func TestInstallerRunning(t *testing.T) {
	old := installLock
	installLock = filepath.Join(t.TempDir(), "lock")
	defer func() { installLock = old }()
	if installerRunning() {
		t.Fatal("no lock file, but running")
	}
	f, err := os.Create(installLock)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if installerRunning() {
		t.Fatal("file without a lock, but running")
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil { // as `flock -n 8` in the installer
		t.Fatal(err)
	}
	if !installerRunning() {
		t.Fatal("held lock not seen")
	}
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	if installerRunning() {
		t.Fatal("released lock still seen")
	}
}

func TestUptime(t *testing.T) {
	if u := uptime(); u <= 0 {
		t.Fatalf("uptime %v", u)
	}
}
