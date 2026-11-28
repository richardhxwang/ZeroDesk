package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"syscall"
	"time"
)

// Shut Down and Restart in ZeroDesk's menu act on ZeroDesk itself, not on the
// CardputerZero (Settings › General › Restart restarts the device).
//
// Shut Down ends the process with status 0. The units restart ZeroDesk only
// on failure, so it stays off until the next boot or until the launcher
// entry starts it again (S on the device screen: packaging/zerodesk-cp0.path).
// Restart replaces the process with a fresh copy of the binary (same PID, so
// systemd keeps tracking it; listening sockets close on exec and are opened
// again).

var (
	powerExit    = os.Exit
	powerRestart = restartSelf
	powerDelay   = 300 * time.Millisecond // lets the reply reach the browser
)

func restartSelf() {
	exe, err := os.Executable()
	if err == nil {
		err = syscall.Exec(exe, os.Args, os.Environ())
	}
	// exec failed: exit with an error so the unit's Restart=on-failure
	// starts ZeroDesk again
	log.Printf("restart: %v", err)
	powerExit(1)
}

func handleZeroDeskPower(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	var act func()
	switch body.Action {
	case "shutdown":
		act = func() {
			log.Printf("shutting down (asked from %s)", r.RemoteAddr)
			powerExit(0)
		}
	case "restart":
		act = func() {
			log.Printf("restarting (asked from %s)", r.RemoteAddr)
			powerRestart()
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "action must be shutdown or restart"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	time.AfterFunc(powerDelay, act)
}
