package main

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// Renders the launcher screen. With ZERODESK_PREVIEW_DIR set, writes the
// 320x170 PNGs used as Store screenshots (placeholder address from the
// documentation range 192.0.2.0/24, never a real network).
func TestRenderScreen(t *testing.T) {
	dir := os.Getenv("ZERODESK_PREVIEW_DIR")
	for _, lang := range []string{"en", "zh"} {
		for _, running := range []bool{true, false} {
			info := screenInfo{Running: running, Host: "cardputerzero.local", Addrs: []string{"192.0.2.10"}, Port80: true, Lang: lang}
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
		buf, err := renderPrompt(320, 170, "48", 4, "", "192.168.1.23", "en")
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
