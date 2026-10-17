// Firmware updates from Home Assistant (the native ESPHome update entity). The newest LAN build listed in
// the repository's firmware/list.txt is installed by the firmware's own installer into the bank that is
// not running, as a trial boot: the new firmware keeps itself only once the Zigbee bridge and Home
// Assistant are connected again, and otherwise reboots back to the previous one.
//
// Actualizaciones de firmware desde Home Assistant (la entidad de actualización nativa de ESPHome). La
// compilación LAN más nueva de firmware/list.txt la instala el instalador del propio firmware en el banco
// libre, como arranque de prueba: el firmware nuevo solo se queda cuando el puente Zigbee y Home Assistant
// vuelven a estar conectados; si no, reinicia y vuelve al anterior.
package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"

	"mgl001hass/api"
)

const (
	keyUpdate     = keyBTAdverts + 1
	listURL       = "https://raw.githubusercontent.com/Nebur692/mgl001_fw/main/firmware/list.txt"
	repoURL       = "https://github.com/Nebur692/mgl001_fw"
	installerPath = "/usr/sbin/mgl001_update.sh"
	bootTool      = "/usr/sbin/mgl001-boot"
	autoKeepPath  = "/data/mgl001/autokeep" // the firmware name a Home Assistant update is waiting to keep
	checkEvery    = 6 * time.Hour
	checkRetry    = 10 * time.Minute
	keepDeadline  = 10 * time.Minute
)

// mgl001_<stock version>_<stock build>_lan<YYYYMMDD>[.<n>]
var lanName = regexp.MustCompile(`^mgl001_\d+\.\d+\.\d+_\d+_lan(\d{4})(\d{2})(\d{2})(?:\.(\d+))?$`)

// lanVersion turns a LAN build name into the version Home Assistant compares: 2026.10.17 or 2026.10.17.2.
func lanVersion(name string) string {
	m := lanName.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	v := fmt.Sprintf("%s.%d.%d", m[1], mo, d)
	if m[4] != "" {
		v += "." + m[4]
	}
	return v
}

// The firmware's own name, the one a person reads. The build names (mgl001_<Xiaomi base>_lan<date>) stay
// as the technical id: the installer of every gateway already out there reads its base version from them.
// El nombre propio del firmware, el que lee una persona. Los nombres de compilación siguen como id técnico:
// el instalador de cada pasarela ya instalada lee de ellos su versión base.
const productName = "BurbuHub"

// displayName is how a firmware is shown: "BurbuHub 2026.10.17.5" for a LAN build, any other as it is.
func displayName(name string) string {
	if v := lanVersion(name); v != "" {
		return productName + " " + v
	}
	return name
}

// runningVersion is the running LAN build's version, or this program's when it runs on something else.
func runningVersion() string {
	if v := lanVersion(firmwareName()); v != "" {
		return v
	}
	return version
}

// versionNewer: a > b, comparing the dot-separated numbers.
func versionNewer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// newestLAN picks the newest LAN build from list.txt (name|kind|...).
func newestLAN(list string) string {
	best, bestV := "", ""
	for _, l := range strings.Split(list, "\n") {
		f := strings.Split(strings.TrimSpace(l), "|")
		if len(f) < 2 || f[1] != "custom" {
			continue
		}
		if v := lanVersion(f[0]); v != "" && (bestV == "" || versionNewer(v, bestV)) {
			best, bestV = f[0], v
		}
	}
	return best
}

type Updater struct {
	mu         sync.Mutex
	current    string // running firmware name (/etc/mgl001_fw_mod)
	latest     string // newest LAN build found, or "" if none/unknown
	inProgress bool
	progress   float32
	note       string // last check or install problem, shown as release notes
	checkNow   chan struct{}
}

func NewUpdater() *Updater {
	return &Updater{current: firmwareName(), checkNow: make(chan struct{}, 1)}
}

func (u *Updater) Entity() proto.Message {
	return &api.ListEntitiesUpdateResponse{ObjectId: "firmware_update", Key: keyUpdate, Name: "Firmware",
		DeviceClass: "firmware", EntityCategory: api.EntityCategory_ENTITY_CATEGORY_CONFIG}
}

func (u *Updater) State() *api.UpdateStateResponse {
	u.mu.Lock()
	defer u.mu.Unlock()
	cur := lanVersion(u.current)
	if cur == "" {
		cur = u.current // not a LAN build (stock or mod): shown as it is, never "newer"
	}
	latest := cur
	if v := lanVersion(u.latest); v != "" && lanVersion(u.current) != "" && versionNewer(v, cur) {
		latest = v
	}
	// The problem, if any, goes first (the update card shows the start of the notes)
	// El problema, si lo hay, va primero (la tarjeta de actualización enseña el principio de las notas)
	notes := ""
	if u.note != "" {
		notes = u.note + "\n\n"
	}
	notes += productName + ": ZHA bridge and native Home Assistant entities, no Xiaomi services."
	if u.latest != "" {
		notes += " Newest: " + displayName(u.latest)
	}
	return &api.UpdateStateResponse{Key: keyUpdate, CurrentVersion: cur, LatestVersion: latest, Title: productName,
		ReleaseSummary: notes, ReleaseUrl: repoURL, InProgress: u.inProgress, HasProgress: u.inProgress, Progress: u.progress}
}

func (u *Updater) publish() { broadcastState(u.State()) }

func (u *Updater) Command(r *api.UpdateCommandRequest) {
	if r.Key != keyUpdate {
		return
	}
	switch r.Command {
	case api.UpdateCommand_UPDATE_COMMAND_CHECK:
		select {
		case u.checkNow <- struct{}{}:
		default:
		}
	case api.UpdateCommand_UPDATE_COMMAND_UPDATE:
		u.mu.Lock()
		name, cur := u.latest, u.current
		busy := u.inProgress
		ok := !busy && lanVersion(cur) != "" && lanVersion(name) != "" && versionNewer(lanVersion(name), lanVersion(cur))
		if ok {
			u.inProgress, u.progress, u.note = true, 0, ""
		}
		u.mu.Unlock()
		if !ok {
			log.Printf("update: nothing to install (running %s, newest %q, busy %v)", cur, name, busy)
			return
		}
		u.publish()
		go u.install(name)
	}
}

// Run checks for updates now and then, and settles a pending trial boot started from Home Assistant.
func (u *Updater) Run() {
	go u.settleTrial()
	t := time.NewTimer(2 * time.Minute) // let DHCP and NTP settle first
	for {
		select {
		case <-t.C:
		case <-u.checkNow:
		}
		// After a failure (no DNS or no time yet, GitHub down) try again in 10 minutes, not 6 hours
		// Tras un fallo (sin DNS o sin hora aún, GitHub caído) se reintenta en 10 minutos, no en 6 horas
		if u.check() {
			t.Reset(checkEvery)
		} else {
			t.Reset(checkRetry)
		}
	}
}

func (u *Updater) check() bool {
	list, err := fetch(listURL)
	u.mu.Lock()
	if err != nil {
		u.note = "Could not check for updates: " + err.Error()
	} else {
		u.latest, u.note = newestLAN(list), ""
	}
	u.mu.Unlock()
	if err != nil {
		log.Printf("update: %v", err)
	}
	u.publish()
	return err == nil
}

func fetch(url string) (string, error) {
	c := &http.Client{Timeout: 30 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(b), err
}

// The installer's own progress lines, in order, and how far each one is.
var installSteps = []struct {
	prefix string
	pct    float32
}{
	{"* Getting firmware list", 2}, {"* Downloading", 5}, {"  SHA-256 OK", 30}, {"* Flashing kernel", 35},
	{"* Flashing rootfs", 45}, {"* Verifying", 80}, {"  Verified", 90}, {"* Trial boot", 95},
}

func (u *Updater) install(name string) {
	fail := func(msg string) {
		log.Printf("update: %s", msg)
		_ = os.Remove(autoKeepPath)
		u.mu.Lock()
		u.inProgress, u.progress, u.note = false, 0, "Last update failed: "+msg+". The running firmware was kept."
		u.mu.Unlock()
		u.publish()
	}
	// Another install still running (started before this program restarted): leave it and its file alone
	// Otra instalación aún en marcha (empezada antes de reiniciarse este programa): no se toca, ni su fichero
	if installerRunning() {
		log.Printf("update: another update is already running")
		u.mu.Lock()
		u.inProgress, u.progress, u.note = false, 0, "Another update is already running."
		u.mu.Unlock()
		u.publish()
		return
	}
	// Written before the trial boot: the new firmware reads it to keep itself once healthy
	if err := os.WriteFile(autoKeepPath, []byte(name+"\n"), 0600); err != nil {
		fail(err.Error())
		return
	}
	cmd := exec.Command("sh", installerPath, "--install", name, "--trial")
	out, err := cmd.StdoutPipe()
	if err != nil {
		fail(err.Error())
		return
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		fail(err.Error())
		return
	}
	last := ""
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		line := sc.Text()
		log.Printf("update: %s", line)
		if strings.HasPrefix(line, "! ") {
			last = strings.TrimPrefix(line, "! ")
		}
		for _, s := range installSteps {
			if strings.HasPrefix(line, s.prefix) {
				u.mu.Lock()
				u.progress = s.pct
				u.mu.Unlock()
				u.publish()
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		if last == "" {
			last = err.Error()
		}
		fail(last)
	}
	// On success the installer reboots into the trial; nothing else to do here
}

// settleTrial: on a trial boot started from Home Assistant, keep this firmware once the Zigbee bridge and
// Home Assistant are connected again; if they are not within keepDeadline, reboot back to the old bank.
func (u *Updater) settleTrial() {
	b, err := os.ReadFile(autoKeepPath)
	if err != nil {
		return
	}
	want := strings.TrimSpace(string(b))
	// The installer still writing (this program restarted mid-install): its trial boot needs the file
	// El instalador sigue escribiendo (este programa se reinició a mitad): su arranque de prueba necesita el fichero
	if installerRunning() {
		return
	}
	// "name=value" while a trial waits to be kept; an error when the variable does not exist
	if keep, err := exec.Command("/etc/fw_printenv", "mgl001_kcmd").Output(); err != nil || !strings.Contains(string(keep), "=") {
		_ = os.Remove(autoKeepPath) // no trial pending (already kept, or gone back)
		return
	}
	if want != u.current {
		log.Printf("update: trial boot of %s, but %s was expected; leaving it to boot back", u.current, want)
		_ = os.Remove(autoKeepPath)
		return
	}
	log.Printf("update: trial boot of %s; keeping it once Zigbee and Home Assistant are connected", u.current)
	// From the gateway's boot, not this program's start: a crash loop must not keep postponing it
	// Desde el arranque de la pasarela, no de este programa: un bucle de caídas no debe aplazarlo
	deadline := time.Now().Add(keepDeadline - uptime())
	for time.Now().Before(deadline) {
		if zigbeeConnected() && zigbeeAnswered() && haConnected() {
			out, err := exec.Command(bootTool, "keep").CombinedOutput()
			log.Printf("update: %s", strings.TrimSpace(string(out)))
			if err == nil {
				_ = os.Remove(autoKeepPath)
				return
			}
		}
		time.Sleep(10 * time.Second)
	}
	log.Printf("update: Zigbee or Home Assistant did not come back in %s; rebooting to the previous firmware", keepDeadline)
	_ = os.Remove(autoKeepPath)
	rebootGateway()
}

// haConnected: Home Assistant (or any API client) is subscribed to states.
func haConnected() bool {
	found := false
	srv.Each(func(c *Conn) {
		if c.States.Load() {
			found = true
		}
	})
	return found
}

var installLock = "/tmp/mgl001_update.lock" // held by mgl001_update.sh while it runs (a var for tests)

// installerRunning: mgl001_update.sh holds its lock (flock) from start to reboot.
func installerRunning() bool {
	f, err := os.Open(installLock)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil {
		return true
	}
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return false
}

// zigbeeAnswered: the Zigbee chip answered ZHA in this boot (the bridge left its firmware in RAM), which
// says more than a connected socket.
func zigbeeAnswered() bool {
	_, err := os.Stat(*flagZbInfo)
	return err == nil
}

// uptime since the gateway booted.
func uptime() time.Duration {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	s, _ := strconv.ParseFloat(strings.Fields(string(b) + " 0")[0], 64)
	return time.Duration(s * float64(time.Second))
}
