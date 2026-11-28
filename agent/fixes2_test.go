package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// r2#1: a sign-out between the sign-in check and the registration of a
// long-lived connection is still caught.
func TestSignOutBetweenAuthAndRegistration(t *testing.T) {
	s := newTestServer(t)
	tok, rec, _ := s.sessions.create(nil, "token")
	testHookAfterAuth = func() { s.sessions.revoke(rec.ID) }
	defer func() { testHookAfterAuth = nil }()
	ran := false
	h := s.auth(func(w http.ResponseWriter, r *http.Request) { ran = true })
	r := httptest.NewRequest("GET", "/ws/term", nil)
	r.Header.Set("Upgrade", "websocket")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	w := httptest.NewRecorder()
	h(w, r)
	if ran || w.Code != http.StatusUnauthorized {
		t.Fatalf("connection of a browser signed out meanwhile was served (%d)", w.Code)
	}
}

// r2#1: a session that expires closes what it opened.
func TestExpiredSessionClosesConnections(t *testing.T) {
	s := newTestServer(t)
	s.sessions.onRevoke = func(id string) { s.conns.closeOwner(id) }
	now := time.Now()
	s.sessions.now = func() time.Time { return now }
	_, rec, _ := s.sessions.create(nil, "token")
	r := httptest.NewRequest("GET", "/ws/term", nil)
	r.Header.Set("Upgrade", "websocket")
	_, r2, done := s.conns.track(rec.ID, httptest.NewRecorder(), r)
	defer done()
	s.sessions.now = func() time.Time { return now.Add(sessionTTL + time.Minute) }
	s.sessions.sweepExpired()
	select {
	case <-r2.Context().Done():
	default:
		t.Fatal("connection of an expired session still open")
	}
}

// r2#5: a sign-out between the window check and its registration is caught.
func TestCapSignOutBetweenCheckAndRegistration(t *testing.T) {
	s := newTestServer(t)
	s.webapps.ownerAlive = s.sessions.has
	s.webapps.track = s.conns.trackAll
	_, rec, _ := s.sessions.create(nil, "token")
	s.webapps.sessions["cap1"] = webSession{app: "a", created: time.Now(), owner: rec.ID}
	testHookAfterAuth = func() { s.sessions.revoke(rec.ID) }
	defer func() { testHookAfterAuth = nil }()
	w := httptest.NewRecorder()
	s.webapps.handleCap(w, httptest.NewRequest("GET", "/x/cap1/api/stream", nil))
	if w.Code != http.StatusGone {
		t.Fatalf("window of a browser signed out meanwhile answered %d", w.Code)
	}
}

// r2#6: a Zagent session works only on zagent.local and only while its
// parent is signed in (also when the parent simply expires).
func TestZagentSessionScope(t *testing.T) {
	s := newTestServer(t)
	now := time.Now()
	s.sessions.now = func() time.Time { return now }
	_, parent, _ := s.sessions.create(nil, "token")
	tok, child, err := s.sessions.createWith(nil, "zagent", parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	at := func(host string) bool {
		r := httptest.NewRequest("GET", "http://"+host+"/", nil)
		r.Host = host
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		_, ok := s.authenticate(httptest.NewRecorder(), r)
		return ok
	}
	if !at(zagentHost) {
		t.Fatal("Zagent session refused on zagent.local")
	}
	if at("cardputerzero.local") {
		t.Fatal("Zagent session accepted by ZeroDesk's own API")
	}
	// the parent ages out while the child keeps being used
	s.sessions.byHash[parent.Hash].ExpiresAt = now.Add(-time.Second)
	if at(zagentHost) {
		t.Fatal("Zagent session outlived its expired parent")
	}
	_ = child
}

// r2#7: the cooldown counts from when the prompt ended; a full table refuses.
func TestPairCooldownFromEnd(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	s := newTestServer(t)
	// a prompt that started long ago ends now: the cooldown restarts now
	s.pair.lastPrompt = map[string]time.Time{"192.168.1.20": time.Now().Add(-time.Hour)}
	ctx, cancel := context.WithCancel(context.Background())
	s.runPairing(ctx, cancel, "id1", "1234", "192.168.1.20", "en", pairConfirmCode)
	s.pair.mu.Lock()
	last := s.pair.lastPrompt["192.168.1.20"]
	s.pair.mu.Unlock()
	if time.Since(last) > time.Second {
		t.Fatal("cooldown still counts from the start of the prompt")
	}
	s.pair.mu.Lock()
	for i := 0; i < 4096; i++ {
		s.pair.lastPrompt[strings.Repeat("x", i+1)] = time.Now()
	}
	s.pair.mu.Unlock()
	r2 := httptest.NewRequest("POST", "/api/pair/start", strings.NewReader(`{}`))
	r2.RemoteAddr = "192.168.1.99:1"
	w := httptest.NewRecorder()
	s.handlePairStart(w, r2)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("full cooldown table let a new address prompt: %d", w.Code)
	}
}

// r2#8: a full table refuses new addresses and forgets nobody in-window.
func TestLimiterFullTableForgetsNobody(t *testing.T) {
	l := newAttemptLimiter(1, time.Minute)
	l.maxKeys = 10
	for i := 0; i < 10; i++ {
		l.reserve(strings.Repeat("k", i+1), time.Now())
	}
	if ok, _, _ := l.reserve("new", time.Now()); ok {
		t.Fatal("full table took a new address")
	}
	if ok, _, _ := l.reserve("k", time.Now()); ok {
		t.Fatal("an address still in its window got its attempts back")
	}
}

// r2#9: an Upgrade header listing several protocols is still a WebSocket.
func TestUpgradeTokenList(t *testing.T) {
	s := newTestServer(t)
	h := s.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	r := httptest.NewRequest("GET", "http://cardputerzero.local/ws/term", nil)
	r.Host = "cardputerzero.local"
	r.Header.Set("Upgrade", "h2c, websocket")
	r.Header.Set("Origin", "https://cardputerzero.local")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("WebSocket with a protocol list skipped the Origin check: %d", w.Code)
	}
}

// new#1: Sign Out All from a script signs every browser out.
func TestScriptResetSignsEveryoneOut(t *testing.T) {
	s := newTestServer(t)
	t.Setenv("HOME", t.TempDir())
	os.MkdirAll(configDir(), 0o700)
	s.sessions.create(nil, "token")
	s.sessions.create(nil, "pair")
	w := httptest.NewRecorder()
	s.handleSessionsReset(w, httptest.NewRequest("POST", "/api/sessions/reset", nil))
	if w.Code != 200 || len(s.sessions.list()) != 0 {
		t.Fatalf("script reset left %d sessions (%d)", len(s.sessions.list()), w.Code)
	}
}
