package main

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// The user's own desktop picture. The browser scales a chosen photo down
// before sending it (the device never decodes images), so one stored file is
// small; it lives in ~/.local/share/zerodesk/wallpaper/custom and is shared by
// every browser that signs in. Which wallpaper is shown stays a per-browser
// preference.

const maxWallpaper = 12 << 20

var (
	wallpaperMu  sync.Mutex
	wallpaperDir = func() string {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "share", "zerodesk", "wallpaper")
	}
)

// wallpaperTypes are the formats every browser shows as a background.
var wallpaperTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

func customWallpaperPath() string { return filepath.Join(wallpaperDir(), "custom") }

func handleCustomWallpaperGet(w http.ResponseWriter, r *http.Request) {
	f, err := os.Open(customWallpaperPath())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	head := make([]byte, 512)
	k, _ := io.ReadFull(f, head)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(head[:k]))
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// handleCustomWallpaperPut stores the request body (a JPEG, PNG or WebP) as
// the custom wallpaper. It streams to a temporary file and renames it into
// place, so a failed upload leaves the previous picture.
func handleCustomWallpaperPut(w http.ResponseWriter, r *http.Request) {
	body := bufio.NewReaderSize(http.MaxBytesReader(w, r.Body, maxWallpaper), 512)
	head, _ := body.Peek(512)
	if len(head) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty image"})
		return
	}
	if !wallpaperTypes[http.DetectContentType(head)] {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "use a JPEG, PNG or WebP image"})
		return
	}

	wallpaperMu.Lock()
	defer wallpaperMu.Unlock()
	dir := wallpaperDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	_, err = io.Copy(tmp, body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "image too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := os.Rename(tmp.Name(), customWallpaperPath()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	st, err := os.Stat(customWallpaperPath())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"rev": st.ModTime().UnixMilli()})
}

func handleCustomWallpaperDelete(w http.ResponseWriter, r *http.Request) {
	wallpaperMu.Lock()
	defer wallpaperMu.Unlock()
	if err := os.Remove(customWallpaperPath()); err != nil && !os.IsNotExist(err) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
