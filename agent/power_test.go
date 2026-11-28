package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fakePower(t *testing.T) (exits chan int, restarts chan struct{}) {
	t.Helper()
	exits = make(chan int, 1)
	restarts = make(chan struct{}, 1)
	oldExit, oldRestart, oldDelay := powerExit, powerRestart, powerDelay
	powerExit = func(code int) { exits <- code }
	powerRestart = func() { restarts <- struct{}{} }
	powerDelay = time.Millisecond
	t.Cleanup(func() { powerExit, powerRestart, powerDelay = oldExit, oldRestart, oldDelay })
	return
}

func TestPowerShutdownExitsCleanly(t *testing.T) {
	exits, restarts := fakePower(t)
	rec := httptest.NewRecorder()
	handleZeroDeskPower(rec, httptest.NewRequest("POST", "/api/zerodesk/power", strings.NewReader(`{"action":"shutdown"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	select {
	case code := <-exits:
		// 0: Restart=on-failure must not bring it back
		if code != 0 {
			t.Fatalf("exit code %d, want 0", code)
		}
	case <-restarts:
		t.Fatal("restarted instead of shutting down")
	case <-time.After(time.Second):
		t.Fatal("did not exit")
	}
}

func TestPowerRestart(t *testing.T) {
	exits, restarts := fakePower(t)
	rec := httptest.NewRecorder()
	handleZeroDeskPower(rec, httptest.NewRequest("POST", "/api/zerodesk/power", strings.NewReader(`{"action":"restart"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	select {
	case <-restarts:
	case code := <-exits:
		t.Fatalf("exited with %d instead of restarting", code)
	case <-time.After(time.Second):
		t.Fatal("did not restart")
	}
}

func TestPowerRejectsOtherActions(t *testing.T) {
	exits, restarts := fakePower(t)
	for _, body := range []string{`{"action":"reboot"}`, `{}`, `nope`} {
		rec := httptest.NewRecorder()
		handleZeroDeskPower(rec, httptest.NewRequest("POST", "/api/zerodesk/power", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: got %d, want 400", body, rec.Code)
		}
	}
	select {
	case <-exits:
		t.Fatal("exited")
	case <-restarts:
		t.Fatal("restarted")
	case <-time.After(50 * time.Millisecond):
	}
}
