// mgl001-hass: makes the Xiaomi Smart Home Hub 2 (mgl001_fw LAN variant) a native ESPHome device for
// Home Assistant: the gateway's own entities and a Bluetooth proxy (passive/active/auto scanning,
// active connections and pairing), discovered by mDNS and encrypted with Noise.
//
// mgl001-hass: convierte la Hub 2 (variante LAN de mgl001_fw) en un dispositivo ESPHome nativo para
// Home Assistant: sus propias entidades y un proxy Bluetooth (escaneo pasivo/activo/auto, conexiones
// y emparejamiento), descubierto por mDNS y cifrado con Noise.
package main

import (
	"crypto/rand"
	"encoding/base64"
	"flag"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"mgl001hass/api"
)

const (
	apiMajor, apiMinor = 1, 16
	esphomeVersion     = "2026.9.1" // API level we speak / nivel de API que hablamos
	version            = "2026.10.17"
)

var (
	flagPort     = flag.Int("port", 6053, "API port")
	flagKey      = flag.String("key", "/data/mgl001/api_key", "Noise PSK file (base64, created if missing)")
	flagNoBT     = flag.Bool("no-bt", false, "disable the Bluetooth proxy")
	flagUART     = flag.String("uart", "/dev/ttyS2", "Bluetooth controller UART (H5)")
	flagAvoid    = flag.String("ble-avoid", "", "BLE data channels to avoid, e.g. 20-34 (Wi-Fi 11 + Zigbee 20)")
	flagBondDir  = flag.String("bonds", "/data/mgl001/bonds.json", "BLE bond store")
	flagNoMDNS   = flag.Bool("no-mdns", false, "do not announce by mDNS (tests)")
	flagNoLED    = flag.Bool("no-led", false, "do not drive the LED (tests on a firmware that does)")
	flagZbPort   = flag.Int("zigbee-port", 6638, "Zigbee bridge TCP port for ZHA, announced by mDNS (0: no bridge)")
	flagZbInHass = flag.Bool("zigbee-here", false, "run the Zigbee bridge inside this process (tests); on the gateway it is its own service, mgl001-zigbee")
	flagZbUART   = flag.String("zigbee-uart", "/dev/ttyS1", "Zigbee coordinator UART")
	flagZbNoFlow = flag.Bool("zigbee-no-flow", false, "no RTS/CTS on the Zigbee UART (tests on a pseudo-terminal)")
	flagBtInfo   = flag.Bool("btinfo", false, "read the Bluetooth controller version and exit (read-only)")
	flagBtDFU    = flag.String("btdfu", "", "flash this sealed Telink image to the Bluetooth controller and exit")
	flagBtVer    = flag.Uint("btdfu-version", 3, "firmware version announced to the controller DFU")
)

var (
	srv *Server
	gw  *Gateway
	bt  *Proxy
	upd *Updater
)

func loadKey(path string) []byte {
	if b, err := os.ReadFile(path); err == nil {
		if k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b))); err == nil && len(k) == 32 {
			return k
		}
		log.Fatalf("%s is not a base64 32-byte key", path)
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		log.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	if err := writeFileAtomic(path, []byte(base64.StdEncoding.EncodeToString(k)+"\n"), 0600); err != nil {
		log.Fatal(err)
	}
	log.Printf("new API encryption key in %s", path)
	return k
}

func ifaceMAC(name string) string {
	if i, err := net.InterfaceByName(name); err == nil {
		return strings.ToUpper(i.HardwareAddr.String())
	}
	return "00:00:00:00:00:00"
}

// ESPHome device names are lower case letters, digits and hyphens.
func deviceName() string {
	h, _ := os.Hostname()
	h = strings.ToLower(h)
	var b strings.Builder
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "mgl001"
	}
	return b.String()
}

func main() {
	flag.Parse()
	log.SetFlags(log.LstdFlags)
	// Started as mgl001-zigbee (a link to this binary): the Zigbee bridge alone, its own system service,
	// so restarting the Home Assistant service never cuts Zigbee.
	// Arrancado como mgl001-zigbee: solo el puente Zigbee, su propio servicio del sistema, para que reiniciar
	// el servicio de Home Assistant nunca corte el Zigbee.
	if filepath.Base(os.Args[0]) == "mgl001-zigbee" {
		if *flagZbPort == 0 {
			log.Printf("zigbee bridge off (-zigbee-port 0)")
			// Stay, so the service loop does not restart it every few seconds (a bare select{} with nothing
			// else alive is a fatal "all goroutines are asleep" in Go)
			for {
				time.Sleep(time.Hour)
			}
		}
		(&ZigbeeBridge{ports: []int{*flagZbPort}, uart: *flagZbUART, flow: !*flagZbNoFlow}).Run()
		select {}
	}
	loadMessageIDs()
	if *flagBtInfo || *flagBtDFU != "" {
		var err error
		if *flagBtDFU != "" {
			err = btDFU(*flagBtDFU, uint16(*flagBtVer))
		} else {
			err = btInfo()
		}
		if err != nil {
			log.Fatal(err)
		}
		return
	}

	name := deviceName()
	mac := ifaceMAC("eth0")
	gw = NewGateway()
	upd = NewUpdater()
	if !*flagNoBT {
		bt = NewProxy(0, *flagBondDir)
	}
	srv = &Server{name: name, mac: strings.ToLower(strings.ReplaceAll(mac, ":", "")), psk: loadKey(*flagKey), handler: handle}
	if err := srv.Listen(*flagPort); err != nil {
		log.Fatal(err)
	}
	go gw.Run()
	if *flagZbInHass {
		(&ZigbeeBridge{ports: []int{*flagZbPort}, uart: *flagZbUART, flow: !*flagZbNoFlow}).Run()
	}
	go upd.Run()
	if bt != nil {
		go bt.Run()
	}
	if !*flagNoMDNS {
		go announce(name, mac, *flagPort, *flagZbPort)
	}
	log.Printf("mgl001-hass %s: %s on :%d", version, name, *flagPort)
	select {}
}

func deviceInfo() *api.DeviceInfoResponse {
	host, _ := os.Hostname()
	info := &api.DeviceInfoResponse{
		Name:                   srv.name,
		FriendlyName:           "Xiaomi Hub 2 (" + host + ")",
		MacAddress:             ifaceMAC("eth0"),
		EsphomeVersion:         esphomeVersion,
		CompilationTime:        buildTime,
		Model:                  "Smart Home Hub 2 (mgl001) - mgl001_fw",
		Manufacturer:           "Xiaomi",
		ProjectName:            "nebur692.mgl001_fw",
		ProjectVersion:         version,
		ApiEncryptionSupported: true,
	}
	if bt != nil {
		info.BluetoothProxyFeatureFlags = bt.FeatureFlags()
		info.BluetoothMacAddress = bt.MAC()
	}
	return info
}

var buildTime = time.Now().Format("Jan 02 2006, 15:04:05")

func handle(c *Conn, m proto.Message) {
	switch r := m.(type) {
	case *api.HelloRequest:
		_ = c.Send(&api.HelloResponse{ApiVersionMajor: apiMajor, ApiVersionMinor: apiMinor, ServerInfo: "mgl001_fw " + version, Name: srv.name})
	case *api.AuthenticationRequest: // old clients; there is no password
		_ = c.Send(&api.AuthenticationResponse{})
	case *api.PingRequest:
		_ = c.Send(&api.PingResponse{})
	case *api.DisconnectRequest:
		_ = c.Send(&api.DisconnectResponse{})
		c.CloseWhenSent()
	case *api.DeviceInfoRequest:
		_ = c.Send(deviceInfo())
	case *api.ListEntitiesRequest:
		for _, e := range gw.List() {
			_ = c.Send(e)
		}
		_ = c.Send(upd.Entity())
		_ = c.Send(&api.ListEntitiesDoneResponse{})
	case *api.SubscribeStatesRequest:
		c.States.Store(true)
		for _, s := range gw.States() {
			_ = c.Send(s)
		}
		_ = c.Send(upd.State())
	case *api.SubscribeLogsRequest, *api.SubscribeHomeassistantServicesRequest, *api.SubscribeHomeAssistantStatesRequest:
		// nothing to send / nada que mandar
	case *api.LightCommandRequest:
		gw.LightCommand(r)
	case *api.ButtonCommandRequest:
		gw.ButtonCommand(r)
	case *api.UpdateCommandRequest:
		upd.Command(r)
	default:
		if bt != nil {
			bt.Handle(c, m)
		}
	}
}

// Broadcast sends a state to every client subscribed to states.
func broadcastState(m proto.Message) {
	srv.Each(func(c *Conn) {
		if c.States.Load() {
			_ = c.Send(m)
		}
	})
}

// writeFileAtomic writes a temporary file, flushes it to flash and renames it into place: a power cut
// leaves the old file or the new one, never a truncated one.
// Escribe un temporal, lo fuerza a la flash y lo renombra: un corte de luz deja el viejo o el nuevo.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
