package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func testPreset(t *testing.T, body []byte, sum string, size int64) (*preset, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	t.Cleanup(srv.Close)
	root := t.TempDir()
	old := presetsRoot
	presetsRoot = func() string { return root }
	t.Cleanup(func() { presetsRoot = old })
	return &preset{ID: "test", Files: []presetFile{{Name: "theme.json", URL: srv.URL, Size: size, SHA256: sum}}}, srv
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestPresetFetchKeepsVerifiedFile(t *testing.T) {
	body := []byte(`{"background":"#000000"}`)
	p, _ := testPreset(t, body, sha(body), int64(len(body)))
	if err := presetFetch(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if !presetInstalled(p) {
		t.Fatal("preset not installed after a verified download")
	}
	got, _ := os.ReadFile(filepath.Join(presetsRoot(), "test", "theme.json"))
	if string(got) != string(body) {
		t.Fatalf("stored %q", got)
	}
}

func TestPresetFetchRejectsWrongChecksum(t *testing.T) {
	body := []byte(`{"background":"#000000"}`)
	p, _ := testPreset(t, body, sha([]byte("something else")), int64(len(body)))
	err := presetFetch(t.Context(), p)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("want checksum error, got %v", err)
	}
	assertNoFiles(t)
}

func TestPresetFetchRejectsWrongSize(t *testing.T) {
	body := []byte(`{"background":"#000000"}`)
	p, _ := testPreset(t, body, sha(body), int64(len(body))+1)
	if err := presetFetch(t.Context(), p); err == nil {
		t.Fatal("want size error")
	}
	assertNoFiles(t)
}

func assertNoFiles(t *testing.T) {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(presetsRoot(), "test"))
	if len(entries) != 0 {
		t.Fatalf("left files behind: %v", entries)
	}
}

func TestPresetFileServesOnlyCatalogNames(t *testing.T) {
	loadPresets()
	root := t.TempDir()
	old := presetsRoot
	presetsRoot = func() string { return root }
	t.Cleanup(func() { presetsRoot = old })

	p := presetsByID["dracula"]
	os.MkdirAll(filepath.Join(root, "dracula"), 0o755)
	os.WriteFile(filepath.Join(root, "dracula", "secret"), []byte("x"), 0o644)
	body := make([]byte, p.Files[0].Size)
	os.WriteFile(filepath.Join(root, "dracula", "dark.json"), body, 0o644)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/presets/{id}/{file}", handlePresetFile)
	for path, want := range map[string]int{
		"/api/presets/dracula/dark.json":  200,
		"/api/presets/dracula/light.json": 404, // Dracula has no light variant
		"/api/presets/dracula/secret":     404,
		"/api/presets/nope/dark.json":     404,
		"/api/presets/nord/dark.json":     404, // not downloaded
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
	}
}

func TestPresetsListHidesURLs(t *testing.T) {
	rec := httptest.NewRecorder()
	handlePresetsList(rec, httptest.NewRequest("GET", "/api/presets", nil))
	if strings.Contains(rec.Body.String(), "https://raw.") || strings.Contains(rec.Body.String(), "sha256") {
		t.Fatal("list exposes download URLs or checksums")
	}
	var got struct{ Themes, Fonts []map[string]any }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Themes) == 0 || len(got.Fonts) == 0 {
		t.Fatalf("bad list: %v %s", err, rec.Body.String())
	}
}

func TestPresetCatalogIsWellFormed(t *testing.T) {
	loadPresets()
	hexsum := regexp.MustCompile(`^[0-9a-f]{64}$`)
	name := regexp.MustCompile(`^[0-9a-z]+\.(json|woff2|ttf)$`)
	seen := map[string]bool{}
	for _, list := range [][]preset{presetsCat.Themes, presetsCat.Fonts} {
		for _, p := range list {
			if seen[p.ID] || !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(p.ID) {
				t.Errorf("bad or duplicate id %q", p.ID)
			}
			seen[p.ID] = true
			if len(p.Files) == 0 || p.License == "" {
				t.Errorf("%s: no files or no license", p.ID)
			}
			for _, f := range p.Files {
				if !strings.HasPrefix(f.URL, "https://") || !hexsum.MatchString(f.SHA256) || !name.MatchString(f.Name) || f.Size <= 0 || f.Size > maxPresetFile {
					t.Errorf("%s: bad file %+v", p.ID, f)
				}
			}
		}
	}
	for _, p := range presetsCat.Themes {
		if len(p.Preview) == 0 || len(p.Preview) != len(p.Files) {
			t.Errorf("%s: needs a preview for each variant", p.ID)
		}
		for _, f := range p.Files {
			mode := strings.TrimSuffix(f.Name, ".json")
			if (mode != "light" && mode != "dark") || len(p.Preview[mode]) != 8 {
				t.Errorf("%s: %s needs 8 preview colours", p.ID, f.Name)
			}
		}
	}
	for _, p := range presetsCat.Fonts {
		if p.Family == "" {
			t.Errorf("%s: font needs a family", p.ID)
		}
	}
}
