package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.RGBA{200, 100, 50, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func useWallpaperDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := wallpaperDir
	wallpaperDir = func() string { return dir + "/wallpaper" }
	t.Cleanup(func() { wallpaperDir = old })
	return dir + "/wallpaper"
}

func TestCustomWallpaperRoundTrip(t *testing.T) {
	useWallpaperDir(t)
	pic := testPNG(t)

	rec := httptest.NewRecorder()
	handleCustomWallpaperGet(rec, httptest.NewRequest("GET", "/api/wallpaper/custom", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("before upload: got %d, want 404", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleCustomWallpaperPut(rec, httptest.NewRequest("PUT", "/api/wallpaper/custom", bytes.NewReader(pic)))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: got %d %s", rec.Code, rec.Body)
	}
	var res struct{ Rev int64 }
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || res.Rev == 0 {
		t.Fatalf("upload response %q: %v", rec.Body, err)
	}

	rec = httptest.NewRecorder()
	handleCustomWallpaperGet(rec, httptest.NewRequest("GET", "/api/wallpaper/custom", nil))
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), pic) {
		t.Fatalf("get: code %d, %d bytes", rec.Code, rec.Body.Len())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content type %q", ct)
	}

	rec = httptest.NewRecorder()
	handleCustomWallpaperDelete(rec, httptest.NewRequest("DELETE", "/api/wallpaper/custom", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: got %d", rec.Code)
	}
	if _, err := os.Stat(customWallpaperPath()); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
}

func TestCustomWallpaperRejectsNonImages(t *testing.T) {
	dir := useWallpaperDir(t)
	for _, body := range []string{"", "<svg xmlns='http://www.w3.org/2000/svg'/>", "#!/bin/sh\necho hi\n"} {
		rec := httptest.NewRecorder()
		handleCustomWallpaperPut(rec, httptest.NewRequest("PUT", "/api/wallpaper/custom", strings.NewReader(body)))
		if rec.Code == http.StatusOK {
			t.Fatalf("%q was accepted", body)
		}
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("left files behind: %v", ents)
	}
}

func TestCustomWallpaperTooLargeKeepsPrevious(t *testing.T) {
	dir := useWallpaperDir(t)
	pic := testPNG(t)
	rec := httptest.NewRecorder()
	handleCustomWallpaperPut(rec, httptest.NewRequest("PUT", "/", bytes.NewReader(pic)))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d", rec.Code)
	}

	big := append(append([]byte{}, pic...), make([]byte, maxWallpaper)...)
	rec = httptest.NewRecorder()
	handleCustomWallpaperPut(rec, httptest.NewRequest("PUT", "/", bytes.NewReader(big)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload: got %d, want 413", rec.Code)
	}
	got, err := os.ReadFile(customWallpaperPath())
	if err != nil || !bytes.Equal(got, pic) {
		t.Fatalf("previous picture lost: %v", err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("temporary files left: %v", ents)
	}
}
