package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Web apps: cp0 apps that ship a web UI ZeroDesk opens as a native window.
// The format is specified in docs/APP-SPEC.md; keep the two in step.

const (
	webAppSpec        = 1
	webAppIdleStop    = 30 * time.Second
	webAppStartWait   = 10 * time.Second
	webAppStorageMax  = 1 << 20
	webAppSessionTTL  = 12 * time.Hour
	webAppDefaultMem  = "48M"
	webAppManifestMax = 256 << 10
)

//go:embed sdk
var sdkFS embed.FS

var (
	webAppIDRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	webAppMemRe = regexp.MustCompile(`^[0-9]{1,4}[KMG]$`)
	// web directories must live in package-owned or per-user places
	webAppRoots = []string{"/usr/share/", "/usr/lib/", "/opt/"}
)

type webIcon struct {
	Src   string `json:"src"`
	Sizes string `json:"sizes,omitempty"`
	Type  string `json:"type,omitempty"`
}

type webManifest struct {
	Name        string    `json:"name"`
	ShortName   string    `json:"short_name"`
	Description string    `json:"description"`
	StartURL    string    `json:"start_url"`
	Categories  []string  `json:"categories"`
	Icons       []webIcon `json:"icons"`
	ZeroDesk    struct {
		Spec      int       `json:"spec"`
		IconsDark []webIcon `json:"icons_dark"`
		Window    struct {
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			MinWidth  int    `json:"min_width"`
			MinHeight int    `json:"min_height"`
			Resizable *bool  `json:"resizable"`
			Titlebar  string `json:"titlebar"`
		} `json:"window"`
		Permissions []string `json:"permissions"`
		Backend     *struct {
			Exec      []string `json:"exec"`
			MemoryMax string   `json:"memory_max"`
			Exclusive bool     `json:"exclusive"`
		} `json:"backend"`
	} `json:"zerodesk"`
}

// webApp is what the UI sees (GET /api/webapps).
type webApp struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	Width       int      `json:"width"`
	Height      int      `json:"height"`
	MinWidth    int      `json:"min_width"`
	MinHeight   int      `json:"min_height"`
	Resizable   bool     `json:"resizable"`
	Titlebar    string   `json:"titlebar"`
	Permissions []string `json:"permissions"`
	Backend     bool     `json:"backend"`
	// the device app (.desktop id) this window replaces, "" for ZeroDesk-only apps
	DeviceApp string `json:"device_app,omitempty"`

	dir      string
	start    string
	manifest webManifest
}

var knownPermissions = map[string]bool{
	"storage": true, "notifications": true, "files.read": true, "files.write": true, "device.info": true,
}

func userWebAppsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "zerodesk", "apps")
}

// bundledWebAppsDir holds the apps that ship with ZeroDesk itself (the
// hardware apps): <prefix>/share/zerodesk/apps next to <prefix>/bin/zerodesk,
// so /usr/share/zerodesk/apps for the package and the per-user directory for
// a development install in ~/.local/bin.
func bundledWebAppsDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Join(filepath.Dir(filepath.Dir(exe)), "share", "zerodesk", "apps")
}

// webDirAllowed resolves symlinks and checks the directory is in an allowed root.
func webDirAllowed(dir string) (string, bool) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", false
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return "", false
	}
	roots := append([]string{userWebAppsDir() + "/"}, webAppRoots...)
	for _, r := range roots {
		if rr, err := filepath.EvalSymlinks(strings.TrimSuffix(r, "/")); err == nil && strings.HasPrefix(real+"/", rr+"/") {
			return real, true
		}
	}
	return "", false
}

func loadWebApp(id, dir, deviceApp string) (*webApp, error) {
	real, ok := webDirAllowed(dir)
	if !ok {
		return nil, fmt.Errorf("%s: web directory not allowed", id)
	}
	f, err := os.Open(filepath.Join(real, "manifest.webmanifest"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var m webManifest
	if err := json.NewDecoder(io.LimitReader(f, webAppManifestMax)).Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: manifest: %w", id, err)
	}
	if m.ZeroDesk.Spec != webAppSpec {
		return nil, fmt.Errorf("%s: unsupported zerodesk.spec %d", id, m.ZeroDesk.Spec)
	}
	a := &webApp{ID: id, DeviceApp: deviceApp, dir: real, manifest: m}
	a.Name = m.ShortName
	if a.Name == "" {
		a.Name = m.Name
	}
	if a.Name == "" {
		a.Name = id
	}
	a.Description = m.Description
	if len(m.Categories) > 2 {
		m.Categories = m.Categories[:2]
	}
	a.Categories = m.Categories
	w := m.ZeroDesk.Window
	a.Width, a.Height = clampInt(w.Width, 240, 4000, 720), clampInt(w.Height, 160, 4000, 480)
	a.MinWidth, a.MinHeight = clampInt(w.MinWidth, 200, a.Width, 320), clampInt(w.MinHeight, 140, a.Height, 240)
	a.Resizable = w.Resizable == nil || *w.Resizable
	a.Titlebar = "standard"
	if w.Titlebar == "app" {
		a.Titlebar = "app"
	}
	a.Permissions = []string{}
	for _, p := range m.ZeroDesk.Permissions {
		if knownPermissions[p] {
			a.Permissions = append(a.Permissions, p)
		}
	}
	if b := m.ZeroDesk.Backend; b != nil && len(b.Exec) > 0 {
		// "zerodesk" is ZeroDesk's own executable (the backends of the bundled
		// hardware apps are `zerodesk hw <app>`); otherwise absolute, or
		// relative to the web directory (and inside it)
		if b.Exec[0] == "zerodesk" {
			if exe, err := os.Executable(); err == nil {
				b.Exec[0] = exe
			} else {
				b.Exec = nil
			}
		} else if !filepath.IsAbs(b.Exec[0]) {
			if p, ok := webFile(real, b.Exec[0]); ok {
				b.Exec[0] = p
			} else {
				b.Exec = nil
			}
		}
		a.Backend = len(b.Exec) > 0
	}
	a.start = m.StartURL
	if a.start == "" {
		a.start = "index.html"
	}
	if u, err := url.Parse(a.start); err != nil || u.IsAbs() || strings.HasPrefix(u.Path, "/") || strings.Contains(u.Path, "..") {
		a.start = "index.html"
	}
	return a, nil
}

func clampInt(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	return max(lo, min(hi, v))
}

// loadWebApps finds every web app: X-ZeroDesk-Web in the launcher's .desktop
// files first, then the apps bundled with ZeroDesk, then
// ~/.local/share/zerodesk/apps/<id>/. A bundled or per-user app whose id is an
// installed launcher app replaces that app's virtual screen.
func loadWebApps() []*webApp {
	byID := map[string]*webApp{}
	installed := map[string]string{} // launcher app id -> name
	files, _ := filepath.Glob(filepath.Join(appLaunchDir, "applications", "*.desktop"))
	for _, f := range files {
		d, ok := parseDesktop(f)
		if ok {
			installed[d.ID] = d.Name
		}
		if !ok || d.WebDir == "" || !webAppIDRe.MatchString(d.ID) {
			continue
		}
		a, err := loadWebApp(d.ID, d.WebDir, d.ID)
		if err != nil {
			log.Printf("webapp %v", err)
			continue
		}
		if a.Name == d.ID {
			a.Name = d.Name
		}
		byID[a.ID] = a
	}
	for _, root := range []string{bundledWebAppsDir(), userWebAppsDir()} {
		if root == "" {
			continue
		}
		dirs, _ := os.ReadDir(root)
		for _, e := range dirs {
			id := e.Name()
			if !webAppIDRe.MatchString(id) || byID[id] != nil {
				continue
			}
			device := ""
			if _, ok := installed[id]; ok {
				device = id
			}
			a, err := loadWebApp(id, filepath.Join(root, id), device)
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					log.Printf("webapp %v", err)
				}
				continue
			}
			byID[id] = a
		}
	}
	out := make([]*webApp, 0, len(byID))
	for _, a := range byID {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func findWebApp(id string) *webApp {
	for _, a := range loadWebApps() {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// ---- sessions (one per open window) and backends ----

type webSession struct {
	app     string
	created time.Time
}

type webBackend struct {
	socket  string
	unit    string
	running bool
	stopAt  *time.Timer
}

type webApps struct {
	mu       sync.Mutex
	sessions map[string]webSession // capability -> session
	backends map[string]*webBackend
	starting map[string]*sync.Mutex
	apps     *appManager
}

func newWebApps(apps *appManager) *webApps {
	return &webApps{sessions: map[string]webSession{}, backends: map[string]*webBackend{}, starting: map[string]*sync.Mutex{}, apps: apps}
}

func (wa *webApps) handleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, loadWebApps())
}

// GET /api/webapps/{id}/icon?scheme=dark
func (wa *webApps) handleIcon(w http.ResponseWriter, r *http.Request) {
	a := findWebApp(r.PathValue("id"))
	if a == nil {
		http.NotFound(w, r)
		return
	}
	icons := a.manifest.Icons
	if r.URL.Query().Get("scheme") == "dark" && len(a.manifest.ZeroDesk.IconsDark) > 0 {
		icons = a.manifest.ZeroDesk.IconsDark
	}
	p := bestIcon(a.dir, icons)
	if p == "" && a.DeviceApp != "" {
		if d, ok := findApp(a.DeviceApp); ok {
			p = d.iconPath()
		}
	}
	if p == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "max-age=3600")
	http.ServeFile(w, r, p)
}

// bestIcon picks the largest declared icon that exists inside the web directory.
func bestIcon(dir string, icons []webIcon) string {
	best, bestSize := "", -1
	for _, ic := range icons {
		p, ok := webFile(dir, ic.Src)
		if !ok {
			continue
		}
		size := 0
		if ic.Sizes == "any" || strings.HasSuffix(ic.Src, ".svg") {
			size = 1 << 20
		} else {
			fmt.Sscanf(ic.Sizes, "%dx", &size)
		}
		if size > bestSize {
			best, bestSize = p, size
		}
	}
	return best
}

// webFile maps a manifest-relative path to a regular file inside dir.
func webFile(dir, rel string) (string, bool) {
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	if rel == "" || rel == "." {
		return "", false
	}
	p := filepath.Join(dir, filepath.FromSlash(rel))
	real, err := filepath.EvalSymlinks(p)
	if err != nil || !strings.HasPrefix(real, dir+string(filepath.Separator)) {
		return "", false
	}
	if st, err := os.Stat(real); err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	return real, true
}

// POST /api/webapps/{id}/session -> {"base":"/x/<cap>/","start":"index.html"}
func (wa *webApps) handleSessionOpen(w http.ResponseWriter, r *http.Request) {
	a := findWebApp(r.PathValue("id"))
	if a == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such app"})
		return
	}
	if a.Backend && a.manifest.ZeroDesk.Backend.Exclusive && a.DeviceApp != "" && wa.deviceAppRunning(a.DeviceApp) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "running on device"})
		return
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	capability := hex.EncodeToString(b)
	wa.mu.Lock()
	now := time.Now()
	for k, s := range wa.sessions {
		if now.Sub(s.created) > webAppSessionTTL {
			delete(wa.sessions, k)
		}
	}
	wa.sessions[capability] = webSession{app: a.ID, created: now}
	if be := wa.backends[a.ID]; be != nil && be.stopAt != nil {
		be.stopAt.Stop()
		be.stopAt = nil
	}
	wa.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"base": "/x/" + capability + "/", "start": a.start, "app": a})
}

// DELETE /api/webapps/session/{cap}
func (wa *webApps) handleSessionClose(w http.ResponseWriter, r *http.Request) {
	capability := r.PathValue("cap")
	wa.mu.Lock()
	s, ok := wa.sessions[capability]
	delete(wa.sessions, capability)
	if ok {
		wa.scheduleStopLocked(s.app)
	}
	wa.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (wa *webApps) deviceAppRunning(id string) bool {
	return wa.apps.detectForeground().AppID == id
}

// scheduleStopLocked stops an app's backend once no window uses it.
func (wa *webApps) scheduleStopLocked(app string) {
	for _, s := range wa.sessions {
		if s.app == app {
			return
		}
	}
	be := wa.backends[app]
	if be == nil || !be.running || be.stopAt != nil {
		return
	}
	be.stopAt = time.AfterFunc(webAppIdleStop, func() {
		wa.mu.Lock()
		for _, s := range wa.sessions {
			if s.app == app {
				be.stopAt = nil
				wa.mu.Unlock()
				return
			}
		}
		be.running, be.stopAt = false, nil
		unit := be.unit
		wa.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = systemctlUser(ctx, "stop", unit)
	})
}

func webSocketDir() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), fmt.Sprintf("zerodesk-%d", os.Getuid()))
	}
	return filepath.Join(dir, "zerodesk", "apps")
}

// ensureBackend starts the app's backend (once) and returns its socket.
func (wa *webApps) ensureBackend(a *webApp) (string, error) {
	wa.mu.Lock()
	lock := wa.starting[a.ID]
	if lock == nil {
		lock = &sync.Mutex{}
		wa.starting[a.ID] = lock
	}
	wa.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()

	wa.mu.Lock()
	be := wa.backends[a.ID]
	if be != nil && be.running {
		wa.mu.Unlock()
		if c, err := net.DialTimeout("unix", be.socket, time.Second); err == nil {
			c.Close()
			return be.socket, nil
		}
		wa.mu.Lock()
		be.running = false
	}
	wa.mu.Unlock()

	if a.manifest.ZeroDesk.Backend.Exclusive && a.DeviceApp != "" && wa.deviceAppRunning(a.DeviceApp) {
		return "", errors.New("running on device")
	}
	dir := webSocketDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	sock := filepath.Join(dir, a.ID+".sock")
	_ = os.Remove(sock)
	unit := "zerodesk-app-" + a.ID + ".service"
	mem := a.manifest.ZeroDesk.Backend.MemoryMax
	if !webAppMemRe.MatchString(mem) {
		mem = webAppDefaultMem
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// a unit left over from an earlier agent run
	_ = systemctlUser(ctx, "stop", unit)
	_ = exec.CommandContext(ctx, "systemctl", "--user", "reset-failed", unit).Run()
	args := []string{"--user", "--quiet", "--collect", "--unit=" + unit,
		"--property=MemoryMax=" + mem, "--property=KillMode=mixed",
		"--working-directory=" + a.dir,
		"--setenv=ZERODESK_APP_ID=" + a.ID, "--setenv=ZERODESK_SOCKET=" + sock, "--setenv=ZERODESK_WEB_DIR=" + a.dir,
		"--"}
	args = append(args, a.manifest.ZeroDesk.Backend.Exec...)
	if out, err := exec.CommandContext(ctx, "systemd-run", args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("systemd-run: %v: %s", err, strings.TrimSpace(string(out)))
	}
	deadline := time.Now().Add(webAppStartWait)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("unix", sock, 200*time.Millisecond); err == nil {
			c.Close()
			wa.mu.Lock()
			wa.backends[a.ID] = &webBackend{socket: sock, unit: unit, running: true}
			wa.mu.Unlock()
			return sock, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = systemctlUser(context.Background(), "stop", unit)
	return "", errors.New("backend did not start")
}

// ---- /x/<cap>/...: the window's files, SDK and backend ----

func (wa *webApps) handleCap(w http.ResponseWriter, r *http.Request) {
	capability, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/x/"), "/")
	wa.mu.Lock()
	s, ok := wa.sessions[capability]
	wa.mu.Unlock()
	h := w.Header()
	// the page runs in an opaque-origin sandbox: the path is the credential
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	h.Set("Content-Security-Policy", "frame-ancestors 'self'")
	h.Set("Referrer-Policy", "no-referrer")
	if r.Method == http.MethodOptions {
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
		h.Set("Access-Control-Allow-Headers", r.Header.Get("Access-Control-Request-Headers"))
		h.Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !ok {
		http.Error(w, "window closed", http.StatusGone)
		return
	}
	a := findWebApp(s.app)
	if a == nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasPrefix(rest, "_zd/"):
		name := strings.TrimPrefix(rest, "_zd/")
		switch name {
		case "sdk.js", "sdk.css":
			h.Set("Cache-Control", "no-cache")
		case "f7-icons.woff2": // framework7-icons, MIT (THIRD_PARTY_NOTICES.md)
			h.Set("Cache-Control", "max-age=86400")
		default:
			http.NotFound(w, r)
			return
		}
		http.ServeFileFS(w, r, sdkFS, "sdk/"+name)
	case rest == "api" || strings.HasPrefix(rest, "api/"):
		if !a.Backend {
			http.NotFound(w, r)
			return
		}
		sock, err := wa.ensureBackend(a)
		if err != nil {
			status := http.StatusBadGateway
			if err.Error() == "running on device" {
				status = http.StatusConflict
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		backendProxy(a.ID, sock).ServeHTTP(w, r2Backend(r, "/"+rest))
	default:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if rest == "" {
			rest = a.start
		}
		p, ok := webFile(a.dir, rest)
		// a backend kept next to the web files is not served as a file
		if !ok || (a.Backend && p == a.manifest.ZeroDesk.Backend.Exec[0]) {
			http.NotFound(w, r)
			return
		}
		h.Set("Cache-Control", "no-cache")
		if strings.HasSuffix(p, ".webmanifest") {
			h.Set("Content-Type", "application/manifest+json")
		}
		f, err := os.Open(p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, _ := f.Stat()
		// ServeContent, not ServeFile: no redirect from …/index.html to …/
		http.ServeContent(w, r, filepath.Base(p), st.ModTime(), f)
	}
}

func r2Backend(r *http.Request, p string) *http.Request {
	r2 := r.Clone(r.Context())
	r2.URL.Path = p
	r2.URL.RawPath = ""
	r2.Header.Del("Cookie")
	r2.Header.Del("X-ZeroDesk-Token")
	return r2
}

func backendProxy(app, sock string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "backend"
			pr.Out.Host = "backend"
			pr.Out.Header.Set("X-ZeroDesk-App", app)
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
		FlushInterval: -1, // stream server-sent events and long responses
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "backend: " + err.Error()})
		},
	}
}

// ---- storage (zd.storage, through the window's SDK bridge) ----

func webAppStoragePath(id string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "zerodesk", "appdata", id, "storage.json")
}

var webStorageMu sync.Mutex

// GET /api/webapps/{id}/storage -> {"key": value, ...}
func (wa *webApps) handleStorageGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !webAppIDRe.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	webStorageMu.Lock()
	b, err := os.ReadFile(webAppStoragePath(id))
	webStorageMu.Unlock()
	if err != nil || !json.Valid(b) {
		b = []byte("{}")
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

// PUT /api/webapps/{id}/storage  {"key": value | null, ...} merges (null removes)
func (wa *webApps) handleStoragePut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !webAppIDRe.MatchString(id) || findWebApp(id) == nil {
		http.NotFound(w, r)
		return
	}
	var patch map[string]json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, webAppStorageMax)).Decode(&patch); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	webStorageMu.Lock()
	defer webStorageMu.Unlock()
	p := webAppStoragePath(id)
	cur := map[string]json.RawMessage{}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &cur)
	}
	for k, v := range patch {
		if string(v) == "null" {
			delete(cur, k)
		} else {
			cur[k] = v
		}
	}
	b, _ := json.Marshal(cur)
	if len(b) > webAppStorageMax {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "storage full"})
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil || os.Rename(tmp, p) != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
