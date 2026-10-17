// mDNS: announces _esphomelib._tcp like an ESPHome device, and the Zigbee bridge as _zigbee-coordinator._tcp
// (radio_type, serial_number), so Home Assistant discovers both by itself: the device and the ZHA radio.
// mDNS: anuncia _esphomelib._tcp como un ESPHome y el puente Zigbee como _zigbee-coordinator._tcp, para que
// Home Assistant descubra solo las dos cosas: el dispositivo y la radio de ZHA.
package main

import (
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
)

func eth0IPs() []string {
	i, err := net.InterfaceByName("eth0")
	if err != nil {
		return nil
	}
	addrs, _ := i.Addrs()
	var ips []string
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			ips = append(ips, n.IP.String())
		}
	}
	return ips
}

// announce publishes the ESPHome API, and the Zigbee bridge when it runs (zigbeePort != 0).
func announce(name, mac string, port, zigbeePort int) {
	host, _ := os.Hostname()
	plainMAC := strings.ToLower(strings.ReplaceAll(mac, ":", ""))
	go announceService(name, name, "_esphomelib._tcp", port, []string{
		"friendly_name=" + productName + " (" + host + ")",
		"version=" + esphomeVersion,
		"mac=" + plainMAC,
		"platform=Linux",
		"board=mgl001",
		"network=ethernet",
		"api_encryption=Noise_NNpsk0_25519_ChaChaPoly_SHA256",
		"project_name=Burbujalandia." + productName,
		"project_version=" + runningVersion(),
	})
	if zigbeePort != 0 {
		// What ZHA reads from _zigbee-coordinator._tcp: radio_type and a stable serial_number (its unique id)
		// Lo que ZHA lee de _zigbee-coordinator._tcp: radio_type y un serial_number estable (su id único)
		go announceService(productName+" Zigbee ("+host+")", name, "_zigbee-coordinator._tcp", zigbeePort, []string{
			"radio_type=ezsp",
			"serial_number=mgl001-" + plainMAC,
			"name=" + productName + " Zigbee (" + host + ")",
			"manufacturer=Burbujalandia",
			"model=" + productName + " (Xiaomi Hub 2 mgl001)",
		})
	}
}

func announceService(instance, hostName, service string, port int, txt []string) {
	iface, _ := net.InterfaceByName("eth0")
	for {
		// Wait for DHCP; re-announce if the address changes / espera al DHCP y se reanuncia si cambia
		ips := eth0IPs()
		if len(ips) == 0 || iface == nil {
			time.Sleep(5 * time.Second)
			iface, _ = net.InterfaceByName("eth0")
			continue
		}
		s, err := zeroconf.RegisterProxy(instance, service, "local.", port, hostName, ips, txt, []net.Interface{*iface})
		if err != nil {
			log.Printf("mdns: %v", err)
			time.Sleep(30 * time.Second)
			continue
		}
		log.Printf("mdns: %s (%s) at %v:%d", instance, service, ips, port)
		for {
			time.Sleep(30 * time.Second)
			if strings.Join(eth0IPs(), ",") != strings.Join(ips, ",") {
				break
			}
		}
		s.Shutdown()
	}
}
