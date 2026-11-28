package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const maxUpload = 2 << 30 // 2 GiB per request

type fileEntry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Dir      bool      `json:"dir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Link     bool      `json:"link,omitempty"`
	Hidden   bool      `json:"hidden,omitempty"`
}

// cleanPath resolves the request path to an absolute, clean path.
// ZeroDesk acts as the device owner (user pi), so any path that user can
// reach is allowed; the kernel enforces permissions.
func cleanPath(p string) (string, error) {
	if p == "" || p == "~" {
		home, _ := os.UserHomeDir()
		return home, nil
	}
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, p[2:])
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("path must be absolute")
	}
	return filepath.Clean(p), nil
}

func fsError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, os.ErrNotExist):
		status = http.StatusNotFound
	case errors.Is(err, os.ErrPermission):
		status = http.StatusForbidden
	case errors.Is(err, os.ErrExist):
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func handleFsList(w http.ResponseWriter, r *http.Request) {
	dir, err := cleanPath(r.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		fsError(w, err)
		return
	}
	out := make([]fileEntry, 0, len(ents))
	for _, e := range ents {
		full := filepath.Join(dir, e.Name())
		info, err := os.Stat(full) // follow links for size/dir
		link := e.Type()&os.ModeSymlink != 0
		if err != nil {
			info, err = e.Info()
			if err != nil {
				continue
			}
		}
		out = append(out, fileEntry{
			Name:     e.Name(),
			Path:     full,
			Dir:      info.IsDir(),
			Size:     info.Size(),
			Modified: info.ModTime(),
			Link:     link,
			Hidden:   strings.HasPrefix(e.Name(), "."),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{"path": dir, "entries": out})
}

func handleFsGet(w http.ResponseWriter, r *http.Request) {
	setFileSafetyHeaders(w.Header()) // on every answer, errors included
	p, err := cleanPath(r.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	f, err := os.Open(p)
	if err != nil {
		fsError(w, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a file"})
		return
	}
	if ct := mime.TypeByExtension(filepath.Ext(p)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	disp := "inline"
	if r.URL.Query().Get("download") == "1" || activeContent(p) {
		disp = "attachment"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename*=UTF-8''%s", disp, urlPathEscape(filepath.Base(p))))
	http.ServeContent(w, r, filepath.Base(p), st.ModTime(), f)
}

// activeContent: files a browser would run as a page (HTML, SVG, XML,
// scripts). Opened from a link they would run with ZeroDesk's origin and its
// sign-in, so they are always downloaded. <img>, <audio>, <video> and fetch()
// ignore Content-Disposition, so previews still work.
func activeContent(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".html", ".htm", ".xhtml", ".shtml", ".svg", ".svgz", ".xml", ".xsl", ".xslt", ".js", ".mjs":
		return true
	}
	return false
}

// setFileSafetyHeaders makes a served file inert as a page: no type
// sniffing, and if a browser shows it anyway it runs in an opaque sandbox
// with no scripts.
func setFileSafetyHeaders(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'")
}

func urlPathEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.IndexByte("-._~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// handleFsUpload streams multipart files into ?dir= without buffering them
// in memory (the device has ~100 MB free RAM).
func handleFsUpload(w http.ResponseWriter, r *http.Request) {
	dir, err := cleanPath(r.URL.Query().Get("dir"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var saved []string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		name := part.FileName()
		if name == "" {
			continue
		}
		// browsers send folder uploads as relative paths; keep the tree but
		// never let it escape the target directory
		rel := filepath.Clean("/" + filepath.ToSlash(name))[1:]
		dst := filepath.Join(dir, rel)
		if !strings.HasPrefix(dst, dir+string(os.PathSeparator)) && dst != dir {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			fsError(w, err)
			return
		}
		tmp := dst + ".zerodesk-part"
		f, err := os.Create(tmp)
		if err != nil {
			fsError(w, err)
			return
		}
		_, err = io.Copy(f, part)
		cerr := f.Close()
		if err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(tmp)
			fsError(w, err)
			return
		}
		if err := os.Rename(tmp, dst); err != nil {
			os.Remove(tmp)
			fsError(w, err)
			return
		}
		saved = append(saved, dst)
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": saved})
}

func handleFsOp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Op   string `json:"op"` // mkdir | trash | delete | rename
		Path string `json:"path"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	p, err := cleanPath(body.Path)
	if err != nil || p == "/" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad path"})
		return
	}
	switch body.Op {
	case "mkdir":
		err = os.Mkdir(p, 0o755)
	case "trash", "delete":
		home, _ := os.UserHomeDir()
		if p == home || p == trashDir() || strings.HasPrefix(p, trashDir()+"/") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "refusing to delete this folder"})
			return
		}
		if body.Op == "delete" {
			err = os.RemoveAll(p)
			break
		}
		if _, terr := moveToTrash(p); terr != nil {
			// e.g. a USB stick (different filesystem): the UI offers to delete instead
			if errors.Is(terr, syscall.EXDEV) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot move to trash", "code": "cross_device"})
				return
			}
			err = terr
		}
	case "rename":
		to, perr := cleanPath(body.To)
		if perr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad target"})
			return
		}
		if _, statErr := os.Lstat(to); statErr == nil {
			err = os.ErrExist
		} else {
			err = os.Rename(p, to)
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown op"})
		return
	}
	if err != nil {
		fsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
