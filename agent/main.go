// ZeroDesk agent: a small HTTP/WebSocket service that runs on the
// CardputerZero and exposes its screen, keyboard, apps, files and a shell
// to the macOS-style web desktop in ../web.
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

//go:embed all:web
var webFS embed.FS

const cookieName = "zd_token"

type server struct {
	token   string
	screen  *screenHub
	keys    *keyInjector
	apps    *appManager
	vnc     *desktopVNC
	vapps   *vappManager
	webapps *webApps

	kbdMatch string
	fbPath   string
	pair     pairState
}

func main() {
	ensureSessionEnv()
	// The device has ~350 MB RAM shared with the launcher, apps and the
	// desktop. Keep the Go heap small and return memory to the OS eagerly.
	debug.SetMemoryLimit(32 << 20)
	debug.SetGCPercent(50)

	addr := flag.String("addr", ":8686", "listen address")
	extra := flag.String("also", ":80", "additional listen address (e.g. :80 so http://cardputerzero.local works); empty to disable")
	fbPath := flag.String("fb", "/dev/fb0", "framebuffer device of the internal LCD")
	kbdName := flag.String("kbd", "tca8418", "substring of the internal keyboard's input device name")
	noAuth := flag.Bool("no-auth", false, "disable token check (development only)")
	flag.Parse()
	// the launcher entry: same binary, started as zerodesk-screen (or `zerodesk screen`)
	if filepath.Base(os.Args[0]) == "zerodesk-screen" || flag.Arg(0) == "screen" {
		os.Exit(runDeviceScreen(*fbPath, strings.ToLower(*kbdName)))
	}
	// backends of the bundled hardware apps: `zerodesk hw <app>` (hw.go)
	if flag.Arg(0) == "hw" {
		os.Exit(runHW(flag.Args()[1:]))
	}

	token := ""
	if !*noAuth {
		var err error
		token, err = loadOrCreateToken()
		if err != nil {
			log.Fatalf("token: %v", err)
		}
	}

	s := &server{
		token:  token,
		screen: newScreenHub(*fbPath),
		keys:   newKeyInjector(*kbdName),
		apps:   newAppManager(),
		vnc:    newDesktopVNC(),
		vapps:  newVappManager(),

		kbdMatch: strings.ToLower(*kbdName),
		fbPath:   *fbPath,
	}
	s.webapps = newWebApps(s.apps)
	s.apps.keys = s.keys
	s.apps.ensureLauncherRunning()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("POST /api/pair/start", s.handlePairStart)
	mux.HandleFunc("GET /api/pair/status", s.handlePairStatus)
	mux.HandleFunc("POST /api/pair/cancel", s.handlePairCancel)
	mux.HandleFunc("GET /api/session", s.auth(s.handleSession))
	mux.HandleFunc("GET /api/wallpaper", s.auth(handleWallpaper))
	mux.HandleFunc("GET /api/sys", s.auth(handleSys))
	mux.HandleFunc("GET /api/apps", s.auth(s.apps.handleList))
	mux.HandleFunc("GET /api/apps/icon", s.auth(s.apps.handleIcon))
	mux.HandleFunc("GET /api/apps/foreground", s.auth(s.apps.handleForeground))
	mux.HandleFunc("POST /api/apps/open", s.auth(s.apps.handleOpen))
	mux.HandleFunc("POST /api/apps/home", s.auth(s.apps.handleHome))
	mux.HandleFunc("GET /api/fs/list", s.auth(handleFsList))
	mux.HandleFunc("GET /api/fs/get", s.auth(handleFsGet))
	mux.HandleFunc("GET /api/audio/peaks", s.auth(handleAudioPeaks))
	mux.HandleFunc("GET /api/piano/{name}", s.auth(handlePianoSample))
	mux.HandleFunc("GET /api/settings", s.auth(handleSettingsGet))
	mux.HandleFunc("POST /api/settings", s.auth(handleSettingsSet))
	mux.HandleFunc("GET /api/wifi/networks", s.auth(handleWifiList))
	mux.HandleFunc("POST /api/wifi/connect", s.auth(handleWifiConnect))
	mux.HandleFunc("POST /api/wifi/disconnect", s.auth(handleWifiDisconnect))
	mux.HandleFunc("POST /api/system/reboot", s.auth(handleReboot))
	mux.HandleFunc("GET /api/zagent/settings", s.auth(handleZagentSettings))
	mux.HandleFunc("GET /api/zagent/options", s.auth(handleZagentOptions))
	mux.HandleFunc("POST /api/zagent/set", s.auth(handleZagentSet))
	mux.HandleFunc("POST /api/fs/upload", s.auth(handleFsUpload))
	mux.HandleFunc("POST /api/fs/op", s.auth(handleFsOp))
	mux.HandleFunc("GET /api/trash", s.auth(handleTrashList))
	mux.HandleFunc("POST /api/trash/op", s.auth(handleTrashOp))
	mux.HandleFunc("GET /api/apps/info", s.auth(s.apps.handleAppInfo))
	mux.HandleFunc("POST /api/apps/uninstall", s.auth(s.apps.handleUninstall))
	mux.HandleFunc("POST /api/deb/install", s.auth(handleDebInstall))
	mux.HandleFunc("POST /api/rec/start", s.auth(handleRecStart))
	mux.HandleFunc("POST /api/rec/stop", s.auth(handleRecStop))
	mux.HandleFunc("POST /api/rec/pause", s.auth(handleRecPause))
	mux.HandleFunc("GET /api/rec/status", s.auth(handleRecStatus))
	mux.HandleFunc("GET /ws/screen", s.auth(s.handleScreenWS))
	mux.HandleFunc("GET /ws/term", s.auth(handleTermWS))
	mux.HandleFunc("GET /ws/vnc", s.auth(s.handleVNCWS))
	mux.HandleFunc("GET /api/webapps", s.auth(s.webapps.handleList))
	mux.HandleFunc("GET /api/webapps/{id}/icon", s.auth(s.webapps.handleIcon))
	mux.HandleFunc("POST /api/webapps/{id}/session", s.auth(s.webapps.handleSessionOpen))
	mux.HandleFunc("DELETE /api/webapps/session/{cap}", s.auth(s.webapps.handleSessionClose))
	mux.HandleFunc("GET /api/webapps/{id}/storage", s.auth(s.webapps.handleStorageGet))
	mux.HandleFunc("PUT /api/webapps/{id}/storage", s.auth(s.webapps.handleStoragePut))
	// window content: authorised by the capability in the path, not the cookie (docs/APP-SPEC.md §6)
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		mux.HandleFunc(m+" /x/", s.webapps.handleCap)
	}
	mux.HandleFunc("GET /api/vapps", s.auth(s.vapps.handleList))
	mux.HandleFunc("POST /api/vapps/start", s.auth(s.vapps.handleStart))
	mux.HandleFunc("POST /api/vapps/stop", s.auth(s.vapps.handleStop))
	mux.HandleFunc("GET /ws/vapp", s.auth(s.vapps.handleWS))

	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", staticHandler(sub))

	handler := logRequests(mux)
	if *extra != "" {
		go func() {
			// needs CAP_NET_BIND_SERVICE for ports < 1024; optional
			srv := &http.Server{Addr: *extra, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
			if err := srv.ListenAndServe(); err != nil {
				log.Printf("also-listen %s: %v", *extra, err)
			}
		}()
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("zerodesk listening on %s and %s (auth=%v)", *addr, *extra, token != "")
	log.Fatal(srv.ListenAndServe())
}

// loadOrCreateToken keeps one random access token per device in
// ~/.config/zerodesk/token so the URL stays stable across restarts.
func loadOrCreateToken() (string, error) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "zerodesk")
	path := filepath.Join(dir, "token")
	if b, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(b)); t != "" {
			return t, nil
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	t := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(t+"\n"), 0o600); err != nil {
		return "", err
	}
	return t, nil
}

func (s *server) validToken(t string) bool {
	return subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1
}

func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			next(w, r)
			return
		}
		if c, err := r.Cookie(cookieName); err == nil && s.validToken(c.Value) {
			next(w, r)
			return
		}
		if t := r.Header.Get("X-ZeroDesk-Token"); t != "" && s.validToken(t) {
			next(w, r)
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if s.token != "" && !s.validToken(strings.TrimSpace(body.Token)) {
		time.Sleep(400 * time.Millisecond)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "wrong token"})
		return
	}
	s.setSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleSession(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	host, _ := os.Hostname()
	user := os.Getenv("USER")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"keyboard": s.keys.deviceName(),
		"home":     home,
		"hostname": host,
		"user":     user,
		"edition":  edition,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// staticHandler serves the embedded web UI. The build step stores a
// pre-compressed .gz next to every text asset, so nothing is compressed at
// request time on the device. index.html is revalidated; hashed assets are
// cached for a year.
func staticHandler(root fs.FS) http.Handler {
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" || strings.HasSuffix(p, "/") {
			p += "index.html"
		}
		if strings.Contains(p, "/_app/immutable/") || strings.HasPrefix(p, "_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			if f, err := root.Open(p + ".gz"); err == nil {
				defer f.Close()
				if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
					w.Header().Set("Content-Type", ct)
				}
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Add("Vary", "Accept-Encoding")
				_, _ = io.Copy(w, f)
				return
			}
		}
		if _, err := fs.Stat(root, p); err != nil {
			// single-page app: unknown routes fall back to index.html
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			files.ServeHTTP(w, r2)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only state-changing calls are logged: polling GETs would keep writing
		// to the journal (and the SD card) every few seconds.
		if r.Method != http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}
		h.ServeHTTP(w, r)
	})
}
