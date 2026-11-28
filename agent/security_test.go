package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *server {
	t.Helper()
	dir := t.TempDir()
	st, err := newSessionStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &server{
		token:      "devicetoken123",
		sessions:   st,
		loginLimit: newAttemptLimiter(3, time.Minute),
		pairLimit:  newAttemptLimiter(3, time.Minute),
		hosts:      &hostPolicy{path: filepath.Join(dir, hostsFile), name: "cardputerzero"},
		webapps:    newWebApps(nil),
	}
}

func TestSessionsCreateLookupRevoke(t *testing.T) {
	s := newTestServer(t)
	tok, rec, err := s.sessions.create(nil, "token")
	if err != nil {
		t.Fatal(err)
	}
	if rec.PrincipalID == "" || rec.DeviceID == "" || rec.PrincipalKind != "human" {
		t.Fatalf("identifiers not filled: %+v", rec)
	}
	if _, ok, _ := s.sessions.lookup(tok); !ok {
		t.Fatal("new session not found")
	}
	b, _ := os.ReadFile(s.sessions.path())
	if strings.Contains(string(b), tok) {
		t.Fatal("sessions.json holds the cookie value, not only its hash")
	}
	if fi, _ := os.Stat(s.sessions.path()); fi.Mode().Perm() != 0o600 {
		t.Fatalf("sessions.json mode %v", fi.Mode().Perm())
	}
	// survives a restart
	st2, err := newSessionStore(s.sessions.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st2.lookup(tok); !ok {
		t.Fatal("session lost after reload")
	}
	if err := s.sessions.revokeToken(tok); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.sessions.lookup(tok); ok {
		t.Fatal("revoked session still valid")
	}
}

func TestSessionExpires(t *testing.T) {
	s := newTestServer(t)
	now := time.Now()
	s.sessions.now = func() time.Time { return now }
	tok, _, _ := s.sessions.create(nil, "token")
	s.sessions.now = func() time.Time { return now.Add(sessionTTL + time.Minute) }
	if _, ok, _ := s.sessions.lookup(tok); ok {
		t.Fatal("expired session accepted")
	}
}

// An old zd_token cookie (0.4.x) no longer signs in, even with the right
// token: it is cleared and the sign-in page is told why.
func TestOldCookieNoLongerSignsIn(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest("GET", "/api/sys", nil)
	r.AddCookie(&http.Cookie{Name: legacyCookie, Value: s.token})
	w := httptest.NewRecorder()
	if _, ok := s.authenticate(w, r); ok {
		t.Fatal("old zd_token cookie still signs in")
	}
	var cleared, notice bool
	for _, c := range w.Result().Cookies() {
		if c.Name == legacyCookie && c.MaxAge < 0 {
			cleared = true
		}
		if c.Name == noticeCookie && c.Value == noticeUpgraded && !c.HttpOnly {
			notice = true
		}
	}
	if !cleared || !notice {
		t.Fatalf("cookie cleared %v, notice for the sign-in page %v", cleared, notice)
	}
	if len(s.sessions.list()) != 0 {
		t.Fatal("a session was created from the old cookie")
	}
	// signing in again clears the notice
	lr := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"token":"`+s.token+`"}`))
	lw := httptest.NewRecorder()
	s.handleLogin(lw, lr)
	gone := false
	for _, c := range lw.Result().Cookies() {
		if c.Name == noticeCookie && c.MaxAge < 0 {
			gone = true
		}
	}
	if lw.Code != 200 || !gone {
		t.Fatalf("sign-in %d, notice cleared %v", lw.Code, gone)
	}
}

// Logout ends the session on the server; Sign Out All replaces the token.
func TestLogoutRevokesAndResetReplacesToken(t *testing.T) {
	s := newTestServer(t)
	tok, _, _ := s.sessions.create(nil, "token")
	lr := httptest.NewRequest("POST", "/api/logout", nil)
	lr.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	s.handleLogout(httptest.NewRecorder(), lr)
	r2 := httptest.NewRequest("GET", "/api/sys", nil)
	r2.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	if _, ok := s.authenticate(httptest.NewRecorder(), r2); ok {
		t.Fatal("session still valid after logout")
	}
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".config", "zerodesk"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := s.token
	if err := s.rotateToken(); err != nil {
		t.Fatal(err)
	}
	r3 := httptest.NewRequest("GET", "/api/sys", nil)
	r3.Header.Set("X-ZeroDesk-Token", old)
	if _, ok := s.authenticate(httptest.NewRecorder(), r3); ok {
		t.Fatal("old token accepted after reset")
	}
}

func TestRevokingSessionClosesItsWebApps(t *testing.T) {
	s := newTestServer(t)
	s.sessions.onRevoke = s.webapps.revokeOwner
	tok, rec, _ := s.sessions.create(nil, "token")
	s.webapps.sessions["cap1"] = webSession{app: "a", created: time.Now(), owner: rec.ID}
	s.webapps.sessions["cap2"] = webSession{app: "b", created: time.Now(), owner: "other"}
	s.sessions.revokeToken(tok)
	if _, ok := s.webapps.sessions["cap1"]; ok {
		t.Fatal("web-app window of a signed-out browser still open")
	}
	if _, ok := s.webapps.sessions["cap2"]; !ok {
		t.Fatal("another browser's window was closed")
	}
}

func TestExpiredCapabilityRefused(t *testing.T) {
	wa := newWebApps(nil)
	wa.sessions["old"] = webSession{app: "a", created: time.Now().Add(-webAppSessionTTL - time.Minute)}
	w := httptest.NewRecorder()
	wa.handleCap(w, httptest.NewRequest("GET", "/x/old/index.html", nil))
	if w.Code != http.StatusGone {
		t.Fatalf("expired capability: status %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("/x/ response without CSP sandbox")
	}
}

func TestHostPolicy(t *testing.T) {
	hp := &hostPolicy{name: "cardputerzero", extra: []string{"zd.example.com"}}
	for _, h := range []string{"localhost", "127.0.0.1", "::1", "cardputerzero", "cardputerzero.local", "CardputerZero.local.", "zagent.local", "zd.example.com"} {
		if !hp.allowed(h) {
			t.Errorf("%q refused", h)
		}
	}
	for _, h := range []string{"evil.example", "cardputerzero.evil.example", "", "8.8.8.8"} {
		if hp.allowed(h) {
			t.Errorf("%q allowed", h)
		}
	}
	if got := cleanHostList([]string{" ZD.Example.com ", "zd.example.com", "http://x", "a b", "[fe80::1]"}); strings.Join(got, ",") != "fe80::1,zd.example.com" {
		t.Errorf("cleanHostList = %v", got)
	}
}

func TestGuard(t *testing.T) {
	s := newTestServer(t)
	h := s.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	cases := []struct {
		name, method, host, path string
		hdr                      map[string]string
		want                     int
	}{
		{"rebinding host", "GET", "evil.example", "/", nil, http.StatusMisdirectedRequest},
		{"own name", "GET", "cardputerzero.local", "/", nil, 200},
		{"cross-site POST", "POST", "cardputerzero.local", "/api/logout", map[string]string{"Origin": "http://evil.example"}, 403},
		{"cross-site fetch metadata", "POST", "cardputerzero.local", "/api/fs/upload", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"same-origin POST", "POST", "cardputerzero.local", "/api/logout", map[string]string{"Origin": "http://cardputerzero.local", "Sec-Fetch-Site": "same-origin"}, 200},
		{"script without Origin", "POST", "localhost", "/api/apps/open", nil, 200},
		{"cross-site websocket", "GET", "cardputerzero.local", "/ws/term", map[string]string{"Upgrade": "websocket", "Origin": "http://evil.example"}, 403},
		{"web app null origin", "POST", "cardputerzero.local", "/x/abc/api/save", map[string]string{"Origin": "null"}, 200},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "http://"+c.host+c.path, nil)
		r.Host = c.host
		for k, v := range c.hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, w.Code, c.want)
		}
	}
}

func TestCodeEntry(t *testing.T) {
	// 2,3,4,5 are the keys for 1,2,3,4
	e := &codeEntry{want: "1234"}
	if done, _, _ := e.key(keyEnter); done {
		t.Fatal("Enter with nothing typed answered the prompt")
	}
	for _, k := range []uint16{2, 3, 4, 6} { // 1 2 3 5
		e.key(k)
	}
	if done, ok, _ := e.key(keyEnter); !done || ok {
		t.Fatal("wrong code was not denied")
	}
	e = &codeEntry{want: "1234"}
	for _, k := range []uint16{2, 3, 4, 6, keyBackspace, 5} { // 1 2 3 5 ⌫ 4
		e.key(k)
	}
	if done, ok, _ := e.key(keyEnter); !done || !ok {
		t.Fatalf("right code after a correction not accepted (typed %q)", e.typed)
	}
	e = &codeEntry{want: "1234"}
	if done, ok, _ := e.key(keyEsc); !done || ok {
		t.Fatal("Esc did not deny")
	}
}

func TestPairOneRequestAtATime(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	s := newTestServer(t)
	start := func(ip, replace string) int {
		r := httptest.NewRequest("POST", "/api/pair/start", strings.NewReader(`{"replace":"`+replace+`"}`))
		r.RemoteAddr = ip + ":5000"
		w := httptest.NewRecorder()
		s.handlePairStart(w, r)
		return w.Code
	}
	// a request from .10 is waiting on the device
	s.pair.ip, s.pair.id, s.pair.expires, s.pair.cancel = "192.168.1.10", "x", time.Now().Add(time.Minute), func() {}
	if c := start("192.168.1.11", ""); c != http.StatusConflict {
		t.Fatalf("another address replaced a waiting request: %d", c)
	}
	if c := start("192.168.1.10", ""); c != http.StatusConflict {
		t.Fatalf("same address without the request id (another browser behind it) replaced it: %d", c)
	}
	if c := start("192.168.1.10", "x"); c != 200 {
		t.Fatalf("the browser that asked could not ask again: %d", c)
	}
	// that put a prompt on the device: the next one waits pairCooldown
	s.pair.mu.Lock()
	id := s.pair.id
	s.pair.mu.Unlock()
	if c := start("192.168.1.10", id); c != http.StatusTooManyRequests {
		t.Fatalf("new prompt within the cooldown: %d", c)
	}
	time.Sleep(200 * time.Millisecond) // let the prompt (no screen here) finish
	s.pair.mu.Lock()
	s.pair.lastPrompt["192.168.1.10"] = time.Now().Add(-pairCooldown)
	s.pair.mu.Unlock()
	if c := start("192.168.1.10", ""); c != 200 {
		t.Fatalf("after the cooldown: %d", c)
	}
	time.Sleep(200 * time.Millisecond)
}

func TestPairApprovalExpiresAndIsBoundToRequester(t *testing.T) {
	s := newTestServer(t)
	p := &s.pair
	p.id, p.ip, p.approved, p.expires, p.cancel = "abc", "192.168.1.10", true, time.Now().Add(time.Minute), func() {}
	status := func(ip string) string {
		r := httptest.NewRequest("GET", "/api/pair/status?id=abc", nil)
		r.RemoteAddr = ip + ":1"
		w := httptest.NewRecorder()
		s.handlePairStatus(w, r)
		return w.Body.String()
	}
	if got := status("192.168.1.99"); !strings.Contains(got, "expired") {
		t.Fatalf("another address collected the approval: %s", got)
	}
	p.expires = time.Now().Add(-time.Second)
	if got := status("192.168.1.10"); !strings.Contains(got, "expired") {
		t.Fatalf("approval collected after it expired: %s", got)
	}
}

func TestZagentTicketsAreOneTime(t *testing.T) {
	var z zagentTickets
	tk := z.issue("ses_a")
	if parent, ok := z.take(tk); !ok || parent != "ses_a" {
		t.Fatal("ticket not taken with its session")
	}
	if _, ok := z.take(tk); ok {
		t.Fatal("ticket not one-time")
	}
	if _, ok := z.take("nope"); ok {
		t.Fatal("unknown ticket accepted")
	}
	h := http.Header{}
	h.Add("Cookie", "zd_session=a; theirs=1; zd_token=b")
	h.Set("X-ZeroDesk-Token", "t")
	withoutZeroDeskCredentials(h)
	if got := h.Get("Cookie"); got != "theirs=1" || h.Get("X-ZeroDesk-Token") != "" {
		t.Fatalf("credentials forwarded: %q", got)
	}
}

func TestActiveFilesDownload(t *testing.T) {
	for _, n := range []string{"a.html", "b.SVG", "c.js", "d.xml"} {
		if !activeContent(n) {
			t.Errorf("%s served inline", n)
		}
	}
	for _, n := range []string{"a.png", "b.mp3", "c.txt", "d.pdf"} {
		if activeContent(n) {
			t.Errorf("%s forced to download", n)
		}
	}
}
