package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	appLaunchDir   = "/usr/share/APPLaunch"
	launcherUnit   = "APPLaunch.service"
	launcherBinary = "M5CardputerZero-APPLaunch"
)

type desktopApp struct {
	ID       string `json:"id"` // .desktop file name without extension
	Name     string `json:"name"`
	Comment  string `json:"comment,omitempty"`
	Exec     string `json:"-"`
	Icon     string `json:"-"`
	Terminal bool   `json:"terminal"`
	HasIcon  bool   `json:"hasIcon"`
	// freedesktop Categories= (e.g. "Utility;AudioVideo"), often empty on this device
	Categories []string `json:"categories,omitempty"`
	// cacheable icon URL (changes with the file) and how to draw it (icons.go)
	IconURL    string `json:"icon,omitempty"`
	IconOpaque bool   `json:"iconOpaque"`
	// X-ZeroDesk-Web: directory with a ZeroDesk web UI (docs/APP-SPEC.md)
	WebDir string `json:"-"`
}

// appManager starts apps from the web the same way the launcher does:
// exactly one program owns the LCD at a time. While a web-started app runs,
// the launcher service is stopped; when the app exits, it is started again.
type appManager struct {
	// keys reach the launcher, whose own force-quit (hold ESC) ends apps
	// that run with more rights than ZeroDesk has
	keys    *keyInjector
	mu      sync.Mutex
	running *exec.Cmd
	runID   string
}

func newAppManager() *appManager { return &appManager{} }

func parseDesktop(path string) (desktopApp, bool) {
	f, err := os.Open(path)
	if err != nil {
		return desktopApp{}, false
	}
	defer f.Close()
	a := desktopApp{ID: strings.TrimSuffix(filepath.Base(path), ".desktop")}
	inEntry := false
	noDisplay := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inEntry = line == "[Desktop Entry]"
			continue
		}
		if !inEntry || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Name":
			a.Name = strings.TrimSpace(v)
		case "Comment":
			a.Comment = strings.TrimSpace(v)
		case "Exec":
			a.Exec = strings.TrimSpace(v)
		case "Icon":
			a.Icon = strings.TrimSpace(v)
		case "Terminal":
			a.Terminal = strings.EqualFold(strings.TrimSpace(v), "true")
		case "Categories":
			for _, c := range strings.Split(v, ";") {
				if c = strings.TrimSpace(c); c != "" {
					a.Categories = append(a.Categories, c)
				}
			}
		case "X-ZeroDesk-Web":
			if v = strings.TrimSpace(v); filepath.IsAbs(v) {
				a.WebDir = v
			}
		case "NoDisplay":
			noDisplay = strings.EqualFold(strings.TrimSpace(v), "true")
		}
	}
	if a.Name == "" || a.Exec == "" || noDisplay {
		return desktopApp{}, false
	}
	if p := a.iconPath(); p != "" {
		if info, ok := deviceIconInfo(p); ok {
			a.HasIcon = true
			a.IconURL = "/api/apps/icon?id=" + url.QueryEscape(a.ID) + "&v=" + info.version
			a.IconOpaque = info.opaque
		}
	}
	return a, true
}

// iconPath resolves Icon= the way the launcher does (cp0_lvgl resolve_path):
// absolute paths are used as is, everything else lives under
// /usr/share/APPLaunch/share/images/ ("share/images/" prefix optional).
func (a desktopApp) iconPath() string {
	if a.Icon == "" || strings.Contains(a.Icon, "..") {
		return ""
	}
	var p string
	switch {
	case filepath.IsAbs(a.Icon):
		p = a.Icon
	case strings.HasPrefix(a.Icon, "share/images/"):
		p = filepath.Join(appLaunchDir, a.Icon)
	default:
		p = filepath.Join(appLaunchDir, "share", "images", a.Icon)
	}
	p = filepath.Clean(p)
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p
	}
	return ""
}

func loadApps() []desktopApp {
	files, _ := filepath.Glob(filepath.Join(appLaunchDir, "applications", "*.desktop"))
	apps := make([]desktopApp, 0, len(files))
	for _, f := range files {
		// ZeroDesk's own launcher entry (zerodesk-screen) is not an app to open from ZeroDesk
		if a, ok := parseDesktop(f); ok && a.ID != "zerodesk" {
			apps = append(apps, a)
		}
	}
	sort.Slice(apps, func(i, j int) bool { return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name) })
	return apps
}

func findApp(id string) (desktopApp, bool) {
	for _, a := range loadApps() {
		if a.ID == id {
			return a, true
		}
	}
	return desktopApp{}, false
}

func (m *appManager) handleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, loadApps())
}

func (m *appManager) handleIcon(w http.ResponseWriter, r *http.Request) {
	a, ok := findApp(r.URL.Query().Get("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	p := a.iconPath()
	if p == "" {
		http.NotFound(w, r)
		return
	}
	info, _ := deviceIconInfo(p)
	tag := `"` + info.version + `"`
	if r.URL.Query().Get("v") == info.version {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.Header().Set("ETag", tag)
	if etagMatches(r, tag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	http.ServeFile(w, r, p)
}

// --- foreground detection ---------------------------------------------------

type procInfo struct {
	pid, ppid, pgid int
	comm, cmdline   string
	start           uint64
}

func readProc(pid int) (procInfo, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return procInfo{}, false
	}
	s := string(b)
	l, r := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if l < 0 || r < 0 {
		return procInfo{}, false
	}
	fields := strings.Fields(s[r+2:])
	if len(fields) < 20 || fields[0] == "Z" {
		return procInfo{}, false
	}
	p := procInfo{pid: pid, comm: s[l+1 : r]}
	p.ppid, _ = strconv.Atoi(fields[1])
	p.pgid, _ = strconv.Atoi(fields[2])
	p.start, _ = strconv.ParseUint(fields[19], 10, 64)
	if c, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		p.cmdline = strings.TrimSpace(strings.ReplaceAll(string(c), "\x00", " "))
	}
	return p, true
}

func allProcs() map[int]procInfo {
	out := map[int]procInfo{}
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if p, ok := readProc(pid); ok {
			out[pid] = p
		}
	}
	return out
}

// heldDevices reports whether pid has the framebuffer and an evdev input
// node open. Helpers spawned by an app (nmcli, sh -c wrappers) inherit the
// framebuffer fd but never read the keyboard, so "both" marks the real app.
func heldDevices(pid int) (fb, input bool) {
	fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		return false, false
	}
	for _, fd := range fds {
		t, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, fd.Name()))
		if err != nil {
			continue
		}
		if strings.HasPrefix(t, "/dev/fb") {
			fb = true
		} else if strings.HasPrefix(t, "/dev/input/event") {
			input = true
		}
	}
	return fb, input
}

type foreground struct {
	Launcher    bool   `json:"launcher"`      // the launcher home screen owns the LCD
	LauncherUp  bool   `json:"launcherUp"`    // launcher process exists
	PID         int    `json:"pid,omitempty"` // newest non-launcher fb owner
	Command     string `json:"command,omitempty"`
	AppID       string `json:"appId,omitempty"`
	StartedByUs bool   `json:"startedByUs,omitempty"` // started from the web
	rootPID     int    // topmost ancestor below launcher/agent, for stopping
}

func (m *appManager) detectForeground() foreground {
	procs := allProcs()
	self := os.Getpid()
	launcher := 0
	for pid, p := range procs {
		// comm is truncated to 15 chars ("M5CardputerZero"), which every
		// official app shares, so match the full executable path instead.
		if filepath.Base(strings.Fields(p.cmdline + " x")[0]) == launcherBinary {
			launcher = pid
		}
	}
	var fg foreground
	fg.LauncherUp = launcher != 0
	best := procInfo{}
	for pid, p := range procs {
		if pid == launcher || pid == self {
			continue
		}
		if fb, input := heldDevices(pid); !fb || !input {
			continue
		}
		if p.start > best.start {
			best = p
		}
	}
	if best.pid == 0 {
		fg.Launcher = launcher != 0
		return fg
	}
	fg.PID, fg.Command = best.pid, best.cmdline
	// climb to the child of the launcher (or of us) so the whole app tree can be stopped
	root := best
	for {
		parent, ok := procs[root.ppid]
		if !ok || root.ppid == launcher || root.ppid == self || root.ppid <= 1 || parent.comm == "systemd" {
			break
		}
		root = parent
	}
	fg.rootPID = root.pid
	m.mu.Lock()
	if m.running != nil && m.running.Process != nil && m.running.Process.Pid == root.pid {
		fg.StartedByUs = true
		fg.AppID = m.runID
	}
	m.mu.Unlock()
	if fg.AppID == "" {
		for _, a := range loadApps() {
			bin := strings.Fields(a.Exec)
			for _, tok := range bin {
				if strings.HasPrefix(tok, "/") && strings.Contains(root.cmdline+" "+best.cmdline, tok) {
					fg.AppID = a.ID
					break
				}
			}
			if fg.AppID != "" {
				break
			}
		}
	}
	return fg
}

func (m *appManager) handleForeground(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, m.detectForeground())
}

// --- start / stop -----------------------------------------------------------

func systemctlUser(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *appManager) ensureLauncherRunning() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := systemctlUser(ctx, "start", launcherUnit); err != nil {
		log.Printf("apps: %v", err)
	}
}

// stopForeground terminates the current foreground app (if any) and waits
// until it is gone. Returns true if something was stopped.
func (m *appManager) stopForeground(ctx context.Context) (bool, error) {
	fg := m.detectForeground()
	if fg.PID == 0 {
		return false, nil
	}
	target := fg.rootPID
	if target == 0 {
		target = fg.PID
	}
	_ = syscall.Kill(-target, syscall.SIGTERM) // process group, if it leads one
	_ = syscall.Kill(target, syscall.SIGTERM)
	_ = syscall.Kill(fg.PID, syscall.SIGTERM)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, alive := readProc(fg.PID); !alive {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	_ = syscall.Kill(-target, syscall.SIGKILL)
	_ = syscall.Kill(target, syscall.SIGKILL)
	_ = syscall.Kill(fg.PID, syscall.SIGKILL)
	time.Sleep(200 * time.Millisecond)
	if _, alive := readProc(fg.PID); alive {
		// apps started through `sudo` run as root and ignore our signals:
		// hold ESC, the launcher's force-quit (it also asks the app itself to quit)
		holdEscape(m.keys, func() bool { _, ok := readProc(fg.PID); return ok }, 6*time.Second)
	}
	if _, alive := readProc(fg.PID); alive && sudoWithoutPassword(ctx) {
		_ = exec.Command("sudo", "-n", "kill", "-KILL", strconv.Itoa(fg.PID)).Run()
	}
	return true, nil
}

func (m *appManager) handleHome(w http.ResponseWriter, r *http.Request) {
	stopped, err := m.stopForeground(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stopped": stopped})
}

func (m *appManager) handleOpen(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	a, ok := findApp(body.ID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such app"})
		return
	}
	if a.Terminal {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "terminal apps run in the Terminal window"})
		return
	}
	if err := m.open(r.Context(), a); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (m *appManager) open(ctx context.Context, a desktopApp) error {
	if _, err := m.stopForeground(ctx); err != nil {
		return err
	}
	// wait for a previous web-started app's cleanup to finish
	for i := 0; i < 30; i++ {
		m.mu.Lock()
		busy := m.running != nil
		m.mu.Unlock()
		if !busy {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := systemctlUser(ctx, "stop", launcherUnit); err != nil {
		return err
	}
	cmd := exec.Command("/bin/sh", "-c", a.Exec)
	cmd.Dir = appLaunchDir
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		m.ensureLauncherRunning()
		return fmt.Errorf("start %s: %w", a.Name, err)
	}
	m.mu.Lock()
	m.running, m.runID = cmd, a.ID
	m.mu.Unlock()
	log.Printf("apps: started %s (pid %d)", a.Name, cmd.Process.Pid)
	go func() {
		err := cmd.Wait()
		log.Printf("apps: %s exited: %v", a.Name, err)
		m.mu.Lock()
		m.running, m.runID = nil, ""
		m.mu.Unlock()
		m.ensureLauncherRunning()
	}()
	return nil
}
