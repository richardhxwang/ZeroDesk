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
	"errors"
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
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed all:web
var webFS embed.FS

type server struct {
	tokenMu sync.RWMutex
	token   string // device access token; "" with -no-auth
	noAuth  bool
	// credMu: signing in with the token and replacing the token never
	// interleave (Settings › Sign Out All).
	credMu sync.Mutex
	conns  liveConns

	sessions   *sessionStore // nil with -no-auth
	loginLimit *attemptLimiter
	pairLimit  *attemptLimiter
	hosts      *hostPolicy
	zagent     zagentTickets

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
	// the launcher entry: same binary, started as zerodesk-cp0-screen (the
	// package) or zerodesk-screen (a development install), or with `screen`
	if b := filepath.Base(os.Args[0]); b == "zerodesk-cp0-screen" || b == "zerodesk-screen" || flag.Arg(0) == "screen" {
		os.Exit(runDeviceScreen(*fbPath, strings.ToLower(*kbdName)))
	}
	// backends of the bundled hardware apps: `zerodesk hw <app>` (hw.go)
	if flag.Arg(0) == "hw" {
		os.Exit(runHW(flag.Args()[1:]))
	}

	token := ""
	var sessions *sessionStore
	if !*noAuth {
		var err error
		token, err = loadOrCreateToken()
		if err != nil {
			log.Fatalf("token: %v", err)
		}
		if sessions, err = newSessionStore(configDir()); err != nil {
			log.Fatalf("sessions: %v", err)
		}
	}

	s := &server{
		token:      token,
		noAuth:     *noAuth,
		sessions:   sessions,
		loginLimit: newAttemptLimiter(10, 10*time.Minute),
		pairLimit:  newAttemptLimiter(6, 10*time.Minute),
		hosts:      loadHostPolicy(),
		screen:     newScreenHub(*fbPath),
		keys:       newKeyInjector(*kbdName),
		apps:       newAppManager(),
		vnc:        newDesktopVNC(),
		vapps:      newVappManager(),

		kbdMatch: strings.ToLower(*kbdName),
		fbPath:   *fbPath,
	}
	s.webapps = newWebApps(s.apps)
	s.webapps.track = s.conns.trackAll
	if s.sessions != nil {
		// a signed-out browser's web-app windows, terminals and other open
		// connections stop working too
		s.sessions.onRevoke = func(id string) {
			s.webapps.revokeOwner(id)
			s.conns.closeOwner(id)
		}
		s.webapps.ownerAlive = s.sessions.has
		// expired sessions are removed for good, and what they opened closes
		go func() {
			for range time.Tick(time.Minute) {
				s.sessions.sweepExpired()
			}
		}()
	}
	s.apps.keys = s.keys
	s.apps.ensureLauncherRunning()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("POST /api/pair/start", s.handlePairStart)
	mux.HandleFunc("GET /api/pair/status", s.handlePairStatus)
	mux.HandleFunc("POST /api/pair/cancel", s.handlePairCancel)
	mux.HandleFunc("GET /q/{key}", s.handleQRLogin)
	mux.HandleFunc("GET /api/session", s.auth(s.handleSession))
	mux.HandleFunc("GET /api/sessions", s.auth(s.handleSessionsList))
	mux.HandleFunc("DELETE /api/sessions/{id}", s.auth(s.handleSessionRevoke))
	mux.HandleFunc("POST /api/sessions/reset", s.auth(s.handleSessionsReset))
	mux.HandleFunc("GET /api/zagent/handoff", s.handleZagentHandoff)
	mux.HandleFunc("GET /api/pairing", s.auth(s.handlePairingGet))
	mux.HandleFunc("PUT /api/pairing", s.auth(s.handlePairingPut))
	mux.HandleFunc("GET /api/hosts", s.auth(s.handleHostsGet))
	mux.HandleFunc("PUT /api/hosts", s.auth(s.handleHostsPut))
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
	mux.HandleFunc("GET /api/presets", s.auth(handlePresetsList))
	mux.HandleFunc("POST /api/presets/{id}", s.auth(handlePresetFetch))
	mux.HandleFunc("DELETE /api/presets/{id}", s.auth(handlePresetDelete))
	mux.HandleFunc("GET /api/presets/{id}/{file}", s.auth(handlePresetFile))
	mux.HandleFunc("GET /api/shell", s.auth(handleShellGet))
	mux.HandleFunc("POST /api/shell", s.auth(handleShellSet))
	mux.HandleFunc("POST /api/shell/zsh", s.auth(handleShellInstallZsh))
	mux.HandleFunc("GET /api/settings", s.auth(handleSettingsGet))
	mux.HandleFunc("POST /api/settings", s.auth(handleSettingsSet))
	mux.HandleFunc("GET /api/wifi/networks", s.auth(handleWifiList))
	mux.HandleFunc("POST /api/wifi/connect", s.auth(handleWifiConnect))
	mux.HandleFunc("POST /api/wifi/disconnect", s.auth(handleWifiDisconnect))
	mux.HandleFunc("POST /api/system/reboot", s.auth(handleReboot))
	mux.HandleFunc("GET /api/zagent/settings", s.auth(handleZagentSettings))
	mux.HandleFunc("GET /api/zagent/options", s.auth(handleZagentOptions))
	mux.HandleFunc("POST /api/zagent/set", s.auth(handleZagentSet))
	mux.HandleFunc("GET /api/zagent/web/{path...}", s.auth(handleZagentWeb()))
	mux.HandleFunc("POST /api/zagent/web/{path...}", s.auth(handleZagentWeb()))
	mux.HandleFunc("POST /api/fs/upload", s.auth(handleFsUpload))
	mux.HandleFunc("POST /api/fs/op", s.auth(handleFsOp))
	mux.HandleFunc("GET /api/trash", s.auth(handleTrashList))
	mux.HandleFunc("POST /api/trash/op", s.auth(handleTrashOp))
	mux.HandleFunc("POST /api/deb/install", s.auth(handleDebInstall))
	mux.HandleFunc("GET /api/deb/info", s.auth(handleDebInfo))
	mux.HandleFunc("GET /api/deb/icon", s.auth(handleDebIcon))
	mux.HandleFunc("GET /api/store/catalog", s.auth(handleStoreCatalog))
	mux.HandleFunc("GET /api/store/asset", s.auth(handleStoreAsset))
	mux.HandleFunc("POST /api/store/install", s.auth(handleStoreInstall))
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

	handler := logRequests(s.guard(s.zagentWebHost(mux)))
	startAlsoListen(*extra, handler) // port80.go
	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("zerodesk listening on %s and %s (auth=%v)", *addr, *extra, !*noAuth)
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
	s.tokenMu.RLock()
	defer s.tokenMu.RUnlock()
	return s.token != "" && subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1
}

// rotateToken replaces the device access token (Settings › Signed-in
// browsers › reset). Old zd_token cookies and copies of the token stop working.
func (s *server) rotateToken() error {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".config", "zerodesk", "token")
	t := randomHex(12)
	if err := writeFileAtomic(path, []byte(t+"\n")); err != nil {
		return err
	}
	s.tokenMu.Lock()
	s.token = t
	s.tokenMu.Unlock()
	log.Printf("auth: device access token replaced")
	return nil
}

// authenticate finds what authorises r: a browser session (zd_session), an
// old zd_token cookie (exchanged for a session on the spot), or the access
// token in X-ZeroDesk-Token (scripts). It may set cookies on w.
func (s *server) authenticate(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	r2, status := s.authenticateStatus(w, r)
	return r2, status == 0
}

// authenticateStatus is authenticate with the status to answer a refusal
// with (401).
func (s *server) authenticateStatus(w http.ResponseWriter, r *http.Request) (*http.Request, int) {
	if s.noAuth {
		return r, 0
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		if rec, ok, refresh := s.sessions.lookup(c.Value); ok && s.sessionUsableHere(rec, r) {
			if refresh {
				setSessionCookie(w, c.Value)
			}
			return r.WithContext(withSession(r.Context(), rec)), 0
		}
	}
	// Up to 0.4.x a browser kept the device access token in zd_token. That
	// cookie no longer signs anyone in (it was the same for every browser and
	// could not be signed out): the browser pairs once more, and the sign-in
	// page says why.
	if c, err := r.Cookie(legacyCookie); err == nil && c.Value != "" {
		clearCookie(w, legacyCookie)
		setNotice(w, noticeUpgraded)
	}
	if t := r.Header.Get("X-ZeroDesk-Token"); t != "" && s.validToken(t) {
		return r, 0
	}
	return r, http.StatusUnauthorized
}

// sessionUsableHere: a Zagent session (handed off to zagent.local) works
// only there and only while the session it came from is signed in; other
// sessions do not work on zagent.local.
func (s *server) sessionUsableHere(rec sessionRec, r *http.Request) bool {
	onZagent := requestHost(r) == zagentHost
	if rec.Method == "zagent" {
		return onZagent && s.sessions.has(rec.ParentID)
	}
	return !onZagent
}

// testHookAfterAuth runs between the sign-in check and the registration of
// a long-lived connection (tests sign the session out there).
var testHookAfterAuth func()

func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r2, status := s.authenticateStatus(w, r)
		if status != 0 {
			writeJSON(w, status, map[string]string{"error": "unauthorized"})
			return
		}
		rec, _ := sessionFrom(r2)
		if testHookAfterAuth != nil {
			testHookAfterAuth()
		}
		w2, r3, done := s.conns.track(rec.ID, w, r2)
		defer done()
		// Checked again once registered: a sign-out between the check
		// above and the registration would otherwise miss this connection.
		if rec.ID != "" && !s.sessions.has(rec.ID) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w2, r3)
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
	ip := clientIP(r)
	// The attempt is taken before the token is checked, so parallel guesses
	// from one address cannot share the last free attempt.
	ok, wait, slot := s.loginLimit.reserve(ip, time.Now())
	if !ok {
		writeRetryAfter(w, wait)
		return
	}
	tok := strings.TrimSpace(body.Token)
	_, err := s.issueSession(w, r, "token", "", func() bool { return s.noAuth || s.validToken(tok) })
	if errors.Is(err, errBadCredential) {
		time.Sleep(400 * time.Millisecond)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "wrong token"})
		return
	}
	s.loginLimit.refund(ip, slot)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// writeRetryAfter answers 429 with how long to wait.
func writeRetryAfter(w http.ResponseWriter, wait time.Duration) {
	writeRetryAfterReason(w, wait, "limit")
}

// writeRetryAfterReason: reason "cooldown" is a short pause between two
// prompts on the device, "limit" is too many attempts.
func writeRetryAfterReason(w http.ResponseWriter, wait time.Duration, reason string) {
	secs := int(wait/time.Second) + 1
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	msg := "too many attempts"
	if reason == "cooldown" {
		msg = "a new code is coming shortly"
	}
	writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": msg, "reason": reason, "retryIn": secs})
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
		switch {
		case strings.Contains(p, "/_app/immutable/") || strings.HasPrefix(p, "_app/immutable/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case strings.HasPrefix(p, "icons/") && r.URL.Query().Get("v") != "":
			// the UI asks for icons with the build's icon hash (?v=)
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		default:
			w.Header().Set("Cache-Control", "no-cache")
		}
		// revalidation with 304 for everything else
		tag := embedETag(root, p)
		if tag != "" {
			w.Header().Set("ETag", tag)
			if etagMatches(r, tag) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
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

func init() {
	// not in Go's built-in table, and /etc/mime.types may lack it
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}
