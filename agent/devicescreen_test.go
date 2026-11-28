package main

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Renders the launcher screen. With ZERODESK_PREVIEW_DIR set, writes the
// 320x170 PNGs used as Store screenshots (placeholder address from the
// documentation range 192.0.2.0/24, never a real network).
func TestRenderScreen(t *testing.T) {
	dir := os.Getenv("ZERODESK_PREVIEW_DIR")
	for _, lang := range []string{"en", "zh"} {
		for _, running := range []bool{true, false} {
			info := screenInfo{Running: running, Host: "cardputerzero.local", Addrs: []string{"192.0.2.10"}, Port80: true, Lang: lang, WiFi: "Home"}
			if running {
				info.QR = qrLoginURL("192.0.2.10", true, "aaaaaaaaaaaaaaaa")
			}
			img, err := renderScreen(320, 170, info)
			if err != nil {
				t.Fatal(err)
			}
			if b := img.Bounds(); b.Dx() != 320 || b.Dy() != 170 {
				t.Fatalf("size %v", b)
			}
			if dir == "" {
				continue
			}
			name := "screen_" + lang
			if !running {
				name += "_stopped"
			}
			writePNG(t, filepath.Join(dir, name+".png"), img)
		}
	}
	if dir != "" {
		buf, err := renderPrompt(320, 170, "4821", "en")
		if err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rect(0, 0, 320, 170))
		for i := 0; i < 320*170; i++ {
			v := uint16(buf[i*2]) | uint16(buf[i*2+1])<<8
			img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = uint8(v>>11)<<3, uint8(v>>5&63)<<2, uint8(v&31)<<3, 255
		}
		writePNG(t, filepath.Join(dir, "screen_pairing.png"), img)
	}
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// No network address: the screen must not offer an address nobody can reach.
func TestRenderScreenOffline(t *testing.T) {
	dir := os.Getenv("ZERODESK_PREVIEW_DIR")
	for _, lang := range []string{"en", "zh"} {
		for _, running := range []bool{true, false} {
			info := screenInfo{Running: running, Host: "cardputerzero.local", Lang: lang}
			img, err := renderScreen(320, 170, info)
			if err != nil {
				t.Fatal(err)
			}
			if dir != "" {
				name := "screen_" + lang + "_offline"
				if !running {
					name += "_stopped"
				}
				writePNG(t, filepath.Join(dir, name+".png"), img)
			}
		}
	}
}

// The screen while S is being acted on: no S hint, the status says what is happening.
func TestRenderScreenPending(t *testing.T) {
	dir := os.Getenv("ZERODESK_PREVIEW_DIR")
	for _, lang := range []string{"en", "zh"} {
		for _, pending := range []string{"starting", "stopping"} {
			running := pending == "stopping"
			info := screenInfo{Running: running, Host: "cardputerzero.local", Addrs: []string{"192.0.2.10"}, Port80: true, Lang: lang, Pending: pending}
			if running {
				info.QR = qrLoginURL("192.0.2.10", true, "aaaaaaaaaaaaaaaa")
			}
			img, err := renderScreen(320, 170, info)
			if err != nil {
				t.Fatal(err)
			}
			if dir != "" {
				writePNG(t, filepath.Join(dir, "screen_"+lang+"_"+pending+".png"), img)
			}
		}
	}
}

func TestFitText(t *testing.T) {
	fc := face(mustFont("en"), 12)
	long := "Same Wi-Fi: a very long network name that cannot fit"
	got := fitText(fc, long, 120)
	if textWidth(fc, got) > 120 || !strings.HasSuffix(got, "…") {
		t.Fatalf("fitText = %q (%d px)", got, textWidth(fc, got))
	}
	if fitText(fc, "Home", 120) != "Home" {
		t.Fatal("short text was changed")
	}
}

func TestFitNamedKeepsPrefixAndEnds(t *testing.T) {
	prefix, name := "Same Wi-Fi: ", "ChinaNet-Home-Living-Room-Upstairs-5G"
	fc, got := fitNamed("en", 11, prefix, name, 160)
	if textWidth(fc, got) > 160 {
		t.Fatalf("%q is %d px wide", got, textWidth(fc, got))
	}
	if !strings.HasPrefix(got, prefix+"Chin") || !strings.HasSuffix(got, "-5G") || !strings.Contains(got, "…") {
		t.Fatalf("fitNamed = %q", got)
	}
	if _, got := fitNamed("en", 11, prefix, "Home", 160); got != prefix+"Home" {
		t.Fatalf("short name changed: %q", got)
	}
}
