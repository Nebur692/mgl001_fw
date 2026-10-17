// Keepalive and goodbye, as a native ESPHome device does them.
//   - aioesphomeapi pings only when the device has been silent for 20 s. With the Bluetooth proxy on, the
//     gateway is never silent, so Home Assistant may send nothing for minutes: the gateway pings it after
//     a minute without hearing from it, and the 5-minute read deadline only drops a client that is gone.
//   - Before a reboot or a stop, every client gets a DisconnectRequest: Home Assistant then knows the
//     disconnect was expected (the update entity stays available during an install, no fast reconnects).
//
// Ping y despedida, como un dispositivo ESPHome nativo: la pasarela hace ping tras un minuto sin oír al
// cliente, y antes de reiniciar o pararse avisa a cada cliente con DisconnectRequest.
package main

import (
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"mgl001hass/api"
)

const (
	goodbyeWithin = time.Second
)

var pingAfter = time.Minute // a var for tests / variable para las pruebas

// pinger sends a PingRequest when the client has been silent for pingAfter, once per silence.
func (c *Conn) pinger() {
	after := pingAfter
	t := time.NewTicker(after / 3)
	defer t.Stop()
	var pinged int64
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
			last := c.lastRx.Load()
			if time.Since(time.Unix(0, last)) >= after && pinged != last {
				pinged = last
				_ = c.Send(&api.PingRequest{})
			}
		}
	}
}

// Goodbye tells every client that the gateway is going away and waits (at most goodbyeWithin) for them
// to close.
func (s *Server) Goodbye() {
	if s == nil {
		return
	}
	s.Each(func(c *Conn) { _ = c.Send(&api.DisconnectRequest{}) })
	for end := time.Now().Add(goodbyeWithin); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		s.mu.Lock()
		n := len(s.conns)
		s.mu.Unlock()
		if n == 0 {
			return
		}
	}
}

// rebootGateway says goodbye, then reboots.
func rebootGateway() {
	srv.Goodbye()
	_ = exec.Command("sync").Run()
	_ = exec.Command("reboot").Run()
}

// goodbyeOnSignal: a stop or a reboot by someone else (the installer, rcS) also says goodbye first.
func goodbyeOnSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-ch
		log.Printf("api: %v, saying goodbye to the clients", s)
		srv.Goodbye()
		os.Exit(0)
	}()
}

// timeSync asks Home Assistant for the time every 15 minutes, as ESPHome's homeassistant time source
// does: the clock stays right without NTP, and a time zone changed in Home Assistant is followed.
// Pide la hora a Home Assistant cada 15 minutos, como ESPHome: la hora sigue bien sin NTP y se sigue la
// zona horaria si cambia en Home Assistant.
func timeSync() {
	for range time.Tick(15 * time.Minute) {
		asked := false
		srv.Each(func(c *Conn) {
			if !asked && c.States.Load() {
				asked = c.Send(&api.GetTimeRequest{}) == nil
			}
		})
	}
}
