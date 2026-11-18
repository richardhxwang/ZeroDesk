package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Virtual app sessions: a second, independent copy of a device app runs with
// zd_vfb.so preloaded, drawing into a shared-memory framebuffer and reading
// keys from a FIFO. The browser gets its own window for it; the Zero's own
// screen, keyboard and foreground app are not touched.

const (
	vfbWidth       = 320
	vfbHeight      = 170
	vfbStride      = vfbWidth * 2
	maxVSessions   = 3
	minFreeMemMB   = 45                              // refuse to start another copy below this
	vfbShimRelPath = ".local/lib/zerodesk/zd_vfb.so" // deploy.sh (development)
	vfbShimPkgPath = "/usr/lib/zerodesk/zd_vfb.so"   // the .deb package
)

type vsession struct {
	ID      string `json:"id"`
	AppID   string `json:"appId"`
	Name    string `json:"name"`
	Started time.Time
	cmd     *exec.Cmd
	fbPath  string
	kbdPath string
	kbd     *os.File
	ptrPath string
	ptr     *pointerSink
	hub     *screenHub
	mu      sync.Mutex
	exited  bool
	reason  string
}

type vappManager struct {
	mu       sync.Mutex
	sessions map[string]*vsession
}

func newVappManager() *vappManager {
	return &vappManager{sessions: map[string]*vsession{}}
}

func memAvailableMB() int {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return -1
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) >= 2 && fs[0] == "MemAvailable:" {
			kb, _ := strconv.Atoi(fs[1])
			return kb / 1024
		}
	}
	return -1
}

// withShim makes the app's command line carry the preload environment, also
// through `sudo` (which would otherwise drop LD_PRELOAD).
func withShim(execLine string, env []string) string {
	prefix := "env " + strings.Join(env, " ") + " "
	fields := strings.Fields(execLine)
	if len(fields) > 0 && (fields[0] == "sudo" || fields[0] == "/usr/bin/sudo") {
		// keep sudo and its options, then insert env before the program
		i := 1
		for i < len(fields) && strings.HasPrefix(fields[i], "-") {
			i++
			if fields[i-1] == "--" {
				break
			}
		}
		return strings.Join(fields[:i], " ") + " " + prefix + strings.Join(fields[i:], " ")
	}
	return prefix + execLine
}

func (m *vappManager) start(a desktopApp) (*vsession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.AppID == a.ID && !s.isExited() {
			return s, nil // one virtual copy per app
		}
	}
	if len(m.sessions) >= maxVSessions {
		return nil, fmt.Errorf("too many apps open on the computer (max %d)", maxVSessions)
	}
	if free := memAvailableMB(); free >= 0 && free < minFreeMemMB {
		return nil, fmt.Errorf("not enough free memory on the device (%d MB)", free)
	}
	home, _ := os.UserHomeDir()
	shim := filepath.Join(home, vfbShimRelPath)
	if _, err := os.Stat(shim); err != nil {
		shim = vfbShimPkgPath
		if _, err := os.Stat(shim); err != nil {
			return nil, fmt.Errorf("virtual screen library missing: %w", err)
		}
	}

	id := randomID()[:8]
	s := &vsession{ID: id, AppID: a.ID, Name: a.Name, Started: time.Now()}
	s.fbPath = "/dev/shm/zerodesk-" + id + ".fb"
	s.kbdPath = filepath.Join(os.TempDir(), "zerodesk-"+id+".kbd")
	if err := os.WriteFile(s.fbPath, make([]byte, vfbStride*vfbHeight), 0o600); err != nil {
		return nil, fmt.Errorf("create framebuffer: %w", err)
	}
	s.ptrPath = filepath.Join(os.TempDir(), "zerodesk-"+id+".ptr")
	if err := syscall.Mkfifo(s.kbdPath, 0o600); err != nil {
		os.Remove(s.fbPath)
		return nil, fmt.Errorf("create keyboard pipe: %w", err)
	}
	// O_RDWR so opening never blocks and the app never sees EOF
	kbd, err := os.OpenFile(s.kbdPath, os.O_RDWR, 0)
	if err != nil {
		s.cleanup()
		return nil, fmt.Errorf("open keyboard pipe: %w", err)
	}
	s.kbd = kbd
	if p, err := newPointerSink(s.ptrPath, vfbWidth, vfbHeight); err == nil {
		s.ptr = p
	} else {
		log.Printf("vapps: %v", err)
	}

	env := []string{
		"LD_PRELOAD=" + shim,
		"ZD_VFB=" + s.fbPath,
		"ZD_VKBD=" + s.kbdPath,
		// cp0_lvgl adds a pointer device for this (touch from the browser)
		"LV_LINUX_MOUSE_DEVICE=" + s.ptrPath,
		fmt.Sprintf("ZD_W=%d", vfbWidth),
		fmt.Sprintf("ZD_H=%d", vfbHeight),
		fmt.Sprintf("ZD_STRIDE=%d", vfbStride),
	}
	cmd := exec.Command("/bin/sh", "-c", withShim(a.Exec, env))
	cmd.Dir = appLaunchDir
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		s.cleanup()
		return nil, fmt.Errorf("start %s: %w", a.Name, err)
	}
	s.cmd = cmd
	s.hub = newFileHub(s.fbPath, fbGeometry{width: vfbWidth, height: vfbHeight, stride: vfbStride, bpp: 16})
	m.sessions[id] = s
	log.Printf("vapps: %s started virtually (session %s, pid %d)", a.Name, id, cmd.Process.Pid)

	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.exited = true
		if err != nil {
			s.reason = err.Error()
		}
		s.mu.Unlock()
		log.Printf("vapps: %s (session %s) exited: %v", a.Name, id, err)
		// keep the session briefly so the window can show that it ended
		time.AfterFunc(5*time.Second, func() {
			m.mu.Lock()
			delete(m.sessions, id)
			m.mu.Unlock()
			s.cleanup()
		})
	}()
	return s, nil
}

func (s *vsession) isExited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

func (s *vsession) cleanup() {
	if s.kbd != nil {
		s.kbd.Close()
	}
	s.ptr.close()
	os.Remove(s.fbPath)
	os.Remove(s.kbdPath)
	os.Remove(s.ptrPath)
}

func (s *vsession) stop() {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	pid := s.cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	time.AfterFunc(1500*time.Millisecond, func() {
		if !s.isExited() {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			// apps started through sudo run as root (GitHub edition only)
			if sudoWithoutPassword(context.Background()) {
				_ = exec.Command("sudo", "-n", "pkill", "-KILL", "-g", strconv.Itoa(pid)).Run()
			}
		}
	})
}

// sendKey writes one key event plus SYN into the app's keyboard pipe.
func (s *vsession) sendKey(code, val int) error {
	if code <= 0 || code > maxKey || val < 0 || val > 2 {
		return fmt.Errorf("invalid key event code=%d val=%d", code, val)
	}
	buf := make([]byte, 48)
	now := time.Now()
	encodeEvent(buf[0:24], now, evKey, uint16(code), int32(val))
	encodeEvent(buf[24:48], now, evSyn, synReport, 0)
	_, err := s.kbd.Write(buf)
	return err
}

func (m *vappManager) get(id string) *vsession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

// --- HTTP ---------------------------------------------------------------------

type vsessionInfo struct {
	ID      string `json:"id"`
	AppID   string `json:"appId"`
	Name    string `json:"name"`
	Exited  bool   `json:"exited"`
	Reason  string `json:"reason,omitempty"`
	Started int64  `json:"started"`
}

func (s *vsession) info() vsessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return vsessionInfo{s.ID, s.AppID, s.Name, s.exited, s.reason, s.Started.Unix()}
}

func (m *vappManager) handleList(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	out := make([]vsessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.info())
	}
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (m *vappManager) handleStart(w http.ResponseWriter, r *http.Request) {
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
	s, err := m.start(a)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.info())
}

func (m *vappManager) handleStop(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	if s := m.get(body.ID); s != nil {
		s.stop()
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (m *vappManager) handleWS(w http.ResponseWriter, r *http.Request) {
	s := m.get(r.URL.Query().Get("id"))
	if s == nil {
		http.NotFound(w, r)
		return
	}
	var sendPointer func(x, y int, down bool) error
	if s.ptr != nil {
		sendPointer = s.ptr.send
	}
	streamScreen(w, r, s.hub, s.sendKey, sendPointer)
}
