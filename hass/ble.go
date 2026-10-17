// Bluetooth proxy for Home Assistant over the ESPHome API: raw advertisements (batched like ESPHome),
// passive/active scanning set by Home Assistant (which also drives AUTO), up to 3 active connections
// with GATT, notifications, pairing and unpairing. The Telink chip on /dev/ttyS2 speaks HCI over H5.
//
// Proxy Bluetooth para HA por la API de ESPHome: anuncios en crudo (en lotes como ESPHome), escaneo
// pasivo/activo que elige HA (que también maneja el AUTO), hasta 3 conexiones activas con GATT,
// notificaciones, emparejamiento y desemparejamiento. El chip Telink de /dev/ttyS2 habla HCI por H5.
package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rigado/ble"
	"github.com/rigado/ble/linux"
	"github.com/rigado/ble/linux/hci"
	"github.com/rigado/ble/linux/hci/bond"
	"github.com/rigado/ble/linux/hci/cmd"
	"google.golang.org/protobuf/proto"

	"mgl001hass/api"
)

const (
	maxConnections = 3
	batchSize      = 16
	batchEvery     = 100 * time.Millisecond
)

const featureFlags = 1<<0 | // PASSIVE_SCAN
	1<<1 | // ACTIVE_CONNECTIONS
	1<<2 | // REMOTE_CACHING (v3 connections: Home Assistant writes the CCCD itself)
	1<<3 | // PAIRING
	1<<4 | // CACHE_CLEARING
	1<<5 | // RAW_ADVERTISEMENTS
	1<<6 // FEATURE_STATE_AND_MODE (passive / active / auto from Home Assistant)

type Proxy struct {
	idx      int
	bondPath string
	bonds    hci.BondManager
	dev      *linux.Device
	mac      string
	h5       *H5
	h4addr   string

	mu         sync.Mutex
	mode       api.BluetoothScannerMode
	configured api.BluetoothScannerMode
	state      api.BluetoothScannerState
	scanning   bool
	paused     int
	batch      []*api.BluetoothLERawAdvertisement
	conns      map[uint64]*bleConn
	adverts    atomic.Int64
	srCount    atomic.Int64 // scan responses seen, for the log / respuestas de escaneo vistas, para el registro
	advErrors  atomic.Int64
}

type bleConn struct {
	addr   uint64
	client ble.Client
	chars  map[uint16]*ble.Characteristic
	descs  map[uint16]*ble.Descriptor
	queue  chan func()
	done   chan struct{}
	cancel context.CancelFunc
	mtu    int
}

func NewProxy(idx int, bondPath string) *Proxy {
	return &Proxy{idx: idx, bondPath: bondPath, conns: map[uint64]*bleConn{},
		mode: api.BluetoothScannerMode_BLUETOOTH_SCANNER_MODE_ACTIVE, configured: api.BluetoothScannerMode_BLUETOOTH_SCANNER_MODE_ACTIVE,
		state: api.BluetoothScannerState_BLUETOOTH_SCANNER_STATE_STARTING}
}

func (p *Proxy) FeatureFlags() uint32 { return featureFlags }
func (p *Proxy) MAC() string          { p.mu.Lock(); defer p.mu.Unlock(); return p.mac }
func (p *Proxy) TakeAdvertCount() int64 {
	return p.adverts.Swap(0)
}

func (p *Proxy) Run() {
	go p.flushLoop()
	go p.statsLoop()
	// A controller that does not answer is retried after 30 s, then less and less often (up to 10 min)
	// Un controlador que no responde se reintenta a los 30 s y luego cada vez menos (hasta 10 min)
	wait := 30 * time.Second
	for {
		if err := p.open(); err != nil {
			log.Printf("bt: %v; retrying in %s", err, wait)
			p.setState(api.BluetoothScannerState_BLUETOOTH_SCANNER_STATE_FAILED)
			time.Sleep(wait)
			wait = min(wait*2, 10*time.Minute)
			continue
		}
		wait = 30 * time.Second
		for p.dev.HCI.Error() == nil {
			time.Sleep(5 * time.Second)
		}
		log.Printf("bt: HCI failed: %v; reopening", p.dev.HCI.Error())
		p.setState(api.BluetoothScannerState_BLUETOOTH_SCANNER_STATE_FAILED)
		_ = p.dev.Stop()
		p.mu.Lock()
		p.dev = nil
		p.mu.Unlock()
		// The whole link again: the controller may have reset, so reset it and sync H5 from scratch
		// El enlace entero otra vez: el chip puede haberse reiniciado; se reinicia y se sincroniza H5
		p.h5.Close()
		p.h5 = nil
		time.Sleep(5 * time.Second)
	}
}

func (p *Proxy) open() error {
	// Our own H5 link on ttyS2 (no hciattach, no kernel H5 driver), bridged to the library over H4.
	// Nuestro propio enlace H5 en ttyS2 (sin hciattach ni el driver H5 del kernel), puenteado por H4.
	if p.h5 == nil {
		resetController()
		h, err := NewH5(*flagUART)
		if err != nil {
			return err
		}
		addr, err := h.ServeH4("127.0.0.1:0")
		if err != nil {
			return err
		}
		p.h5, p.h4addr = h, addr
	}
	p.mu.Lock()
	if p.bonds == nil {
		p.bonds = bond.NewBondManager(p.bondPath)
	}
	bonds := p.bonds
	p.mu.Unlock()
	d, err := linux.NewDeviceWithName("mgl001", ble.OptTransportH4Socket(p.h4addr, 5*time.Second), ble.OptCentralRole(),
		ble.OptDialerTimeout(20*time.Second), ble.OptEnableSecurity(bonds),
		// 30-50 ms interval, 5 s supervision, like BlueZ; the library default (7.5 ms) makes sensors drop
		// Intervalo de 30-50 ms y 5 s de supervisión, como BlueZ; el de la librería (7,5 ms) tira sensores
		ble.OptConnParams(cmd.LECreateConnection{LEScanInterval: 0x60, LEScanWindow: 0x60, ConnIntervalMin: 0x18,
			ConnIntervalMax: 0x28, SupervisionTimeout: 0x1F4}),
		ble.OptErrorHandler(func(error) { p.advErrors.Add(1) }))
	if err != nil {
		return err
	}
	if err := d.HCI.SetAdvHandler(p.onAdvert); err != nil {
		return err
	}
	p.mu.Lock()
	p.dev = d
	p.mac = strings.ToUpper(d.HCI.Addr().String())
	p.mu.Unlock()
	logController(d.HCI)
	setChannelMap(d.HCI, *flagAvoid)
	log.Printf("bt: controller %s ready", p.mac)
	return p.startScan()
}

// With no connection the radio listens all the time (30 ms window every 30 ms). With connections
// open it listens 20 % (10 ms every 50 ms): this controller does not leave room for connection
// events otherwise, and connections drop during service discovery.
// Sin conexiones la radio escucha todo el tiempo; con conexiones abiertas, el 20 %: este controlador
// no deja sitio a los eventos de la conexión y las conexiones se caen durante el descubrimiento.
func scanParams(active, busy bool) cmd.LESetScanParameters {
	t := uint8(0)
	if active {
		t = 1
	}
	if busy {
		return cmd.LESetScanParameters{LEScanType: t, LEScanInterval: 0x50, LEScanWindow: 0x10}
	}
	return cmd.LESetScanParameters{LEScanType: t, LEScanInterval: 0x30, LEScanWindow: 0x30}
}

func (p *Proxy) startScan() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paused > 0 || p.dev == nil {
		return nil
	}
	// The library only sends scan parameters at init: send them to the controller ourselves.
	// La librería solo manda los parámetros al iniciar: se los mandamos nosotros al controlador.
	sp := scanParams(p.mode == api.BluetoothScannerMode_BLUETOOTH_SCANNER_MODE_ACTIVE, len(p.conns) > 0)
	_ = p.dev.HCI.SetScanParams(sp)
	if err := p.dev.HCI.Send(&sp, nil); err != nil {
		return fmt.Errorf("scan parameters: %v", err)
	}
	if err := p.dev.HCI.Scan(true); err != nil {
		return err
	}
	p.scanning = true
	p.state = api.BluetoothScannerState_BLUETOOTH_SCANNER_STATE_RUNNING
	go p.sendState(nil)
	return nil
}

func (p *Proxy) stopScan() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.scanning && p.dev != nil {
		_ = p.dev.HCI.StopScanning()
	}
	p.scanning = false
	p.state = api.BluetoothScannerState_BLUETOOTH_SCANNER_STATE_STOPPED
}

// Scanning pauses while a connection is being set up, as ESPHome does.
func (p *Proxy) pause()  { p.mu.Lock(); p.paused++; p.mu.Unlock(); p.stopScan() }
func (p *Proxy) resume() { p.mu.Lock(); p.paused--; p.mu.Unlock(); _ = p.startScan() }

func (p *Proxy) setState(s api.BluetoothScannerState) {
	p.mu.Lock()
	p.state = s
	p.mu.Unlock()
	p.sendState(nil)
}

func (p *Proxy) stateMsg() *api.BluetoothScannerStateResponse {
	p.mu.Lock()
	defer p.mu.Unlock()
	return &api.BluetoothScannerStateResponse{State: p.state, Mode: p.mode, ConfiguredMode: p.configured}
}

func (p *Proxy) sendState(to *Conn) {
	m := p.stateMsg()
	if to != nil {
		_ = to.Send(m)
		return
	}
	srv.Each(func(c *Conn) {
		if c.Adverts.Load() {
			_ = c.Send(m)
		}
	})
}

func macToUint(s string) uint64 {
	hw, err := net.ParseMAC(s)
	if err != nil || len(hw) != 6 {
		return 0
	}
	var b [8]byte
	copy(b[2:], hw)
	return binary.BigEndian.Uint64(b[:])
}

func uintToMAC(v uint64) string {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return net.HardwareAddr(b[2:]).String()
}

func (p *Proxy) onAdvert(a ble.Advertisement) {
	ha, ok := a.(*hci.Advertisement)
	if !ok {
		return
	}
	data := ha.SrData() // the scan response arrives as the same advertisement, now with SrData
	if len(data) > 0 {
		p.srCount.Add(1)
	}
	if len(data) == 0 {
		data = ha.Data()
	}
	adv := &api.BluetoothLERawAdvertisement{Address: macToUint(a.Addr().String()), Rssi: int32(a.RSSI()),
		AddressType: uint32(ha.AddrType()), Data: append([]byte(nil), data...)}
	p.adverts.Add(1)
	p.mu.Lock()
	p.batch = append(p.batch, adv)
	full := len(p.batch) >= batchSize
	p.mu.Unlock()
	if full {
		p.flush()
	}
}

func (p *Proxy) flush() {
	p.mu.Lock()
	if len(p.batch) == 0 {
		p.mu.Unlock()
		return
	}
	m := &api.BluetoothLERawAdvertisementsResponse{Advertisements: p.batch}
	p.batch = nil
	p.mu.Unlock()
	srv.Each(func(c *Conn) {
		if c.Adverts.Load() {
			_ = c.Send(m)
		}
	})
}

func (p *Proxy) flushLoop() {
	t := time.NewTicker(batchEvery)
	defer t.Stop()
	for range t.C {
		p.flush()
	}
}

func (p *Proxy) freeMsg() *api.BluetoothConnectionsFreeResponse {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := &api.BluetoothConnectionsFreeResponse{Free: uint32(maxConnections - len(p.conns)), Limit: maxConnections}
	for a := range p.conns {
		m.Allocated = append(m.Allocated, a)
	}
	return m
}

func (p *Proxy) sendFree() {
	m := p.freeMsg()
	srv.Each(func(c *Conn) {
		if c.ConnFree.Load() {
			_ = c.Send(m)
		}
	})
}

// Handle takes every Bluetooth request from Home Assistant.
func (p *Proxy) Handle(c *Conn, m proto.Message) {
	switch r := m.(type) {
	case *api.SubscribeBluetoothLEAdvertisementsRequest:
		c.Adverts.Store(true)
		p.sendState(c)
	case *api.UnsubscribeBluetoothLEAdvertisementsRequest:
		c.Adverts.Store(false)
	case *api.SubscribeBluetoothConnectionsFreeRequest:
		c.ConnFree.Store(true)
		_ = c.Send(p.freeMsg())
	case *api.BluetoothScannerSetModeRequest:
		p.setMode(r.Mode)
	case *api.BluetoothDeviceRequest:
		p.device(c, r)
	case *api.BluetoothGATTGetServicesRequest:
		p.onConn(c, r.Address, 0, func(bc *bleConn) { p.services(c, bc) })
	case *api.BluetoothGATTReadRequest:
		p.onConn(c, r.Address, r.Handle, func(bc *bleConn) {
			ch := bc.chars[uint16(r.Handle)]
			if ch == nil {
				gattError(c, r.Address, r.Handle, 0x01)
				return
			}
			v, err := bc.client.ReadLongCharacteristic(ch)
			if err != nil {
				gattError(c, r.Address, r.Handle, attErr(err))
				return
			}
			_ = c.Send(&api.BluetoothGATTReadResponse{Address: r.Address, Handle: r.Handle, Data: v})
		})
	case *api.BluetoothGATTWriteRequest:
		p.onConn(c, r.Address, r.Handle, func(bc *bleConn) {
			ch := bc.chars[uint16(r.Handle)]
			if ch == nil {
				gattError(c, r.Address, r.Handle, 0x01)
				return
			}
			if err := bc.client.WriteCharacteristic(ch, r.Data, !r.Response); err != nil {
				gattError(c, r.Address, r.Handle, attErr(err))
				return
			}
			if r.Response {
				_ = c.Send(&api.BluetoothGATTWriteResponse{Address: r.Address, Handle: r.Handle})
			}
		})
	case *api.BluetoothGATTReadDescriptorRequest:
		p.onConn(c, r.Address, r.Handle, func(bc *bleConn) {
			d := bc.descs[uint16(r.Handle)]
			if d == nil {
				gattError(c, r.Address, r.Handle, 0x01)
				return
			}
			v, err := bc.client.ReadDescriptor(d)
			if err != nil {
				gattError(c, r.Address, r.Handle, attErr(err))
				return
			}
			_ = c.Send(&api.BluetoothGATTReadResponse{Address: r.Address, Handle: r.Handle, Data: v})
		})
	case *api.BluetoothGATTWriteDescriptorRequest:
		p.onConn(c, r.Address, r.Handle, func(bc *bleConn) {
			d := bc.descs[uint16(r.Handle)]
			if d == nil {
				gattError(c, r.Address, r.Handle, 0x01)
				return
			}
			if err := bc.client.WriteDescriptor(d, r.Data); err != nil {
				gattError(c, r.Address, r.Handle, attErr(err))
				return
			}
			_ = c.Send(&api.BluetoothGATTWriteResponse{Address: r.Address, Handle: r.Handle})
		})
	case *api.BluetoothGATTNotifyRequest:
		p.onConn(c, r.Address, r.Handle, func(bc *bleConn) { p.notify(c, bc, r) })
	}
}

func (p *Proxy) setMode(m api.BluetoothScannerMode) {
	p.mu.Lock()
	same := p.mode == m
	p.mode = m
	p.mu.Unlock()
	if same {
		p.sendState(nil)
		return
	}
	log.Printf("bt: scanning mode %v", m)
	p.stopScan()
	_ = p.startScan()
}

func gattError(c *Conn, addr uint64, handle uint32, code int32) {
	_ = c.Send(&api.BluetoothGATTErrorResponse{Address: addr, Handle: handle, Error: code})
}

// ATT error code when the library reports one, else "unlikely error" (0x0E)
func attErr(err error) int32 {
	if e, ok := err.(interface{ Code() int }); ok {
		return int32(e.Code())
	}
	return 0x0E
}

// onConn runs f on the connection's own queue, so ATT operations keep their order.
func (p *Proxy) onConn(c *Conn, addr uint64, handle uint32, f func(*bleConn)) {
	p.mu.Lock()
	bc := p.conns[addr]
	connected := bc != nil && bc.client != nil
	p.mu.Unlock()
	if !connected {
		gattError(c, addr, handle, 0x101) // not connected
		return
	}
	select {
	case <-bc.done:
		gattError(c, addr, handle, 0x101)
	case bc.queue <- func() { f(bc) }:
	default:
		gattError(c, addr, handle, 0x0E)
	}
}

func connResp(c *Conn, addr uint64, ok bool, mtu int, code int32) {
	_ = c.Send(&api.BluetoothDeviceConnectionResponse{Address: addr, Connected: ok, Mtu: uint32(mtu), Error: code})
}

func (p *Proxy) device(c *Conn, r *api.BluetoothDeviceRequest) {
	switch r.RequestType {
	case api.BluetoothDeviceRequestType_BLUETOOTH_DEVICE_REQUEST_TYPE_CONNECT_V3_WITH_CACHE,
		api.BluetoothDeviceRequestType_BLUETOOTH_DEVICE_REQUEST_TYPE_CONNECT_V3_WITHOUT_CACHE:
		go p.connect(c, r.Address, r.AddressType)
	case api.BluetoothDeviceRequestType_BLUETOOTH_DEVICE_REQUEST_TYPE_DISCONNECT:
		p.mu.Lock()
		bc := p.conns[r.Address]
		var cl ble.Client
		if bc != nil {
			cl = bc.client
		}
		p.mu.Unlock()
		if bc == nil {
			connResp(c, r.Address, false, 0, 0)
			return
		}
		if cl != nil {
			_ = cl.CancelConnection()
		} else if bc.cancel != nil {
			bc.cancel()
		}
	case api.BluetoothDeviceRequestType_BLUETOOTH_DEVICE_REQUEST_TYPE_PAIR:
		p.onConn(c, r.Address, 0, func(bc *bleConn) {
			err := bc.client.Pair(ble.AuthData{}, 30*time.Second)
			code := int32(0)
			if err != nil {
				log.Printf("bt: pair %s: %v", uintToMAC(r.Address), err)
				code = 0x05 // insufficient authentication
			}
			_ = c.Send(&api.BluetoothDevicePairingResponse{Address: r.Address, Paired: err == nil, Error: code})
		})
	case api.BluetoothDeviceRequestType_BLUETOOTH_DEVICE_REQUEST_TYPE_UNPAIR:
		// The library keys bonds by the address in hex; delete both byte orders.
		hw := mustMAC(uintToMAC(r.Address))
		rev := make([]byte, len(hw))
		for i := range hw {
			rev[i] = hw[len(hw)-1-i]
		}
		p.mu.Lock()
		bonds := p.bonds
		bc := p.conns[r.Address]
		var cl ble.Client
		if bc != nil {
			cl = bc.client
		}
		p.mu.Unlock()
		if bonds == nil { // the controller never opened: there is nothing stored
			_ = c.Send(&api.BluetoothDeviceUnpairingResponse{Address: r.Address, Success: false, Error: 0x0C})
			return
		}
		e1 := bonds.Delete(hex.EncodeToString(hw))
		e2 := bonds.Delete(hex.EncodeToString(rev))
		_ = c.Send(&api.BluetoothDeviceUnpairingResponse{Address: r.Address, Success: e1 == nil || e2 == nil})
		if cl != nil {
			_ = cl.CancelConnection()
		}
	case api.BluetoothDeviceRequestType_BLUETOOTH_DEVICE_REQUEST_TYPE_CLEAR_CACHE:
		_ = c.Send(&api.BluetoothDeviceClearCacheResponse{Address: r.Address, Success: true})
	}
}

func mustMAC(s string) []byte {
	hw, _ := net.ParseMAC(s)
	return hw
}

func (p *Proxy) connect(c *Conn, addr uint64, addrType uint32) {
	p.mu.Lock()
	dev := p.dev
	if dev == nil { // controller not open (failed or still starting)
		p.mu.Unlock()
		connResp(c, addr, false, 0, 0x0C) // command disallowed / no permitido ahora
		return
	}
	if old, busy := p.conns[addr]; busy {
		// Asked again: answer at once instead of letting Home Assistant wait its 20 s
		// Se vuelve a pedir: se responde ya en vez de dejar a HA esperando sus 20 s
		cl, mtu := old.client, old.mtu
		p.mu.Unlock()
		if cl != nil {
			connResp(c, addr, true, mtu, 0)
		} else {
			connResp(c, addr, false, 0, 0x0C) // still connecting / aún conectando
		}
		return
	}
	if len(p.conns) >= maxConnections {
		p.mu.Unlock()
		connResp(c, addr, false, 0, 0x0D) // no free slot / sin hueco libre
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	bc := &bleConn{addr: addr, queue: make(chan func(), 64), done: make(chan struct{}), cancel: cancel,
		chars: map[uint16]*ble.Characteristic{}, descs: map[uint16]*ble.Descriptor{}}
	p.conns[addr] = bc
	p.mu.Unlock()
	p.sendFree()
	// The link belongs to the API session that asked for it: when Home Assistant goes away, so does the
	// link (as ESPHome does), and the slot is free for its next session.
	// El enlace es de la sesión que lo pidió: si HA se va, se cierra (como en ESPHome) y el hueco queda libre.
	go func() {
		select {
		case <-c.closed:
			cancel()
			p.mu.Lock()
			cl := bc.client
			p.mu.Unlock()
			if cl != nil {
				_ = cl.CancelConnection()
			}
		case <-bc.done:
		}
	}()

	fail := func(code int32, err error) {
		log.Printf("bt: connect %s: %v", uintToMAC(addr), err)
		cancel()
		p.mu.Lock()
		delete(p.conns, addr)
		p.mu.Unlock()
		close(bc.done)
		p.restartScan()
		p.sendFree()
		connResp(c, addr, false, 0, code)
	}

	var a ble.Addr = ble.NewAddr(uintToMAC(addr))
	if addrType == 1 {
		a = hci.RandomAddress{Addr: a}
	}
	// Scanning stays paused until service discovery ends; then it resumes at the reduced duty cycle.
	// El escaneo sigue parado hasta acabar el descubrimiento; luego vuelve con el ciclo reducido.
	// Sleepy sensors often drop the first connection events (HCI reason 0x3E, "failed to be
	// established"): retry inside the proxy, within Home Assistant's 20 s budget.
	// Los sensores dormilones suelen perder los primeros eventos (motivo 0x3E): se reintenta aquí
	// dentro, sin pasar de los 20 s que espera HA.
	p.pause()
	var cl ble.Client
	var prof *ble.Profile
	var err error
	mtu := 23
	for attempt := 1; attempt <= 3; attempt++ {
		cl, err = dev.HCI.Dial(ctx, a)
		if err != nil {
			break // dial timeout or cancel: no time left for another attempt
		}
		mtu = 23
		if m, e := cl.ExchangeMTU(517); e == nil {
			mtu = m
		}
		prof, err = cl.DiscoverProfile(true)
		if err == nil {
			break
		}
		log.Printf("bt: connect %s attempt %d: %v", uintToMAC(addr), attempt, err)
		_ = cl.CancelConnection()
		cl = nil
		if ctx.Err() != nil {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	p.resume()
	if cl == nil || err != nil {
		if err == nil {
			err = fmt.Errorf("no connection")
		}
		fail(0x3E, err) // connection failed to be established
		return
	}
	for _, s := range prof.Services {
		for _, ch := range s.Characteristics {
			bc.chars[ch.ValueHandle] = ch
			for _, d := range ch.Descriptors {
				bc.descs[d.Handle] = d
			}
		}
	}
	p.mu.Lock()
	bc.client, bc.mtu = cl, mtu
	p.mu.Unlock()
	go func() {
		for {
			select {
			case <-bc.done:
				return
			case f := <-bc.queue:
				f()
			}
		}
	}()
	log.Printf("bt: connected %s (mtu %d)", uintToMAC(addr), mtu)
	connResp(c, addr, true, mtu, 0)
	go func() {
		<-cl.Disconnected()
		cancel()
		p.mu.Lock()
		delete(p.conns, addr)
		p.mu.Unlock()
		p.restartScan()
		close(bc.done)
		log.Printf("bt: disconnected %s", uintToMAC(addr))
		connResp(c, addr, false, 0, 0x13) // remote user terminated connection
		p.sendFree()
	}()
}

// 16/32-bit UUIDs go as short_uuid; 128-bit as two big-endian halves.
func uuidFields(u ble.UUID) (long []uint64, short uint32) {
	b := []byte(u) // little endian in the library
	switch len(b) {
	case 2:
		return nil, uint32(binary.LittleEndian.Uint16(b))
	case 4:
		return nil, binary.LittleEndian.Uint32(b)
	}
	be := make([]byte, len(b))
	for i := range b {
		be[i] = b[len(b)-1-i]
	}
	if len(be) != 16 {
		return nil, 0
	}
	return []uint64{binary.BigEndian.Uint64(be[:8]), binary.BigEndian.Uint64(be[8:])}, 0
}

func (p *Proxy) services(c *Conn, bc *bleConn) {
	prof := bc.client.Profile()
	if prof == nil {
		gattError(c, bc.addr, 0, 0x0E)
		return
	}
	for _, s := range prof.Services {
		l, sh := uuidFields(s.UUID)
		gs := &api.BluetoothGATTService{Uuid: l, ShortUuid: sh, Handle: uint32(s.Handle)}
		for _, ch := range s.Characteristics {
			l, sh := uuidFields(ch.UUID)
			gc := &api.BluetoothGATTCharacteristic{Uuid: l, ShortUuid: sh, Handle: uint32(ch.ValueHandle), Properties: uint32(ch.Property)}
			for _, d := range ch.Descriptors {
				l, sh := uuidFields(d.UUID)
				gc.Descriptors = append(gc.Descriptors, &api.BluetoothGATTDescriptor{Uuid: l, ShortUuid: sh, Handle: uint32(d.Handle)})
			}
			gs.Characteristics = append(gs.Characteristics, gc)
		}
		_ = c.Send(&api.BluetoothGATTGetServicesResponse{Address: bc.addr, Services: []*api.BluetoothGATTService{gs}})
	}
	_ = c.Send(&api.BluetoothGATTGetServicesDoneResponse{Address: bc.addr})
}

func (p *Proxy) notify(c *Conn, bc *bleConn, r *api.BluetoothGATTNotifyRequest) {
	ch := bc.chars[uint16(r.Handle)]
	if ch == nil {
		gattError(c, r.Address, r.Handle, 0x01)
		return
	}
	ind := ch.Property&ble.CharNotify == 0 && ch.Property&ble.CharIndicate != 0
	var err error
	if r.Enable {
		err = bc.client.Subscribe(ch, ind, func(_ uint, data []byte) {
			_ = c.Send(&api.BluetoothGATTNotifyDataResponse{Address: r.Address, Handle: r.Handle, Data: append([]byte(nil), data...)})
		})
	} else {
		err = bc.client.Unsubscribe(ch, ind)
	}
	if err != nil {
		gattError(c, r.Address, r.Handle, attErr(err))
		return
	}
	_ = c.Send(&api.BluetoothGATTNotifyResponse{Address: r.Address, Handle: r.Handle})
}

// statsLoop logs what the radio sees every 10 min / registra lo que ve la radio cada 10 min.
func (p *Proxy) statsLoop() {
	for {
		time.Sleep(10 * time.Minute)
		log.Printf("bt: last 10 min: %d scan responses, %d advertising errors (mode %v)", p.srCount.Swap(0), p.advErrors.Swap(0), p.stateMsg().Mode)
	}
}

// restartScan applies the scan parameters again (duty cycle depends on open connections).
func (p *Proxy) restartScan() {
	p.stopScan()
	_ = p.startScan()
}

// logController logs what the controller says about itself (diagnosis, read-only HCI commands).
// Registra lo que el controlador dice de sí mismo (diagnóstico, comandos HCI de solo lectura).
func logController(h *hci.HCI) {
	var v cmd.ReadLocalVersionInformationRP
	if err := h.Send(&cmd.ReadLocalVersionInformation{}, &v); err == nil {
		log.Printf("bt: HCI %d.%04x LMP %d.%04x manufacturer %#04x", v.HCIVersion, v.HCIRevision, v.LMPPAMVersion, v.LMPPAMSubversion, v.ManufacturerName)
	}
	var s cmd.LEReadSupportedStatesRP
	if err := h.Send(&cmd.LEReadSupportedStates{}, &s); err == nil {
		log.Printf("bt: LE states %#016x", s.LEStates)
	}
	var p cmd.LEReadAdvertisingChannelTxPowerRP
	if err := h.Send(&cmd.LEReadAdvertisingChannelTxPower{}, &p); err == nil {
		log.Printf("bt: advertising TX power %d dBm", int8(p.TransmitPowerLevel))
	}
}

// setChannelMap tells the controller which BLE data channels to avoid (LE Set Host Channel
// Classification). Advertising channels 37-39 are not affected.
// Indica al controlador qué canales de datos BLE evitar; los de anuncios (37-39) no cambian.
func setChannelMap(h *hci.HCI, avoid string) {
	if avoid == "" {
		return
	}
	var lo, hi int
	if _, err := fmt.Sscanf(avoid, "%d-%d", &lo, &hi); err != nil || lo < 0 || hi > 36 || lo > hi {
		log.Printf("bt: bad -ble-avoid %q", avoid)
		return
	}
	var c cmd.LESetHostChannelClassification
	for ch := 0; ch <= 36; ch++ {
		if ch < lo || ch > hi {
			c.ChannelMap[ch/8] |= 1 << (ch % 8)
		}
	}
	var rp cmd.LESetHostChannelClassificationRP
	err := h.Send(&c, &rp)
	log.Printf("bt: avoiding data channels %d-%d: err=%v status=%#02x map=%x", lo, hi, err, rp.Status, c.ChannelMap)
}
