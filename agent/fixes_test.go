package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// #1: a removed browser's open WebSocket is closed.
func TestRevokeClosesOpenWebSocket(t *testing.T) {
	s := newTestServer(t)
	s.sessions.onRevoke = func(id string) { s.webapps.revokeOwner(id); s.conns.closeOwner(id) }
	tok, rec, _ := s.sessions.create(nil, "token")
	ended := make(chan struct{})
	srv := httptest.NewServer(s.auth(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer close(ended)
		for { // like Terminal: read input until the connection goes
			if _, _, err := c.Read(context.Background()); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	h := http.Header{}
	h.Set("Cookie", sessionCookie+"="+tok)
	c, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	for i := 0; i < 50 && s.conns.count(rec.ID) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if s.conns.count(rec.ID) != 1 {
		t.Fatal("WebSocket not registered under its session")
	}
	if _, err := s.sessions.revoke(rec.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("WebSocket of a signed-out browser still open")
	}
}

// #2: nothing changes when the change cannot be saved.
func TestSessionChangesNeedTheDisk(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs a read-only directory")
	}
	s := newTestServer(t)
	tok, rec, _ := s.sessions.create(nil, "token")
	os.Chmod(s.sessions.dir, 0o500)
	defer os.Chmod(s.sessions.dir, 0o700)
	if ok, err := s.sessions.revoke(rec.ID); err == nil || ok {
		t.Fatal("revoke reported success without saving")
	}
	if _, ok, _ := s.sessions.lookup(tok); !ok {
		t.Fatal("memory changed although the revocation was not saved")
	}
	if _, _, err := s.sessions.create(nil, "token"); err == nil {
		t.Fatal("create reported success without saving")
	}
	if n := len(s.sessions.list()); n != 1 {
		t.Fatalf("unsaved session kept in memory (%d)", n)
	}
	lr := httptest.NewRequest("POST", "/api/logout", nil)
	lr.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	w := httptest.NewRecorder()
	s.handleLogout(w, lr)
	if w.Code == 200 {
		t.Fatal("logout said ok although it was not saved")
	}
}

// Full: a new sign-in is refused instead of pushing a signed-in browser out.
func TestFullSessionTableRefusesNewSignIn(t *testing.T) {
	s3 := newTestServer(t)
	first, _, _ := s3.sessions.create(nil, "token")
	for i := 1; i < maxSessions; i++ {
		if _, _, err := s3.sessions.create(nil, "token"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s3.sessions.create(nil, "token"); err != errSessionsFull {
		t.Fatalf("65th sign-in: %v", err)
	}
	if _, ok, _ := s3.sessions.lookup(first); !ok {
		t.Fatal("a signed-in browser was pushed out")
	}
}

// #4: a sign-in checking the old token cannot finish after Sign Out All.
func TestResetAndSignInDoNotInterleave(t *testing.T) {
	s := newTestServer(t)
	t.Setenv("HOME", t.TempDir())
	os.MkdirAll(configDir(), 0o700)
	old := s.token
	s.credMu.Lock() // Sign Out All in progress
	got := make(chan int, 1)
	go func() {
		r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"token":"`+old+`"}`))
		w := httptest.NewRecorder()
		s.handleLogin(w, r)
		got <- w.Code
	}()
	time.Sleep(100 * time.Millisecond)
	select {
	case c := <-got:
		t.Fatalf("sign-in finished during the reset (%d)", c)
	default:
	}
	if err := s.rotateToken(); err != nil {
		t.Fatal(err)
	}
	s.credMu.Unlock()
	if c := <-got; c != http.StatusUnauthorized {
		t.Fatalf("old token signed in after the reset: %d", c)
	}
}

// #5: a window cannot be opened for, or used by, a signed-out browser.
func TestWebAppWindowNeedsLiveOwner(t *testing.T) {
	s := newTestServer(t)
	s.webapps.ownerAlive = s.sessions.has
	_, rec, _ := s.sessions.create(nil, "token")
	s.webapps.sessions["cap1"] = webSession{app: "a", created: time.Now(), owner: rec.ID}
	s.sessions.revoke(rec.ID) // no onRevoke: as if the revocation raced the open
	w := httptest.NewRecorder()
	s.webapps.handleCap(w, httptest.NewRequest("GET", "/x/cap1/index.html", nil))
	if w.Code != http.StatusGone {
		t.Fatalf("window of a signed-out browser answered %d", w.Code)
	}
}

// #6: a ticket and the Zagent session follow the session that asked.
func TestZagentSessionEndsWithItsParent(t *testing.T) {
	s := newTestServer(t)
	_, parent, _ := s.sessions.create(nil, "token")
	tk := s.zagent.issue(parent.ID)
	r := httptest.NewRequest("GET", "http://zagent.local"+zagentHandoffPath+"?t="+tk, nil)
	r.Host = zagentHost
	w := httptest.NewRecorder()
	s.zagentWebHost(http.NotFoundHandler()).ServeHTTP(w, r)
	var child string
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			child = c.Value
		}
	}
	if child == "" {
		t.Fatal("no Zagent session")
	}
	s.sessions.revoke(parent.ID)
	if _, ok, _ := s.sessions.lookup(child); ok {
		t.Fatal("Zagent session outlived the session it came from")
	}
	// a ticket issued before the parent was signed out is worthless
	_, p2, _ := s.sessions.create(nil, "token")
	tk2 := s.zagent.issue(p2.ID)
	s.sessions.revoke(p2.ID)
	r2 := httptest.NewRequest("GET", "http://zagent.local"+zagentHandoffPath+"?t="+tk2, nil)
	r2.Host = zagentHost
	w2 := httptest.NewRecorder()
	s.zagentWebHost(http.NotFoundHandler()).ServeHTTP(w2, r2)
	for _, c := range w2.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			t.Fatal("a ticket of a signed-out session still signed in")
		}
	}
	// not signed in: back to ZeroDesk on the same port, then back here
	r3 := httptest.NewRequest("GET", "http://zagent.local:8686/", nil)
	r3.Host = "zagent.local:8686"
	w3 := httptest.NewRecorder()
	s.zagentWebHost(http.NotFoundHandler()).ServeHTTP(w3, r3)
	if loc := w3.Header().Get("Location"); loc != "http://cardputerzero.local:8686/api/zagent/handoff" {
		t.Fatalf("redirect %q", loc)
	}
	r4 := httptest.NewRequest("GET", "/api/zagent/handoff", nil)
	w4 := httptest.NewRecorder()
	s.handleZagentHandoff(w4, r4)
	if loc := w4.Header().Get("Location"); loc != "/?after=zagent" {
		t.Fatalf("not signed in: redirect %q", loc)
	}
}

// #8: parallel guesses from one address cannot share the last attempt.
func TestLimiterReservesAtomically(t *testing.T) {
	l := newAttemptLimiter(3, time.Minute)
	var ok int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g, _, _ := l.reserve("ip", time.Now()); g {
				atomic.AddInt32(&ok, 1)
			}
		}()
	}
	wg.Wait()
	if ok != 3 {
		t.Fatalf("%d attempts got through, want 3", ok)
	}
	l2 := newAttemptLimiter(3, time.Minute)
	l2.maxKeys = 10
	for i := 0; i < 100; i++ {
		l2.reserve(strings.Repeat("k", i+1), time.Now())
	}
	if len(l2.hits) > 10 {
		t.Fatalf("limiter tracks %d addresses, cap 10", len(l2.hits))
	}
	_, _, slot := l2.reserve("x", time.Now())
	l2.refund("x", slot)
	if len(l2.hits["x"]) != 0 {
		t.Fatal("refund kept the attempt")
	}
}

// #9: Origin must match scheme, host and port; conflicting signals fail.
func TestOriginExactMatch(t *testing.T) {
	cases := []struct {
		host   string
		hdr    map[string]string
		wantOK bool
	}{
		{"cardputerzero.local", map[string]string{"Origin": "https://cardputerzero.local"}, false},
		{"cardputerzero.local:8686", map[string]string{"Origin": "http://cardputerzero.local:8687"}, false},
		{"cardputerzero.local", map[string]string{"Origin": "http://cardputerzero.local:80"}, true},
		{"cardputerzero.local", map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://evil.example"}, false},
		{"zd.example.com", map[string]string{"Origin": "https://zd.example.com", "X-Forwarded-Proto": "https"}, true},
		{"cardputerzero.local", map[string]string{"Origin": "null"}, false},
	}
	for i, c := range cases {
		r := httptest.NewRequest("POST", "/api/logout", nil)
		r.Host = c.host
		for k, v := range c.hdr {
			r.Header.Set(k, v)
		}
		if got := sameOrigin(r); got != c.wantOK {
			t.Errorf("case %d (%v): got %v", i, c.hdr, got)
		}
	}
	s := newTestServer(t)
	h := s.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	r := httptest.NewRequest("POST", "http://zagent.local/x/abc/api/chat", nil)
	r.Host = zagentHost
	r.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("/x/ exemption applied on zagent.local: %d", w.Code)
	}
	hp := &hostPolicy{path: t.TempDir() + "/h.json"}
	hp.set([]string{"203.0.113.9"})
	if !hp.allowed("203.0.113.9") {
		t.Fatal("an address added in Settings is refused")
	}
}

// #10, #11: one root command, no cached sudo, checksum re-checked by root.
func TestStoreRootInstallCommand(t *testing.T) {
	cmd := storeRootInstallCmd(context.Background(), "/home/pi/.cache/x.deb", strings.Repeat("ab", 32), "pw", true)
	args := strings.Join(cmd.Args, " ")
	if !strings.HasPrefix(args, "sudo -k -S -p ") || cmd.Stdin == nil {
		t.Fatalf("password run must not leave cached credentials: %s", args)
	}
	if a := strings.Join(storeRootInstallCmd(context.Background(), "/x.deb", strings.Repeat("ab", 32), "", false).Args, " "); !strings.HasPrefix(a, "sudo -n sh") {
		t.Fatalf("passwordless sudo: %s", a)
	}
	if !strings.Contains(args, "sha256sum -c") || !strings.Contains(args, "mktemp -d") {
		t.Fatalf("root does not check its own copy: %s", args)
	}
	if validSHA256("") || validSHA256("xyz") || !validSHA256(strings.Repeat("aB", 32)) {
		t.Fatal("validSHA256")
	}
	h := http.Header{}
	h.Add("Cookie", "zd_session =a; theirs=1")
	withoutZeroDeskCredentials(h)
	if got := h.Get("Cookie"); got != "theirs=1" {
		t.Fatalf("cookie forwarded: %q", got)
	}
}
