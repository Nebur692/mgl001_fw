// ESPHome native API server: Noise (Noise_NNpsk0_25519_ChaChaPoly_SHA256) framing, as aioesphomeapi
// expects it, and message dispatch by the ids declared in api.proto.
// Servidor de la API nativa de ESPHome: tramas Noise como las espera aioesphomeapi y reparto de
// mensajes por los identificadores declarados en api.proto.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flynn/noise"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"mgl001hass/api"
)

var (
	typeByID = map[uint32]protoreflect.MessageType{}
	idByName = map[protoreflect.FullName]uint32{}
)

// Builds the id <-> message tables from the (id) option of every message in api.proto.
func loadMessageIDs() {
	msgs := api.File_api_proto.Messages()
	for i := 0; i < msgs.Len(); i++ {
		md := msgs.Get(i)
		opts := md.Options()
		if opts == nil || !proto.HasExtension(opts, api.E_Id) {
			continue
		}
		id := proto.GetExtension(opts, api.E_Id).(uint32)
		mt, err := protoregistry.GlobalTypes.FindMessageByName(md.FullName())
		if err != nil {
			continue
		}
		typeByID[id] = mt
		idByName[md.FullName()] = id
	}
}

type Server struct {
	name, mac string
	psk       []byte
	handler   func(c *Conn, m proto.Message)
	mu        sync.Mutex
	conns     map[*Conn]bool
}

const (
	maxConns       = 8   // Home Assistant uses one; a few more for tools / HA usa una; unas pocas más
	maxHandshake   = 256 // largest frame before the key is proven (the client's Noise message is 49 bytes)
	outQueue       = 128 // messages waiting for a slow client before it is dropped
	writeTimeout   = 10 * time.Second
	handshakeLimit = 30 * time.Second
)

type Conn struct {
	srv      *Server
	nc       net.Conn
	r        *bufio.Reader
	enc      *noise.CipherState
	dec      *noise.CipherState
	out      chan []byte // plaintext messages; only the writer goroutine encrypts, so order is kept
	closed   chan struct{}
	once     sync.Once
	Remote   string
	States   atomic.Bool // subscribed to entity states
	Adverts  atomic.Bool // subscribed to raw BLE advertisements
	ConnFree atomic.Bool
}

func (s *Server) Listen(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	// At most maxConns sockets at once, handshakes included: nobody on the LAN can pile them up
	// Como mucho maxConns a la vez, contando los que aún no han dado la clave
	slots := make(chan struct{}, maxConns)
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				log.Printf("api: accept: %v", err)
				time.Sleep(time.Second)
				continue
			}
			select {
			case slots <- struct{}{}:
				go func() {
					defer func() { <-slots }()
					s.serve(nc)
				}()
			default:
				_ = nc.Close()
			}
		}
	}()
	return nil
}

func (s *Server) Each(f func(c *Conn)) {
	s.mu.Lock()
	list := make([]*Conn, 0, len(s.conns))
	for c := range s.conns {
		list = append(list, c)
	}
	s.mu.Unlock()
	for _, c := range list {
		f(c)
	}
}

func (s *Server) serve(nc net.Conn) {
	c := &Conn{srv: s, nc: nc, r: bufio.NewReaderSize(nc, 512), out: make(chan []byte, outQueue),
		closed: make(chan struct{}), Remote: nc.RemoteAddr().String()}
	if tc, ok := nc.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}
	defer c.Close()
	_ = nc.SetDeadline(time.Now().Add(handshakeLimit))
	if err := c.handshake(); err != nil {
		log.Printf("api: %s: handshake: %v", c.Remote, err)
		return
	}
	_ = nc.SetDeadline(time.Time{})
	go c.writer()
	s.mu.Lock()
	if s.conns == nil {
		s.conns = map[*Conn]bool{}
	}
	s.conns[c] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}()
	log.Printf("api: %s connected", c.Remote)
	for {
		// A client that sends nothing for 5 minutes is gone (aioesphomeapi pings every 20 s).
		_ = nc.SetReadDeadline(time.Now().Add(5 * time.Minute))
		m, err := c.readMessage()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				log.Printf("api: %s: %v", c.Remote, err)
			}
			log.Printf("api: %s disconnected", c.Remote)
			return
		}
		if m != nil {
			s.handler(c, m)
		}
	}
}

func (c *Conn) Close() {
	c.once.Do(func() {
		close(c.closed)
		_ = c.nc.Close()
	})
}

// readFrame reads one Noise frame of at most max bytes (the length is checked before anything is allocated).
func (c *Conn) readFrame(max int) ([]byte, error) {
	var h [3]byte
	if _, err := io.ReadFull(c.r, h[:]); err != nil {
		return nil, err
	}
	if h[0] != 0x01 {
		// Plaintext client (Home Assistant's config flow tries this first): tell it encryption is required,
		// the way ESPHome does (an explicit handshake reject), so it asks for the key instead of failing.
		// Cliente sin cifrado (el asistente de HA lo prueba primero): se le dice que hace falta cifrado.
		reason := "Bad indicator byte"
		_, _ = c.nc.Write(append([]byte{0x01, 0x00, byte(1 + len(reason)), 0x01}, reason...))
		return nil, fmt.Errorf("not a noise frame (marker %#x): encryption required sent", h[0])
	}
	n := int(h[1])<<8 | int(h[2])
	if n > max {
		return nil, fmt.Errorf("frame of %d bytes, more than %d", n, max)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(c.r, b); err != nil {
		return nil, err
	}
	return b, nil
}

func (c *Conn) writeFrame(b []byte) error {
	if len(b) > 0xFFFF {
		return fmt.Errorf("frame too big: %d", len(b))
	}
	out := make([]byte, 3+len(b))
	out[0] = 0x01
	out[1] = byte(len(b) >> 8)
	out[2] = byte(len(b))
	copy(out[3:], b)
	_, err := c.nc.Write(out)
	return err
}

func (c *Conn) handshake() error {
	// 1. Client hello: an empty frame.
	if _, err := c.readFrame(maxHandshake); err != nil {
		return err
	}
	// 2. Server hello: protocol 0x01, name\0, mac\0
	hello := append([]byte{0x01}, []byte(c.srv.name)...)
	hello = append(hello, 0)
	hello = append(hello, []byte(c.srv.mac)...)
	hello = append(hello, 0)
	if err := c.writeFrame(hello); err != nil {
		return err
	}
	// 3. Client handshake: 0x00 + Noise message
	f, err := c.readFrame(maxHandshake)
	if err != nil {
		return err
	}
	if len(f) == 0 || f[0] != 0x00 {
		return fmt.Errorf("bad handshake frame")
	}
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256),
		Pattern:               noise.HandshakeNN,
		Initiator:             false,
		Prologue:              []byte("NoiseAPIInit\x00\x00"),
		PresharedKey:          c.srv.psk,
		PresharedKeyPlacement: 0,
	})
	if err != nil {
		return err
	}
	if _, _, _, err = hs.ReadMessage(nil, f[1:]); err != nil {
		_ = c.writeFrame(append([]byte{0x01}, []byte("Handshake MAC failure")...))
		return fmt.Errorf("wrong encryption key")
	}
	out, cs0, cs1, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return err
	}
	if err := c.writeFrame(append([]byte{0x00}, out...)); err != nil {
		return err
	}
	// cs0: initiator -> responder, cs1: responder -> initiator
	c.dec, c.enc = cs0, cs1
	return nil
}

func (c *Conn) readMessage() (proto.Message, error) {
	f, err := c.readFrame(0xFFFF)
	if err != nil {
		return nil, err
	}
	p, err := c.dec.Decrypt(nil, nil, f)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %v", err)
	}
	if len(p) < 4 {
		return nil, fmt.Errorf("short message")
	}
	id := uint32(p[0])<<8 | uint32(p[1])
	mt, ok := typeByID[id]
	if !ok {
		return nil, nil // unknown message: ignored, as ESPHome does
	}
	m := mt.New().Interface()
	if err := proto.Unmarshal(p[4:], m); err != nil {
		return nil, fmt.Errorf("message %d: %v", id, err)
	}
	return m, nil
}

// Send queues one message for the writer goroutine; it never blocks. Safe from any goroutine. A client
// that does not drain its queue is dropped, so it can never hold up the others.
// Send deja el mensaje en la cola del escritor y nunca se bloquea. Un cliente que no la vacía se cierra.
func (c *Conn) Send(m proto.Message) error {
	id, ok := idByName[m.ProtoReflect().Descriptor().FullName()]
	if !ok {
		return fmt.Errorf("no id for %s", m.ProtoReflect().Descriptor().FullName())
	}
	b, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	p := make([]byte, 4+len(b))
	p[0], p[1] = byte(id>>8), byte(id)
	p[2], p[3] = byte(len(b)>>8), byte(len(b))
	copy(p[4:], b)
	select {
	case <-c.closed:
		return net.ErrClosed
	default:
	}
	select {
	case c.out <- p:
		return nil
	case <-c.closed:
		return net.ErrClosed
	default:
		log.Printf("api: %s: too slow, %d messages waiting: dropped", c.Remote, outQueue)
		c.Close()
		return errors.New("client too slow")
	}
}

// CloseWhenSent closes the connection once the messages already queued are written.
func (c *Conn) CloseWhenSent() {
	select {
	case c.out <- nil:
	default:
		c.Close()
	}
}

// writer encrypts and writes the queued messages, in order, until the connection closes.
func (c *Conn) writer() {
	for {
		select {
		case <-c.closed:
			return
		case p := <-c.out:
			if p == nil { // CloseWhenSent: everything before it is out
				c.Close()
				return
			}
			enc, err := c.enc.Encrypt(nil, nil, p)
			if err != nil {
				c.Close()
				return
			}
			_ = c.nc.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := c.writeFrame(enc); err != nil {
				c.Close()
				return
			}
		}
	}
}
