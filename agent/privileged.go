package main

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// Administrator actions (installing packages, setting the clock, ADB).
//
// ZeroDesk itself never runs as root. Like the system Store (cp0_lvgl's
// sudo prompt), it asks the user for their password, checks it with
// `sudo -S -v`, runs one fixed command with `sudo -n`, and drops the cached
// credential with `sudo -K` right away: this image uses a global sudo
// timestamp, so leaving it would let any process use sudo for 15 minutes.
//
// When the device allows sudo without a password, no password is asked: the
// system Store's own prompt behaves the same way (cp0_sudo runs `sudo -S -v`,
// which passes silently then). Both editions do this.

var (
	errNeedPassword  = errors.New("password required")
	errWrongPassword = errors.New("wrong password")
)

func sudoWithoutPassword(ctx context.Context) bool {
	return exec.CommandContext(ctx, "sudo", "-n", "true").Run() == nil
}

// withAdmin runs fn while `sudo -n` works: either because the device allows
// it or because the password was just verified.
func withAdmin(ctx context.Context, password string, fn func() error) error {
	if sudoWithoutPassword(ctx) {
		return fn()
	}
	if password == "" {
		return errNeedPassword
	}
	v := exec.CommandContext(ctx, "sudo", "-S", "-k", "-v", "-p", "")
	v.Stdin = strings.NewReader(password + "\n")
	if err := v.Run(); err != nil {
		return errWrongPassword
	}
	defer exec.Command("sudo", "-K").Run()
	return fn()
}

// writeAdminError answers errNeedPassword / errWrongPassword so the UI can
// show (again) its password sheet; other errors become 500.
func writeAdminError(w http.ResponseWriter, err error, extra map[string]any) bool {
	if err == nil {
		return false
	}
	body := map[string]any{"error": err.Error()}
	for k, v := range extra {
		body[k] = v
	}
	status := http.StatusInternalServerError
	if errors.Is(err, errNeedPassword) || errors.Is(err, errWrongPassword) {
		body["need_password"] = true
		status = http.StatusForbidden
	}
	writeJSON(w, status, body)
	return true
}

// holdEscape asks the launcher to end the app it started: holding ESC is the
// launcher's own force-quit (SIGTERM, then SIGKILL), done with its rights.
func holdEscape(k *keyInjector, alive func() bool, max time.Duration) {
	if k == nil {
		return
	}
	const keyEsc = 1
	_ = k.send(keyEsc, 1)
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) && alive() {
		time.Sleep(100 * time.Millisecond)
		_ = k.send(keyEsc, 2)
	}
	_ = k.send(keyEsc, 0)
}
