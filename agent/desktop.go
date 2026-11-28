package main

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleWallpaper serves the wallpaper of the device's own Linux desktop, so
// the web desktop looks like the device. pcmanfm keeps it per profile in
// desktop-items-<n>.conf; the system default is the fallback.
func handleWallpaper(w http.ResponseWriter, r *http.Request) {
	p := findWallpaper()
	if p == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "max-age=86400")
	http.ServeFile(w, r, p)
}

func findWallpaper() string {
	home, _ := os.UserHomeDir()
	var confs []string
	for _, base := range []string{filepath.Join(home, ".config"), "/etc/xdg"} {
		m, _ := filepath.Glob(filepath.Join(base, "pcmanfm*", "*", "desktop-items-*.conf"))
		confs = append(confs, m...)
	}
	for _, c := range confs {
		f, err := os.Open(c)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "wallpaper="); ok {
				if st, err := os.Stat(v); err == nil && !st.IsDir() {
					f.Close()
					return v
				}
			}
		}
		f.Close()
	}
	for _, pattern := range []string{"/usr/share/rpd-wallpaper/*.jpg", "/usr/share/rpd-wallpaper/*.png"} {
		if m, _ := filepath.Glob(pattern); len(m) > 0 {
			return m[0]
		}
	}
	return ""
}

// handleLogout ends this browser's session on the server, not just its cookie.
func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && s.sessions != nil {
		if err := s.sessions.revokeToken(c.Value); err != nil {
			// not saved: the session would come back after a restart, so
			// say so instead of pretending this browser is signed out
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not sign out: " + err.Error()})
			return
		}
	}
	clearCookie(w, sessionCookie)
	clearCookie(w, legacyCookie)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
