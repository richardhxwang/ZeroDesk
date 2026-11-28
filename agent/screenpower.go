package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// S on the device screen: the screen runs as the device user, like the
// service, so it can read the service's token and ask it to shut down. It
// cannot start a system service itself (that needs root or a polkit rule,
// which Store packages may not install), so the package ships
// zerodesk-cp0.path: a system unit that starts zerodesk-cp0.service when
// /run/zerodesk-cp0/start changes, in a directory the device user owns
// (packaging/zerodesk-cp0.tmpfiles). A development install is a user unit and
// is started directly.

var (
	startTrigger = "/run/zerodesk-cp0/start"
	pathUnitFile = "/usr/lib/systemd/system/zerodesk-cp0.path"
	agentLocal   = "http://127.0.0.1:8686"
)

func stopZeroDesk() error {
	home, _ := os.UserHomeDir()
	tok, err := os.ReadFile(filepath.Join(home, ".config", "zerodesk", "token"))
	if err != nil {
		return fmt.Errorf("shut down: %w", err)
	}
	req, _ := http.NewRequest("POST", agentLocal+"/api/zerodesk/power", bytes.NewReader([]byte(`{"action":"shutdown"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ZeroDesk-Token", strings.TrimSpace(string(tok)))
	res, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("shut down: %w", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("shut down: %s", res.Status)
	}
	return nil
}

func startZeroDesk() error {
	if _, err := os.Stat(pathUnitFile); err == nil {
		// any write that is closed again triggers the path unit
		stamp := []byte(time.Now().UTC().Format(time.RFC3339Nano) + "\n")
		if err := os.WriteFile(startTrigger, stamp, 0o644); err != nil {
			return fmt.Errorf("start: %w", err)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "systemctl", "--user", "start", "zerodesk.service").CombinedOutput(); err != nil {
		return fmt.Errorf("start: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
