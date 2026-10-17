// What the gateway can tell about itself besides the basics: CPU temperature (SigmaStar's own sensor),
// why it last rebooted, the wired network and the time zone.
// Lo que la pasarela puede decir de sí misma además de lo básico: temperatura del procesador, motivo
// del último reinicio, la red por cable y la zona horaria.
package main

import (
	"os"
	"strconv"
	"strings"
)

// New keys after the first twelve, so the old ones never move / Claves nuevas tras las doce primeras
const (
	keyCPUTemp  = 13
	keyReboot   = 14
	keyEthLink  = 15
	keyEthSpeed = 16
	keyIP       = 17
	keyTimeZone = 18
)

var sysRoot = "" // only for tests / solo para pruebas

// cpuTemp reads "Temperature 48" from the SigmaStar msys driver.
func cpuTemp() (float32, bool) {
	b, err := os.ReadFile(sysRoot + "/sys/class/mstar/msys/TEMP_R")
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(f[len(f)-1], 32)
	if err != nil || v < -40 || v > 150 {
		return 0, false
	}
	return float32(v), true
}

// rebootReason reads "Reboot_Type: 2 (SW)" and says it in words; an unknown kind is shown as the kernel
// prints it.
func rebootReason() string {
	b, err := os.ReadFile(sysRoot + "/sys/class/mstar/msys/REBOOT_TYPE")
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	kind := s
	if i, j := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')'); i >= 0 && j > i {
		kind = strings.TrimSpace(s[i+1 : j])
	}
	switch k := strings.ToUpper(kind); {
	case k == "SW":
		return "Software"
	case strings.Contains(k, "WDT") || strings.Contains(k, "WATCHDOG"):
		return "Watchdog"
	case strings.Contains(k, "COLD") || strings.Contains(k, "POWER") || k == "HW":
		return "Power on"
	}
	return kind
}

func ethLink() bool {
	b, err := os.ReadFile(sysRoot + "/sys/class/net/eth0/carrier")
	return err == nil && strings.TrimSpace(string(b)) == "1"
}

// ethSpeed in Mbit/s; 0 when the link is down (the kernel says -1 then).
func ethSpeed() float32 {
	b, err := os.ReadFile(sysRoot + "/sys/class/net/eth0/speed")
	if err != nil {
		return 0
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || v < 0 {
		return 0
	}
	return float32(v)
}

func ipAddress() string {
	if ips := eth0IPs(); len(ips) > 0 {
		return ips[0]
	}
	return ""
}
