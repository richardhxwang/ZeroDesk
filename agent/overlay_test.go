package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// Renders the device pairing prompt. With ZERODESK_PREVIEW_DIR set, the
// result is also written as PNG (scaled 3x) for visual review.
func TestRenderPrompt(t *testing.T) {
	for _, name := range []string{"zh", "en", "en_ok"} {
		lang, show := name, ""
		if name == "en_ok" { // pairing set to "press OK": the device shows the code
			lang, show = "en", "4821"
		}
		buf, err := renderPrompt(320, 170, "48", 4, show, "192.168.1.23", lang)
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		if len(buf) != 320*170*2 {
			t.Fatalf("%s: got %d bytes", lang, len(buf))
		}
		dir := os.Getenv("ZERODESK_PREVIEW_DIR")
		if dir == "" {
			continue
		}
		const k = 3
		img := image.NewRGBA(image.Rect(0, 0, 320*k, 170*k))
		for y := 0; y < 170; y++ {
			for x := 0; x < 320; x++ {
				v := uint16(buf[(y*320+x)*2]) | uint16(buf[(y*320+x)*2+1])<<8
				c := color.RGBA{uint8(v>>11) << 3, uint8(v>>5&63) << 2, uint8(v&31) << 3, 255}
				for dy := 0; dy < k; dy++ {
					for dx := 0; dx < k; dx++ {
						img.SetRGBA(x*k+dx, y*k+dy, c)
					}
				}
			}
		}
		f, err := os.Create(filepath.Join(dir, "prompt_"+name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		_ = png.Encode(f, img)
		f.Close()
	}
}
