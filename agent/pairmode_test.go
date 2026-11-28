package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// In "press OK" mode OK allows and Esc denies; digits do nothing.
func TestCodeEntryOKOnly(t *testing.T) {
	e := &codeEntry{want: "1234", okOnly: true}
	if done, _, _ := e.key(2); done {
		t.Fatal("a digit answered the prompt")
	}
	if done, ok, _ := e.key(keyEnter); !done || !ok {
		t.Fatal("OK did not allow")
	}
	e = &codeEntry{want: "1234", okOnly: true}
	if done, ok, _ := e.key(keyEsc); !done || ok {
		t.Fatal("Esc did not deny")
	}
	// the default still needs the code
	e = &codeEntry{want: "1234"}
	if done, _, _ := e.key(keyEnter); done {
		t.Fatal("OK without the code answered the prompt")
	}
}

// The setting is stored, defaults to typing the code, rejects other values,
// and the browser is told which one applies.
func TestPairingSetting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	os.MkdirAll(configDir(), 0o700)
	if loadPairSettings().Confirm != pairConfirmCode {
		t.Fatal("default is not typing the code")
	}
	s := newTestServer(t)
	_, rec, _ := s.sessions.create(nil, "token")
	put := func(body string) int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("PUT", "/api/pairing", strings.NewReader(body))
		s.handlePairingPut(w, r.WithContext(withSession(r.Context(), rec)))
		return w.Code
	}
	// the access token alone (a script, no browser session) cannot change it
	w0 := httptest.NewRecorder()
	s.handlePairingPut(w0, httptest.NewRequest("PUT", "/api/pairing", strings.NewReader(`{"confirm":"ok"}`)))
	if w0.Code != http.StatusForbidden || loadPairSettings().Confirm != pairConfirmCode {
		t.Fatalf("changed without a browser session: %d", w0.Code)
	}
	if c := put(`{"confirm":"anything"}`); c != http.StatusBadRequest {
		t.Fatalf("bad value accepted: %d", c)
	}
	if c := put(`{"confirm":"ok"}`); c != 200 || loadPairSettings().Confirm != pairConfirmOK {
		t.Fatalf("setting not saved: %d", c)
	}
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	r := httptest.NewRequest("POST", "/api/pair/start", strings.NewReader(`{}`))
	r.RemoteAddr = "192.168.1.30:1"
	w := httptest.NewRecorder()
	s.handlePairStart(w, r)
	if !strings.Contains(w.Body.String(), `"confirm":"ok"`) {
		t.Fatalf("browser not told the mode: %s", w.Body.String())
	}
}

// The mode a request started with stays for that request.
func TestPairModeFixedPerRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	os.MkdirAll(configDir(), 0o700)
	writeJSONFile(configDir()+"/"+pairingFile, pairSettings{Confirm: pairConfirmOK})
	s := newTestServer(t)
	r := httptest.NewRequest("POST", "/api/pair/start", strings.NewReader(`{}`))
	r.RemoteAddr = "192.168.1.40:1"
	w := httptest.NewRecorder()
	s.handlePairStart(w, r)
	writeJSONFile(configDir()+"/"+pairingFile, pairSettings{Confirm: pairConfirmCode})
	s.pair.mu.Lock()
	got := s.pair.confirm
	s.pair.mu.Unlock()
	if !strings.Contains(w.Body.String(), `"confirm":"ok"`) || got != pairConfirmOK {
		t.Fatalf("request mode %q, browser told %s", got, w.Body.String())
	}
}
