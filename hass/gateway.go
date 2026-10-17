// The gateway's own entities: RGB LED (PWM), the physical button (input event), system sensors,
// Zigbee bridge connection, firmware/bank and a restart button.
// Las entidades propias de la pasarela: LED RGB (PWM), el botón (evento de entrada), sensores del
// sistema, conexión del puente Zigbee, firmware/banco y un botón de reinicio.
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	"mgl001hass/api"
)

const (
	keyLED = iota + 1
	keyButton
	keyUptime
	keyMemory
	keyLoad
	keyDataFree
	keyZigbee
	keyFirmware
	keyBank
	keyRestart
	keyBTAdverts
)

const (
	pwmDir    = "/sys/class/pwm/pwmchip0"
	pwmPeriod = 200000 // ns, as Xiaomi's factory scripts / como los guiones de fábrica de Xiaomi
	ledState  = "/data/mgl001/led.json"
)

type ledValue struct {
	On         bool
	Brightness float32
	R, G, B    float32
}

type Gateway struct {
	mu      sync.Mutex
	led     ledValue
	sensors map[uint32]float32
	zigbee  bool
	adverts float32
	saved   []byte      // LED state last written to flash
	saveT   *time.Timer // pending write (changes in a row are written once)
}

func NewGateway() *Gateway {
	g := &Gateway{led: ledValue{Brightness: 1, R: 1, G: 1, B: 1}, sensors: map[uint32]float32{}}
	if b, err := os.ReadFile(ledState); err == nil {
		_ = json.Unmarshal(b, &g.led)
		g.saved = b
	}
	return g
}

func diag() api.EntityCategory { return api.EntityCategory_ENTITY_CATEGORY_DIAGNOSTIC }

func (g *Gateway) List() []proto.Message {
	l := []proto.Message{
		&api.ListEntitiesLightResponse{ObjectId: "led", Key: keyLED, Name: "LED", Icon: "mdi:led-on",
			SupportedColorModes: []api.ColorMode{api.ColorMode_COLOR_MODE_RGB}},
		&api.ListEntitiesEventResponse{ObjectId: "button", Key: keyButton, Name: "Button", DeviceClass: "button",
			EventTypes: []string{"press", "double_press", "long_press"}},
		&api.ListEntitiesBinarySensorResponse{ObjectId: "zigbee_bridge", Key: keyZigbee, Name: "Zigbee bridge",
			DeviceClass: "connectivity", EntityCategory: diag()},
		&api.ListEntitiesSensorResponse{ObjectId: "uptime", Key: keyUptime, Name: "Uptime", UnitOfMeasurement: "s",
			DeviceClass: "duration", StateClass: api.SensorStateClass_STATE_CLASS_TOTAL_INCREASING, EntityCategory: diag()},
		&api.ListEntitiesSensorResponse{ObjectId: "memory_free", Key: keyMemory, Name: "Free memory", UnitOfMeasurement: "MiB",
			AccuracyDecimals: 1, Icon: "mdi:memory", StateClass: api.SensorStateClass_STATE_CLASS_MEASUREMENT, EntityCategory: diag()},
		&api.ListEntitiesSensorResponse{ObjectId: "load", Key: keyLoad, Name: "Load", AccuracyDecimals: 2, Icon: "mdi:cpu-32-bit",
			StateClass: api.SensorStateClass_STATE_CLASS_MEASUREMENT, EntityCategory: diag()},
		&api.ListEntitiesSensorResponse{ObjectId: "data_free", Key: keyDataFree, Name: "Free storage", UnitOfMeasurement: "MiB",
			AccuracyDecimals: 1, Icon: "mdi:harddisk", StateClass: api.SensorStateClass_STATE_CLASS_MEASUREMENT, EntityCategory: diag()},
		&api.ListEntitiesTextSensorResponse{ObjectId: "firmware", Key: keyFirmware, Name: "Firmware", Icon: "mdi:chip", EntityCategory: diag()},
		&api.ListEntitiesTextSensorResponse{ObjectId: "bank", Key: keyBank, Name: "Firmware bank", Icon: "mdi:swap-horizontal", EntityCategory: diag()},
		&api.ListEntitiesButtonResponse{ObjectId: "restart", Key: keyRestart, Name: "Restart", DeviceClass: "restart", EntityCategory: api.EntityCategory_ENTITY_CATEGORY_CONFIG},
	}
	if bt != nil {
		l = append(l, &api.ListEntitiesSensorResponse{ObjectId: "ble_adverts", Key: keyBTAdverts, Name: "Bluetooth advertisements",
			UnitOfMeasurement: "/min", Icon: "mdi:bluetooth-audio", StateClass: api.SensorStateClass_STATE_CLASS_MEASUREMENT, EntityCategory: diag()})
	}
	return l
}

func (g *Gateway) ledStateMsg() *api.LightStateResponse {
	return &api.LightStateResponse{Key: keyLED, State: g.led.On, Brightness: g.led.Brightness, ColorMode: api.ColorMode_COLOR_MODE_RGB,
		ColorBrightness: 1, Red: g.led.R, Green: g.led.G, Blue: g.led.B}
}

func (g *Gateway) States() []proto.Message {
	g.mu.Lock()
	defer g.mu.Unlock()
	l := []proto.Message{g.ledStateMsg(), &api.BinarySensorStateResponse{Key: keyZigbee, State: g.zigbee},
		&api.TextSensorStateResponse{Key: keyFirmware, State: firmwareName()},
		&api.TextSensorStateResponse{Key: keyBank, State: bankName()}}
	for k, v := range g.sensors {
		l = append(l, &api.SensorStateResponse{Key: k, State: v})
	}
	return l
}

func firmwareName() string {
	b, err := os.ReadFile("/etc/mgl001_fw_mod")
	if err != nil {
		return "stock"
	}
	return strings.TrimSpace(string(b))
}

func bankName() string {
	b, _ := os.ReadFile("/proc/cmdline")
	switch {
	case strings.Contains(string(b), "root=/dev/mtdblock7"):
		return "A (rootfs mtd7)"
	case strings.Contains(string(b), "root=/dev/mtdblock8"):
		return "B (rootfs_bak mtd8)"
	}
	return "?"
}

// ---- LED ----

func pwmWrite(ch int, file, val string) error {
	return os.WriteFile(fmt.Sprintf("%s/pwm%d/%s", pwmDir, ch, file), []byte(val), 0644)
}

func (g *Gateway) applyLED() {
	if *flagNoLED {
		return
	}
	if _, err := os.Stat(pwmDir); err != nil {
		return
	}
	for ch, v := range map[int]float32{1: g.led.R, 2: g.led.G, 3: g.led.B} {
		if _, err := os.Stat(fmt.Sprintf("%s/pwm%d", pwmDir, ch)); err != nil {
			_ = os.WriteFile(pwmDir+"/export", []byte(strconv.Itoa(ch)), 0200)
			_ = pwmWrite(ch, "period", strconv.Itoa(pwmPeriod))
			_ = pwmWrite(ch, "enable", "1")
		}
		duty := 0
		if g.led.On {
			duty = int(math.Round(float64(v*g.led.Brightness) * pwmPeriod))
		}
		if err := pwmWrite(ch, "duty_cycle", strconv.Itoa(duty)); err != nil {
			log.Printf("led: pwm%d: %v", ch, err)
		}
	}
	// Written to flash only when it changed, 5 s after the last change (a brightness slider sends many)
	// A la flash solo si cambió, 5 s después del último cambio (un deslizador de brillo manda muchos)
	if b, err := json.Marshal(g.led); err == nil && !bytes.Equal(b, g.saved) {
		if g.saveT == nil {
			g.saveT = time.AfterFunc(5*time.Second, g.saveLED)
		} else {
			g.saveT.Reset(5 * time.Second)
		}
	}
}

func (g *Gateway) LightCommand(r *api.LightCommandRequest) {
	if r.Key != keyLED {
		return
	}
	g.mu.Lock()
	if r.HasState {
		g.led.On = r.State
	}
	if r.HasBrightness {
		g.led.Brightness = r.Brightness
	}
	if r.HasRgb {
		g.led.R, g.led.G, g.led.B = r.Red, r.Green, r.Blue
	}
	g.applyLED()
	msg := g.ledStateMsg()
	g.mu.Unlock()
	broadcastState(msg)
}

func (g *Gateway) ButtonCommand(r *api.ButtonCommandRequest) {
	if r.Key == keyRestart {
		log.Printf("restart requested from Home Assistant")
		go func() {
			time.Sleep(time.Second)
			_ = exec.Command("reboot").Run()
		}()
	}
}

// ---- physical button: /dev/input/event0 (main-key) ----

func (g *Gateway) watchButton() {
	for {
		f, err := os.Open("/dev/input/event0")
		if err != nil {
			time.Sleep(30 * time.Second)
			continue
		}
		ev := make([]byte, 16) // struct input_event on 32-bit ARM
		var down time.Time
		var lastShort time.Time
		var pm sync.Mutex // guards pending: shared with the timer / protege pending, compartida con el temporizador
		pending := false
		var timer *time.Timer
		for {
			if _, err := f.Read(ev); err != nil {
				break
			}
			typ := binary.LittleEndian.Uint16(ev[8:10])
			val := int32(binary.LittleEndian.Uint32(ev[12:16]))
			if typ != 1 { // EV_KEY
				continue
			}
			now := time.Now()
			switch val {
			case 1:
				down = now
			case 0:
				if now.Sub(down) >= 1500*time.Millisecond {
					g.fire("long_press")
					continue
				}
				pm.Lock()
				if pending && now.Sub(lastShort) < 400*time.Millisecond {
					timer.Stop()
					pending = false
					pm.Unlock()
					g.fire("double_press")
					continue
				}
				pending, lastShort = true, now
				pm.Unlock()
				timer = time.AfterFunc(400*time.Millisecond, func() {
					pm.Lock()
					p := pending
					pending = false
					pm.Unlock()
					if p {
						g.fire("press")
					}
				})
			}
		}
		f.Close()
	}
}

func (g *Gateway) fire(t string) {
	log.Printf("button: %s", t)
	broadcastState(&api.EventResponse{Key: keyButton, EventType: t})
}

// ---- periodic sensors ----

func readFloat(path string, field int) float32 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if field >= len(f) {
		return 0
	}
	v, _ := strconv.ParseFloat(f[field], 32)
	return float32(v)
}

func memAvailableMiB() float32 {
	b, _ := os.ReadFile("/proc/meminfo")
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "MemAvailable:") {
			f := strings.Fields(l)
			v, _ := strconv.ParseFloat(f[1], 32)
			return float32(v / 1024)
		}
	}
	return 0
}

func dataFreeMiB() float32 {
	var s syscall.Statfs_t
	if syscall.Statfs("/data", &s) != nil {
		return 0
	}
	return float32(float64(s.Bavail) * float64(s.Bsize) / 1048576)
}

// A client (ZHA) connected to the Zigbee bridge port (state 01 = ESTABLISHED); false with the bridge off.
// Un cliente (ZHA) conectado al puerto del puente Zigbee; falso con el puente apagado.
func zigbeeConnected() bool {
	return *flagZbPort != 0 && tcpEstablished(*flagZbPort)
}

func tcpEstablished(port int) bool {
	suffix := fmt.Sprintf(":%04X", port)
	for _, p := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, _ := os.ReadFile(p)
		for _, l := range strings.Split(string(b), "\n")[1:] {
			f := strings.Fields(l)
			if len(f) > 3 && strings.HasSuffix(f[1], suffix) && f[3] == "01" {
				return true
			}
		}
	}
	return false
}

func round1(v float32) float32 { return float32(math.Round(float64(v)*10) / 10) }

func (g *Gateway) Run() {
	g.mu.Lock()
	g.applyLED()
	g.mu.Unlock()
	go g.watchButton()
	tick := 0
	for {
		g.mu.Lock()
		vals := map[uint32]float32{
			keyUptime:   float32(math.Floor(float64(readFloat("/proc/uptime", 0)))),
			keyMemory:   round1(memAvailableMiB()),
			keyLoad:     readFloat("/proc/loadavg", 0),
			keyDataFree: round1(dataFreeMiB()),
		}
		if bt != nil {
			vals[keyBTAdverts] = float32(bt.TakeAdvertCount())
		}
		zb := zigbeeConnected()
		changed := zb != g.zigbee || tick == 0
		g.zigbee = zb
		for k, v := range vals {
			g.sensors[k] = v
		}
		g.mu.Unlock()
		for k, v := range vals {
			broadcastState(&api.SensorStateResponse{Key: k, State: v})
		}
		if changed {
			broadcastState(&api.BinarySensorStateResponse{Key: keyZigbee, State: zb})
		}
		tick++
		// Zigbee is checked every 5 s, the rest every 60 s
		for i := 0; i < 12; i++ {
			time.Sleep(5 * time.Second)
			if z := zigbeeConnected(); z != g.zigbee {
				g.mu.Lock()
				g.zigbee = z
				g.mu.Unlock()
				broadcastState(&api.BinarySensorStateResponse{Key: keyZigbee, State: z})
			}
		}
	}
}

func (g *Gateway) saveLED() {
	g.mu.Lock()
	defer g.mu.Unlock()
	b, err := json.Marshal(g.led)
	if err != nil || bytes.Equal(b, g.saved) {
		return
	}
	if err := writeFileAtomic(ledState, b, 0644); err != nil {
		log.Printf("led: %v", err)
		return
	}
	g.saved = b
}
