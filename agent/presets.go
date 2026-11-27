package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Downloadable Terminal presets (colour schemes and fonts). The package only
// carries the catalog; the device downloads a preset's files when someone
// picks it in Settings, checks each against the catalog's sha256, keeps them
// in ~/.local/share/zerodesk/presets/<id>/ and serves them to every browser.
// Only catalog URLs are ever fetched: the browser names a preset, not a URL.
// The catalog is made by scripts/presets-catalog.py.

//go:embed presets/catalog.json
var presetsCatalogJSON []byte

type presetFile struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Weight int    `json:"weight,omitempty"`
}

type preset struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// colour schemes: per variant ("light", "dark"), enough colours to draw the tile
	Preview map[string][]string `json:"preview,omitempty"`
	Family  string              `json:"family,omitempty"`
	Nerd    bool                `json:"nerd,omitempty"`
	License string              `json:"license"`
	Source  string              `json:"source"`
	Files   []presetFile        `json:"files,omitempty"`
}

type presetCatalog struct {
	Themes []preset `json:"themes"`
	Fonts  []preset `json:"fonts"`
}

const maxPresetFile = 16 << 20

var (
	presetsOnce  sync.Once
	presetsCat   presetCatalog
	presetsByID  map[string]*preset
	presetLocks  sync.Map // id → *sync.Mutex, one download per preset
	presetClient = &http.Client{Timeout: 3 * time.Minute}
	presetsRoot  = func() string {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "share", "zerodesk", "presets")
	}
)

func loadPresets() {
	presetsOnce.Do(func() {
		if err := json.Unmarshal(presetsCatalogJSON, &presetsCat); err != nil {
			panic("presets/catalog.json: " + err.Error())
		}
		presetsByID = map[string]*preset{}
		for _, list := range [][]preset{presetsCat.Themes, presetsCat.Fonts} {
			for i := range list {
				presetsByID[list[i].ID] = &list[i]
			}
		}
	})
}

func presetInstalled(p *preset) bool {
	for _, f := range p.Files {
		st, err := os.Stat(filepath.Join(presetsRoot(), p.ID, f.Name))
		if err != nil || st.Size() != f.Size {
			return false
		}
	}
	return true
}

func presetSize(p *preset) (n int64) {
	for _, f := range p.Files {
		n += f.Size
	}
	return n
}

// GET /api/presets: the catalog, with what this device already has.
func handlePresetsList(w http.ResponseWriter, r *http.Request) {
	loadPresets()
	type item struct {
		preset
		Size      int64 `json:"size"`
		Installed bool  `json:"installed"`
	}
	conv := func(list []preset) []item {
		out := make([]item, 0, len(list))
		for _, p := range list {
			q := p
			q.Files = nil // URLs and checksums stay on the device
			out = append(out, item{preset: q, Size: presetSize(&p), Installed: presetInstalled(&p)})
		}
		return out
	}
	writeJSON(w, http.StatusOK, map[string]any{"themes": conv(presetsCat.Themes), "fonts": conv(presetsCat.Fonts)})
}

// POST /api/presets/{id}: download the preset's files (once).
func handlePresetFetch(w http.ResponseWriter, r *http.Request) {
	loadPresets()
	p, ok := presetsByID[r.PathValue("id")]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such preset"})
		return
	}
	if err := presetFetch(r.Context(), p); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DELETE /api/presets/{id}: remove the downloaded files.
func handlePresetDelete(w http.ResponseWriter, r *http.Request) {
	loadPresets()
	p, ok := presetsByID[r.PathValue("id")]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such preset"})
		return
	}
	mu, _ := presetLocks.LoadOrStore(p.ID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if err := os.RemoveAll(filepath.Join(presetsRoot(), p.ID)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /api/presets/{id}/{file}: a downloaded file.
func handlePresetFile(w http.ResponseWriter, r *http.Request) {
	loadPresets()
	p, ok := presetsByID[r.PathValue("id")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	name := r.PathValue("file")
	for _, f := range p.Files {
		if f.Name != name {
			continue
		}
		path := filepath.Join(presetsRoot(), p.ID, f.Name)
		if st, err := os.Stat(path); err != nil || st.Size() != f.Size {
			http.NotFound(w, r)
			return
		}
		switch filepath.Ext(name) {
		case ".woff2":
			w.Header().Set("Content-Type", "font/woff2")
		case ".ttf":
			w.Header().Set("Content-Type", "font/ttf")
		case ".json":
			w.Header().Set("Content-Type", "application/json")
		}
		// a file's checksum is pinned in the catalog, so it never changes
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		http.ServeFile(w, r, path)
		return
	}
	http.NotFound(w, r)
}

func presetFetch(ctx context.Context, p *preset) error {
	mu, _ := presetLocks.LoadOrStore(p.ID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	dir := filepath.Join(presetsRoot(), p.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range p.Files {
		path := filepath.Join(dir, f.Name)
		if st, err := os.Stat(path); err == nil && st.Size() == f.Size {
			continue
		}
		if err := presetDownload(ctx, f, path); err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	return nil
}

// presetDownload saves f to path through a temporary file, keeping it only
// when its size and sha256 match the catalog.
func presetDownload(ctx context.Context, f presetFile, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return err
	}
	res, err := presetClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", res.Status)
	}
	tmp := path + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(res.Body, maxPresetFile+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
	case n != f.Size:
		err = fmt.Errorf("size %d, want %d", n, f.Size)
	case hex.EncodeToString(h.Sum(nil)) != f.SHA256:
		err = errors.New("checksum mismatch")
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
