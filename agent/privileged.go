package main

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"sync"
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
// The GitHub edition skips the password when the device allows sudo without
// one; the Store edition always asks.

var (
	errNeedPassword  = errors.New("password required")
	errWrongPassword = errors.New("wrong password")
)

func sudoWithoutPassword(ctx context.Context) bool {
	if edition == "store" {
		return false
	}
	return exec.CommandContext(ctx, "sudo", "-n", "true").Run() == nil
}

// withAdmin runs fn while `sudo -n` works: either because the device allows
// it (GitHub edition) or because password was just verified.
// adminMu runs one administrator action at a time: each one clears sudo's
// cached credential when it ends, which would cut another one short.
var adminMu sync.Mutex

func withAdmin(ctx context.Context, password string, fn func() error) error {
	adminMu.Lock()
	defer adminMu.Unlock()
	if sudoWithoutPassword(ctx) {
		return fn()
	}
	if password == "" {
		return errNeedPassword
	}
	// Clear any cached credential first, so the password is really checked,
	// then validate it. `-v` must run without `-k`: with `-k` sudo checks the
	// password but does not record it, and the `sudo -n` in fn is refused.
	_ = exec.CommandContext(ctx, "sudo", "-K").Run()
	v := exec.CommandContext(ctx, "sudo", "-S", "-v", "-p", "")
	v.Stdin = strings.NewReader(password + "\n")
	if err := v.Run(); err != nil {
		return errWrongPassword
	}
	defer exec.Command("sudo", "-K").Run()
	return fn()
}

// checkAdmin checks the password without leaving sudo's credential cached
// (-k with -v validates but does not record), so slow work such as a
// download can run before withAdmin opens the short window for the step
// that needs root.
func checkAdmin(ctx context.Context, password string) error {
	if sudoWithoutPassword(ctx) {
		return nil
	}
	if password == "" {
		return errNeedPassword
	}
	v := exec.CommandContext(ctx, "sudo", "-k", "-S", "-v", "-p", "")
	v.Stdin = strings.NewReader(password + "\n")
	if err := v.Run(); err != nil {
		return errWrongPassword
	}
	return nil
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
