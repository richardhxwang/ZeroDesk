package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Settings of the Zagent agent client. The device tool
// /usr/local/bin/zagent-settings (ZeroAgent repo, tools/zagent-settings) owns
// reading and writing them; these handlers only call its three read/write
// subcommands with validated arguments.

const zagentSettingsTool = "/usr/local/bin/zagent-settings"

var zagentKeyRe = regexp.MustCompile(`^[a-z_]{1,40}$`)

func runZagent(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, zagentSettingsTool, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", &zagentError{msg}
	}
	return strings.TrimSpace(stdout.String()), nil
}

type zagentError struct{ msg string }

func (e *zagentError) Error() string { return e.msg }

// zagent-settings takes seconds per call, so the last answer is kept and
// served at once (a window's first paint), then refreshed in the background.
var zagentCache struct {
	sync.Mutex
	out        string
	at         time.Time
	refreshing bool
}

func zagentFetch() (string, error) {
	out, err := runZagent(45*time.Second, "json")
	if err != nil {
		return "", err
	}
	if !json.Valid([]byte(out)) {
		return "", &zagentError{"zagent-settings returned invalid JSON"}
	}
	zagentCache.Lock()
	zagentCache.out, zagentCache.at = out, time.Now()
	zagentCache.Unlock()
	return out, nil
}

// zagentWarm fills the cache at start-up when the tool is installed.
func zagentWarm() {
	if _, err := os.Stat(zagentSettingsTool); err == nil {
		_, _ = zagentFetch()
	}
}

// GET /api/zagent/settings -> {"lang":..,"sections":[{id,title,rows:[...]}]}
func handleZagentSettings(w http.ResponseWriter, _ *http.Request) {
	zagentCache.Lock()
	out, at := zagentCache.out, zagentCache.at
	stale := out != "" && time.Since(at) > 5*time.Second && !zagentCache.refreshing
	if stale {
		zagentCache.refreshing = true
	}
	zagentCache.Unlock()
	if stale {
		go func() {
			_, _ = zagentFetch()
			zagentCache.Lock()
			zagentCache.refreshing = false
			zagentCache.Unlock()
		}()
	}
	if out == "" {
		var err error
		if out, err = zagentFetch(); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(out))
}

// GET /api/zagent/options?key=model -> ["a","b",...]
func handleZagentOptions(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if !zagentKeyRe.MatchString(key) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad key"})
		return
	}
	out, err := runZagent(45*time.Second, "options", key)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	options := []string{}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			options = append(options, line)
		}
	}
	writeJSON(w, http.StatusOK, options)
}

// POST /api/zagent/set {"key":..,"value":..} -> {"value": <display value>}
func handleZagentSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if !zagentKeyRe.MatchString(body.Key) || body.Value == "" || len(body.Value) > 2000 ||
		!utf8.ValidString(body.Value) || strings.ContainsAny(body.Value, "\x00\n\r") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad key or value"})
		return
	}
	out, err := runZagent(60*time.Second, "set", body.Key, body.Value)
	zagentCache.Lock()
	zagentCache.out = ""
	zagentCache.Unlock()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"value": out})
}
