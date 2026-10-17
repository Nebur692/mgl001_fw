// Time zone from Home Assistant: when Home Assistant subscribes, the gateway asks it for the time
// (GetTimeRequest). The answer carries Home Assistant's own time zone as a POSIX rule (e.g.
// CET-1CEST,M3.5.0,M10.5.0/3), turned here into a TZif file for glibc and kept in /data, so the whole
// system follows the zone set in Home Assistant, from boot on. The answer's clock is also used when the
// gateway has no time yet (no NTP).
//
// Zona horaria desde Home Assistant: al suscribirse HA, la pasarela le pide la hora. La respuesta trae la
// zona horaria de HA como regla POSIX, que aquí se convierte en un fichero TZif para glibc y se guarda en
// /data: todo el sistema sigue la zona de HA desde el arranque. Su hora se usa si aún no hay NTP.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type TimeZone struct {
	file string // TZif kept across boots / TZif que se conserva entre arranques
	link string // what /etc/localtime points to / a donde apunta /etc/localtime

	mu    sync.Mutex
	posix string
	loc   *time.Location
}

// NewTimeZone loads the zone saved by an earlier answer, if any.
func NewTimeZone(file, link string) *TimeZone {
	t := &TimeZone{file: file, link: link}
	if b, err := os.ReadFile(file); err == nil {
		if loc, err := time.LoadLocationFromTZData("Local", b); err == nil {
			t.loc, t.posix = loc, tzifFooter(b)
		}
	}
	return t
}

// Apply takes Home Assistant's answer: its POSIX rule and its clock.
func (t *TimeZone) Apply(posix string, epoch uint32) {
	// No NTP yet (the gateway has no RTC and starts in 1970): Home Assistant's clock will do
	// Sin NTP todavía (la pasarela no tiene reloj propio y arranca en 1970): sirve el de Home Assistant
	if epoch > 0 && time.Now().Year() < 2024 {
		tv := unix.NsecToTimeval(int64(epoch) * int64(time.Second))
		if err := unix.Settimeofday(&tv); err != nil {
			log.Printf("time: %v", err)
		} else {
			log.Printf("time: clock set from Home Assistant")
		}
	}
	if posix == "" {
		return
	}
	data, err := tzifFromPOSIX(posix)
	if err == nil {
		_, err = time.LoadLocationFromTZData("Local", data)
	}
	if err != nil {
		log.Printf("time zone %q from Home Assistant: %v", posix, err)
		return
	}
	loc, _ := time.LoadLocationFromTZData("Local", data)
	t.mu.Lock()
	same := t.posix == posix
	t.posix, t.loc = posix, loc
	t.mu.Unlock()
	if old, err := os.ReadFile(t.file); err != nil || !bytes.Equal(old, data) {
		if err := writeFileAtomic(t.file, data, 0644); err != nil {
			log.Printf("time zone: %v", err)
			return
		}
	}
	if target, rerr := os.Readlink(t.link); rerr != nil || target != t.file {
		// Point the link at the saved zone, replacing it in one step (rename)
		// Apuntar el enlace a la zona guardada, cambiándolo de una vez (rename)
		tmp := t.link + ".new"
		_ = os.Remove(tmp)
		lerr := os.Symlink(t.file, tmp)
		if lerr == nil {
			lerr = os.Rename(tmp, t.link)
		}
		if lerr != nil {
			log.Printf("time zone: %v", lerr)
		}
	}
	if !same {
		log.Printf("time zone from Home Assistant: %s", posix)
	}
}

// Text is what Home Assistant shows: the zone's abbreviation now and its offset, e.g. "CEST (UTC+02:00)".
func (t *TimeZone) Text() string {
	t.mu.Lock()
	loc := t.loc
	t.mu.Unlock()
	if loc == nil {
		return ""
	}
	now := time.Now().In(loc)
	return fmt.Sprintf("%s (UTC%s)", now.Format("MST"), now.Format("-07:00"))
}

// tzifFromPOSIX builds a TZif v2 file whose rule is the POSIX string: one type (standard time) and one
// transition at 1970, so glibc and Go use the footer rule for every date after it (DST included).
func tzifFromPOSIX(posix string) ([]byte, error) {
	if strings.ContainsAny(posix, "\n\x00") {
		return nil, fmt.Errorf("bad characters")
	}
	name, off, err := posixStd(posix)
	if err != nil {
		return nil, err
	}
	abbr := append([]byte(name), 0)
	var b bytes.Buffer
	header := func(timecnt int) {
		b.WriteString("TZif2")
		b.Write(make([]byte, 15))
		for _, n := range []int{0, 0, 0, timecnt, 1, len(abbr)} { // isut, isstd, leap, time, type, char
			_ = binary.Write(&b, binary.BigEndian, uint32(n))
		}
	}
	ttinfo := func() {
		_ = binary.Write(&b, binary.BigEndian, int32(off))
		b.WriteByte(0) // isdst
		b.WriteByte(0) // abbreviation index
		b.Write(abbr)
	}
	header(0) // v1 block: no transitions, just the type
	ttinfo()
	header(1) // v2 block
	_ = binary.Write(&b, binary.BigEndian, int64(0))
	b.WriteByte(0)
	ttinfo()
	b.WriteString("\n" + posix + "\n")
	return b.Bytes(), nil
}

// posixStd reads the standard-time part of a POSIX TZ rule: its name and its UTC offset in seconds
// (POSIX writes it the other way round: "CET-1" is UTC+1).
func posixStd(s string) (string, int, error) {
	var name string
	switch {
	case strings.HasPrefix(s, "<"):
		end := strings.IndexByte(s, '>')
		if end < 2 {
			return "", 0, fmt.Errorf("bad name in %q", s)
		}
		name, s = s[1:end], s[end+1:]
	default:
		i := 0
		for i < len(s) && (s[i] >= 'A' && s[i] <= 'Z' || s[i] >= 'a' && s[i] <= 'z') {
			i++
		}
		if i < 3 {
			return "", 0, fmt.Errorf("bad name in %q", s)
		}
		name, s = s[:i], s[i:]
	}
	sign := 1
	if s != "" && (s[0] == '+' || s[0] == '-') {
		if s[0] == '-' {
			sign = -1
		}
		s = s[1:]
	}
	end := 0
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || s[end] == ':') {
		end++
	}
	if end == 0 {
		return "", 0, fmt.Errorf("no offset in rule")
	}
	secs, mult := 0, 3600
	for _, p := range strings.Split(s[:end], ":") {
		n, err := strconv.Atoi(p)
		if err != nil || mult == 0 {
			return "", 0, fmt.Errorf("bad offset in rule")
		}
		secs += n * mult
		mult /= 60
	}
	return name, -sign * secs, nil
}

// tzifFooter returns the POSIX rule at the end of a TZif v2+ file.
func tzifFooter(b []byte) string {
	s := strings.TrimRight(string(b), "\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return ""
}
