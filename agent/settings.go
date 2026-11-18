package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Device settings for the native Settings app. Each setting uses the same
// file or command as the CardputerZero launcher's own Settings (see
// docs/cardputer-settings.md), so both stay in agreement.

const (
	backlightDir = "/sys/class/backlight/backlight"
	batteryDir   = "/sys/class/power_supply/bq27220-0"
	ext5vLed     = "/sys/class/leds/ext_5v_out/brightness"
	grove5vLed   = "/sys/class/leds/grove_5v_out/brightness"
)

var (
	launcherConfigMu sync.Mutex
	timeoutChoices   = map[int]bool{0: true, 10: true, 30: true, 60: true, 300: true}
	manualTimeRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}(:\d{2})?$`)
)

func launcherConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "cardputerzero", "config.json")
}

func readLauncherConfig() map[string]any {
	m := map[string]any{}
	if b, err := os.ReadFile(launcherConfigPath()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// setLauncherConfig replaces one top-level key and keeps everything else,
// written via a temp file + rename like the launcher does.
func setLauncherConfig(key string, v any) error {
	launcherConfigMu.Lock()
	defer launcherConfigMu.Unlock()
	m := readLauncherConfig()
	m[key] = v
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	p := launcherConfigPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".zerodesk.tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func configInt(m map[string]any, key string, def int) int {
	if f, ok := m[key].(float64); ok {
		return int(f)
	}
	return def
}

func run(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("XDG_RUNTIME_DIR=/run/user/%d", os.Getuid()), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		if s == "" {
			s = err.Error()
		}
		return s, errors.New(s)
	}
	return s, nil
}

func readInt(p string) (int, bool) {
	v, err := strconv.Atoi(readTrim(p))
	return v, err == nil
}

// ---- reading ----

func brightnessPercent(cfg map[string]any) (pct int, screenOn bool) {
	maxV, ok := readInt(backlightDir + "/max_brightness")
	cur, ok2 := readInt(backlightDir + "/brightness")
	if ok && ok2 && maxV > 0 && cur > 0 {
		return (cur*100 + maxV/2) / maxV, true
	}
	return configInt(cfg, "brightness", 100), false
}

func soundState() (vol int, muted bool) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if out, err := run(3*time.Second, "pactl", "get-sink-mute", "@DEFAULT_SINK@"); err == nil {
			muted = strings.Contains(out, "yes")
		}
	}()
	if out, err := run(3*time.Second, "pactl", "get-sink-volume", "@DEFAULT_SINK@"); err == nil {
		if i := strings.Index(out, "%"); i > 0 {
			j := strings.LastIndexAny(out[:i], " /") + 1
			vol, _ = strconv.Atoi(strings.TrimSpace(out[j:i]))
		}
	}
	wg.Wait()
	return
}

type netDevice struct {
	Device     string `json:"device"`
	Type       string `json:"type"`
	State      string `json:"state"`
	Connection string `json:"connection"`
	IP         string `json:"ip,omitempty"`
}

func netDevices() []netDevice {
	out, err := run(4*time.Second, "nmcli", "-t", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device")
	if err != nil {
		return nil
	}
	var list []netDevice
	for _, l := range strings.Split(out, "\n") {
		f := splitNmcli(l)
		if len(f) < 4 || (f[1] != "wifi" && f[1] != "ethernet") {
			continue
		}
		d := netDevice{Device: f[0], Type: f[1], State: f[2], Connection: f[3]}
		d.IP = ipv4Of(d.Device) // from the kernel: no nmcli call per device

		list = append(list, d)
	}
	return list
}

// ipv4Of returns the interface's first IPv4 address.
func ipv4Of(name string) string {
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return ""
	}
	addrs, _ := ifc.Addrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			return ipn.IP.String()
		}
	}
	return ""
}

// splitNmcli splits one line of `nmcli -t` output on unescaped colons.
func splitNmcli(line string) []string {
	var out []string
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '\\' && i+1 < len(line) {
			b.WriteByte(line[i+1])
			i++
			continue
		}
		if c == ':' {
			out = append(out, b.String())
			b.Reset()
			continue
		}
		b.WriteByte(c)
	}
	return append(out, b.String())
}

func batteryInfo() map[string]any {
	if _, err := os.Stat(batteryDir); err != nil {
		return nil
	}
	b := map[string]any{"status": readTrim(batteryDir + "/status"), "health": readTrim(batteryDir + "/health")}
	for _, k := range []string{"capacity", "cycle_count", "temp", "voltage_now", "current_now", "charge_full", "charge_full_design", "time_to_empty_now", "time_to_full_now"} {
		if v, ok := readInt(batteryDir + "/" + k); ok {
			b[k] = v
		}
	}
	return b
}

func handleSettingsGet(w http.ResponseWriter, _ *http.Request) {
	// The commands below are independent; run them at once so a cold read
	// costs the slowest one, not their sum (window first paint).
	var (
		wg            sync.WaitGroup
		cfg           map[string]any
		vol           int
		muted, wifiOn bool
		timeOut       string
		timeErr       error
		units         string
		network       any
		battery       any
		ext, grove    int
	)
	par := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }
	par(func() { cfg = readLauncherConfig() })
	par(func() { vol, muted = soundState() })
	par(func() {
		if out, err := run(3*time.Second, "nmcli", "radio", "wifi"); err == nil {
			wifiOn = out == "enabled"
		}
	})
	par(func() {
		timeOut, timeErr = run(3*time.Second, "timedatectl", "show", "-p", "NTP", "-p", "NTPSynchronized", "-p", "Timezone")
	})
	// one call for both units; prints one state per line, exits non-zero if any is inactive
	par(func() {
		units, _ = run(3*time.Second, "systemctl", "is-active", "systemd-timesyncd.service", "adbd.service")
	})
	par(func() { network = netDevices() })
	par(func() { battery = batteryInfo() })
	par(func() { ext, _ = readInt(ext5vLed); grove, _ = readInt(grove5vLed) })
	wg.Wait()

	pct, on := brightnessPercent(cfg)
	t := map[string]any{"now": time.Now().Format(time.RFC3339)}
	if timeErr == nil {
		for _, l := range strings.Split(timeOut, "\n") {
			k, v, _ := strings.Cut(l, "=")
			switch k {
			case "NTPSynchronized":
				t["synced"] = v == "yes"
			case "Timezone":
				t["timezone"] = v
			}
		}
	}
	state := strings.Split(units, "\n")
	// the switch is the time sync service itself (see handleSettingsSet "ntp")
	t["ntp"] = len(state) > 0 && strings.TrimSpace(state[0]) == "active"
	adb := len(state) > 1 && strings.TrimSpace(state[1]) == "active"
	host, _ := os.Hostname()
	writeJSON(w, http.StatusOK, map[string]any{
		"hostname": host,
		"display":  map[string]any{"brightness": pct, "screen_on": on, "timeout": configInt(cfg, "dark_time", 30)},
		"sound":    map[string]any{"volume": vol, "muted": muted},
		"wifi":     map[string]any{"enabled": wifiOn},
		"network":  network,
		"ports":    map[string]any{"ext5v": ext > 0, "grove5v": grove > 0},
		"battery":  battery,
		"time":     t,
		"adb":      adb,
	})
}

// ---- writing ----

func handleSettingsSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key      string          `json:"key"`
		Value    json.RawMessage `json:"value"`
		Password string          `json:"password"` // administrator actions (privileged.go)
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	var num float64
	var flag bool
	var str string
	numErr := json.Unmarshal(body.Value, &num)
	flagErr := json.Unmarshal(body.Value, &flag)
	_ = json.Unmarshal(body.Value, &str)
	bad := func(msg string) { writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg}) }
	onOff := map[bool]string{true: "on", false: "off"}

	var err error
	switch body.Key {
	case "brightness":
		if numErr != nil || num < 10 || num > 100 {
			bad("brightness must be 10–100")
			return
		}
		pct := int(num)
		if maxV, ok := readInt(backlightDir + "/max_brightness"); ok {
			// a screen that is off for the timeout stays off; the value applies when it wakes
			if cur, _ := readInt(backlightDir + "/brightness"); cur > 0 {
				err = os.WriteFile(backlightDir+"/brightness", []byte(strconv.Itoa(maxV*pct/100)), 0o644)
			}
		}
		if err == nil {
			err = setLauncherConfig("brightness", pct)
		}
	case "timeout":
		if numErr != nil || !timeoutChoices[int(num)] {
			bad("timeout must be 0, 10, 30, 60 or 300")
			return
		}
		err = setLauncherConfig("dark_time", int(num))
	case "volume":
		if numErr != nil || num < 0 || num > 100 {
			bad("volume must be 0–100")
			return
		}
		if _, err = run(3*time.Second, "pactl", "set-sink-volume", "@DEFAULT_SINK@", fmt.Sprintf("%d%%", int(num))); err == nil {
			err = setLauncherConfig("volume", int(num))
		}
	case "mute":
		if flagErr != nil {
			bad("mute must be true or false")
			return
		}
		_, err = run(3*time.Second, "pactl", "set-sink-mute", "@DEFAULT_SINK@", map[bool]string{true: "1", false: "0"}[flag])
	case "wifi":
		if flagErr != nil {
			bad("wifi must be true or false")
			return
		}
		_, err = run(10*time.Second, "nmcli", "radio", "wifi", onOff[flag])
	case "ext5v", "grove5v":
		if flagErr != nil {
			bad(body.Key + " must be true or false")
			return
		}
		p := map[string]string{"ext5v": ext5vLed, "grove5v": grove5vLed}[body.Key]
		err = os.WriteFile(p, []byte(map[bool]string{true: "1", false: "0"}[flag]), 0o644)
	case "ntp":
		if flagErr != nil {
			bad("ntp must be true or false")
			return
		}
		if sudoWithoutPassword(r.Context()) {
			_, err = run(10*time.Second, "sudo", "-n", "timedatectl", "set-ntp", strconv.FormatBool(flag))
		} else {
			// polkit lets any user start and stop the time sync service (Raspberry Pi OS rules)
			_, err = run(10*time.Second, "systemctl", map[bool]string{true: "start", false: "stop"}[flag], "systemd-timesyncd.service")
		}
	case "time":
		if !manualTimeRe.MatchString(str) {
			bad("time must be YYYY-MM-DD HH:MM[:SS]")
			return
		}
		err = withAdmin(r.Context(), body.Password, func() error {
			if _, err := run(10*time.Second, "sudo", "-n", "date", "-s", str); err != nil {
				return err
			}
			_, err := run(10*time.Second, "sudo", "-n", "hwclock", "-w")
			return err
		})
	case "adb":
		if flagErr != nil {
			bad("adb must be true or false")
			return
		}
		err = withAdmin(r.Context(), body.Password, func() error {
			_, err := run(30*time.Second, "sudo", "-n", "/usr/share/APPLaunch/adb/cardputer-adb", "set", map[bool]string{true: "1", false: "0"}[flag])
			return err
		})
	default:
		bad("unknown setting")
		return
	}
	if writeAdminError(w, err, nil) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- Wi-Fi ----

type wifiNetwork struct {
	SSID     string `json:"ssid"`
	Signal   int    `json:"signal"`
	Security string `json:"security"`
	InUse    bool   `json:"in_use"`
	Known    bool   `json:"known"`
}

func handleWifiList(w http.ResponseWriter, r *http.Request) {
	rescan := "auto"
	if r.URL.Query().Get("rescan") == "1" {
		rescan = "yes"
	}
	out, err := run(20*time.Second, "nmcli", "-t", "-f", "IN-USE,SSID,SIGNAL,SECURITY", "device", "wifi", "list", "--rescan", rescan)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	known := map[string]bool{}
	if cons, err := run(4*time.Second, "nmcli", "-t", "-f", "NAME,TYPE", "connection", "show"); err == nil {
		for _, l := range strings.Split(cons, "\n") {
			if f := splitNmcli(l); len(f) == 2 && f[1] == "802-11-wireless" {
				known[f[0]] = true
			}
		}
	}
	// one entry per SSID: the strongest access point, marked in use if any is
	best := map[string]*wifiNetwork{}
	var order []string
	for _, l := range strings.Split(out, "\n") {
		f := splitNmcli(l)
		if len(f) < 4 || f[1] == "" {
			continue
		}
		sig, _ := strconv.Atoi(f[2])
		n := best[f[1]]
		if n == nil {
			n = &wifiNetwork{SSID: f[1], Known: known[f[1]]}
			best[f[1]] = n
			order = append(order, f[1])
		}
		if sig > n.Signal {
			n.Signal, n.Security = sig, f[3]
		}
		n.InUse = n.InUse || f[0] == "*"
	}
	list := make([]wifiNetwork, 0, len(order))
	for _, s := range order {
		list = append(list, *best[s])
	}
	writeJSON(w, http.StatusOK, list)
}

func handleWifiConnect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SSID     string `json:"ssid"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.SSID == "" || len(body.SSID) > 64 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	args := []string{"--wait", "25", "device", "wifi", "connect", body.SSID}
	if body.Password != "" {
		args = append(args, "password", body.Password)
	} else if out, err := run(4*time.Second, "nmcli", "-t", "-f", "NAME", "connection", "show"); err == nil && strings.Contains("\n"+out+"\n", "\n"+body.SSID+"\n") {
		// a saved network: bring its profile up
		args = []string{"--wait", "25", "connection", "up", "id", body.SSID}
	}
	if _, err := run(30*time.Second, "nmcli", args...); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func handleWifiDisconnect(w http.ResponseWriter, _ *http.Request) {
	for _, d := range netDevices() {
		if d.Type == "wifi" && d.State == "connected" {
			if _, err := run(10*time.Second, "nmcli", "device", "disconnect", d.Device); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func handleReboot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	go func() {
		time.Sleep(500 * time.Millisecond)
		_, _ = run(10*time.Second, "systemctl", "reboot", "-i")
	}()
}
