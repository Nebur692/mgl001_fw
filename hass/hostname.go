// The gateway's names. Two different things:
//   - the host name (what the router and the network see), BurbuHub-XXXX by default, editable from Home
//     Assistant; kept in /data/mgl001/hostname and set at boot by mgl001_lan_init.sh;
//   - the ESPHome device name, which is how Home Assistant knows the gateway: it checks it on every
//     encrypted connection and asks to set the device up again if it changes. It is fixed the first time
//     and kept in /data/mgl001/node_name, so changing the host name never touches it.
//
// Los nombres de la pasarela. El nombre de host (el que ven el router y la red), editable desde Home
// Assistant; y el nombre ESPHome, con el que Home Assistant reconoce la pasarela: lo comprueba en cada
// conexión cifrada y pide configurarla de nuevo si cambia. Se fija la primera vez y nunca sigue al host.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"

	"mgl001hass/api"
)

const keyHostname = 23

// RFC 1123 label: letters, digits and hyphens, 1-63, no hyphen at either end.
var hostLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// sanitizeNodeName makes an ESPHome device name: lower case letters, digits and hyphens.
func sanitizeNodeName(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
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

// deviceName is the ESPHome device name: the saved one, or (first start) one made from the host name and
// saved, so it never changes again.
func deviceName() string {
	if b, err := os.ReadFile(*flagNodeFile); err == nil {
		if n := strings.TrimSpace(string(b)); n != "" && sanitizeNodeName(n) == n {
			return n
		}
	}
	h, _ := os.Hostname()
	n := sanitizeNodeName(h)
	if err := writeFileAtomic(*flagNodeFile, []byte(n+"\n"), 0644); err != nil {
		log.Printf("name: %v", err)
	}
	return n
}

func hostnameEntity() proto.Message {
	return &api.ListEntitiesTextResponse{ObjectId: "hostname", Key: keyHostname, Name: "Hostname", Icon: "mdi:dns",
		EntityCategory: api.EntityCategory_ENTITY_CATEGORY_CONFIG, MinLength: 1, MaxLength: 63,
		Pattern: `[A-Za-z0-9]([A-Za-z0-9\-]{0,61}[A-Za-z0-9])?`, Mode: api.TextMode_TEXT_MODE_TEXT}
}

// hostnameState is the running host name (in tests with -hostname-dry, the saved one).
func hostnameState() *api.TextStateResponse {
	if *flagHostDry {
		if b, err := os.ReadFile(*flagHostFile); err == nil {
			return &api.TextStateResponse{Key: keyHostname, State: strings.TrimSpace(string(b))}
		}
	}
	h, _ := os.Hostname()
	return &api.TextStateResponse{Key: keyHostname, State: h, MissingState: h == ""} // an empty text breaks HA's min length
}

// setHostname saves a new host name and applies it now. The router sees it from the next reboot: DHCP
// keeps the name it started with, and restarting DHCP now would drop the address for a moment, and with
// it the Zigbee connection: not worth it for a name.
// Lo guarda y lo aplica ya. El router lo ve desde el siguiente reinicio: el DHCP sigue con el nombre con
// el que arrancó, y reiniciarlo ahora quitaría la dirección un momento, y con ella el Zigbee.
func setHostname(v string) error {
	v = strings.TrimSpace(v)
	if !hostLabel.MatchString(v) {
		return fmt.Errorf("%q is not a valid host name (letters, digits and hyphens, up to 63)", v)
	}
	if err := writeFileAtomic(*flagHostFile, []byte(v+"\n"), 0644); err != nil {
		return err
	}
	if *flagHostDry {
		return nil
	}
	return applyHostname(v)
}

func hostnameCommand(r *api.TextCommandRequest) {
	if r.Key != keyHostname {
		return
	}
	if err := setHostname(r.State); err != nil {
		log.Printf("hostname: %v", err)
	} else {
		log.Printf("hostname: %s", strings.TrimSpace(r.State))
	}
	broadcastState(hostnameState()) // the new name, or the old one back if it was refused
}

func applyHostname(v string) error { return unix.Sethostname([]byte(v)) }

// friendlyName is the device name Home Assistant shows. On a real ESPHome device it is fixed config, and
// Home Assistant renames the device whenever it changes, so it does not follow the host name either:
// the host name at the first start, kept next to the ESPHome name.
// El nombre que enseña Home Assistant: en ESPHome es fijo y HA renombra el dispositivo si cambia, así que
// tampoco sigue al nombre de host: el del primer arranque, guardado junto al nombre ESPHome.
func friendlyName() string {
	f := filepath.Join(filepath.Dir(*flagNodeFile), "friendly_name")
	if b, err := os.ReadFile(f); err == nil {
		if n := strings.TrimSpace(string(b)); n != "" {
			return n
		}
	}
	h, _ := os.Hostname()
	if h == "" {
		return productName
	}
	if err := writeFileAtomic(f, []byte(h+"\n"), 0644); err != nil {
		log.Printf("name: %v", err)
	}
	return h
}
