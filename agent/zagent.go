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
	"time"
	"unicode/utf8"
)

// Settings of the Zagent agent client. The device tool zagent-settings
// (ZeroAgent repo, tools/zagent-settings) owns
// reading and writing them; these handlers only call its three read/write
// subcommands with validated arguments.

// Standalone Zagent installs its tools under /usr/share/zagent/bin (ZeroAgent
// docs/standalone.md); earlier builds put this one in /usr/local/bin.
var zagentSettingsTool = func() string {
	for _, p := range []string{"/usr/share/zagent/bin/zagent-settings", "/usr/local/bin/zagent-settings"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/usr/share/zagent/bin/zagent-settings"
}()

var zagentKeyRe = regexp.MustCompile(`^[a-z_]{1,40}$`)

// zagentLang is the language ZeroDesk's window asks for (?lang=en|zh), so
// the rows come in ZeroDesk's language rather than the one chosen on the
// device for Zagent's own screens. Zagent has English and Chinese only.
func zagentLang(r *http.Request) string {
	switch l := r.URL.Query().Get("lang"); l {
	case "en", "zh":
		return l
	}
	return ""
}

// runZagent runs zagent-settings. lang ("" = Zagent's own choice) is passed
// as ZAGENT_LANG; a zagent-settings that predates it ignores it and answers
// in its own language, which the window then follows so nothing mixes.
func runZagent(lang string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, zagentSettingsTool, args...)
	if lang != "" {
		cmd.Env = append(os.Environ(), "ZAGENT_LANG="+lang)
	}
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

// GET /api/zagent/settings -> {"lang":..,"sections":[{id,title,rows:[...]}]}
func handleZagentSettings(w http.ResponseWriter, r *http.Request) {
	out, err := runZagent(zagentLang(r), 45*time.Second, "json")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if !json.Valid([]byte(out)) {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "zagent-settings returned invalid JSON"})
		return
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
	out, err := runZagent(zagentLang(r), 45*time.Second, "options", key)
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
	out, err := runZagent(zagentLang(r), 60*time.Second, "set", body.Key, body.Value)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"value": out})
}
