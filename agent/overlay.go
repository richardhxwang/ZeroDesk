package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"syscall"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomedium"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The pairing prompt is drawn straight into the LCD framebuffer, on top of
// whatever app is showing, and the previous pixels are put back afterwards.

// CJK-capable font shipped with the launcher; Go Medium is the fallback.
var cjkFontPaths = []string{
	"/usr/share/APPLaunch/share/font/AlibabaPuHuiTi-3-55-Regular.ttf",
}

type promptText struct {
	title, question, confirm, allow, cancelKey, deny string
}

func promptFor(lang string) promptText {
	if lang == "zh" {
		return promptText{"ZeroDesk", "请输入浏览器上显示的连接码", "请核对浏览器上的连接码", "允许", "esc", "拒绝"}
	}
	return promptText{"ZeroDesk", "Type the code from your browser", "Check the code in your browser", "Allow", "esc", "Deny"}
}

func loadFont(lang string) (*opentype.Font, error) {
	if lang == "zh" {
		paths := cjkFontPaths
		if p := os.Getenv("ZERODESK_CJK_FONT"); p != "" {
			paths = append([]string{p}, paths...)
		}
		for _, p := range paths {
			if b, err := os.ReadFile(p); err == nil {
				if f, err := opentype.Parse(b); err == nil {
					return f, nil
				}
			}
		}
	}
	return opentype.Parse(gomedium.TTF)
}

func face(f *opentype.Font, size float64) font.Face {
	fc, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	return fc
}

func textWidth(fc font.Face, s string) int {
	return font.MeasureString(fc, s).Ceil()
}

func drawText(dst draw.Image, fc font.Face, s string, x, baseline int, c color.Color) {
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: fc, Dot: fixed.P(x, baseline)}
	d.DrawString(s)
}

func drawCentered(dst draw.Image, fc font.Face, s string, cx, baseline int, c color.Color) {
	drawText(dst, fc, s, cx-textWidth(fc, s)/2, baseline, c)
}

// fillRoundRect draws an anti-aliased rounded rectangle.
func fillRoundRect(dst *image.RGBA, r image.Rectangle, radius float64, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			// distance outside the rounded corner
			dx := math.Max(math.Max(float64(r.Min.X)+radius-px, px-(float64(r.Max.X)-radius)), 0)
			dy := math.Max(math.Max(float64(r.Min.Y)+radius-py, py-(float64(r.Max.Y)-radius)), 0)
			cov := radius + 0.5 - math.Hypot(dx, dy)
			if cov <= 0 {
				continue
			}
			if cov > 1 {
				cov = 1
			}
			a := float64(c.A) / 255 * cov
			o := dst.RGBAAt(x, y)
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(float64(c.R)*a + float64(o.R)*(1-a)),
				G: uint8(float64(c.G)*a + float64(o.G)*(1-a)),
				B: uint8(float64(c.B)*a + float64(o.B)*(1-a)),
				A: 255,
			})
		}
	}
}

// renderPrompt returns the prompt as row-packed RGB565 (w*h*2 bytes). When
// the code is typed on the device (show == ""), it is shown only in the
// browser and the device shows one slot per digit with what has been typed
// so far. When pairing is set to confirm with OK, the device shows the code
// (show) to compare with the browser.
func renderPrompt(w, h int, typed string, slots int, show, from, lang string) ([]byte, error) {
	f, err := loadFont(lang)
	if err != nil {
		return nil, fmt.Errorf("font: %w", err)
	}
	t := promptFor(lang)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{14, 14, 16, 255}), image.Point{}, draw.Src)

	dim := color.RGBA{142, 142, 147, 255}
	white := color.RGBA{242, 242, 243, 255}
	accent := white

	small := face(f, 12)
	mid := face(f, 16)
	codeFace := face(f, 30)
	keyFace := face(f, 11)
	cx := w / 2

	title := t.title
	if from != "" { // who is asking, so a request from an unknown address stands out
		title += " · " + from
	}
	drawCentered(img, small, title, cx, 24, dim)
	question := t.question
	digits := []rune(typed)
	if show != "" {
		question, digits, slots = t.confirm, []rune(show), len([]rune(show))
	}
	drawCentered(img, mid, question, cx, 48, white)

	// one slot per digit; typed digits are drawn in their slot
	const slotW, slotH, gap = 38, 44, 10
	x := cx - (slots*slotW+(slots-1)*gap)/2
	for i := 0; i < slots; i++ {
		bg := color.RGBA{38, 38, 42, 255}
		if show == "" && i == len(digits) {
			bg = color.RGBA{58, 58, 62, 255} // the next digit goes here
		}
		fillRoundRect(img, image.Rect(x, 62, x+slotW, 62+slotH), 8, bg)
		if i < len(digits) {
			drawCentered(img, codeFace, string(digits[i]), x+slotW/2, 62+slotH/2+11, accent)
		}
		x += slotW + gap
	}

	// key hints: [OK] Allow     [ESC] Deny
	hint := func(key, label string, left int) int {
		kw := textWidth(keyFace, key) + 14
		fillRoundRect(img, image.Rect(left, 128, left+kw, 148), 5, color.RGBA{58, 58, 62, 255})
		drawCentered(img, keyFace, key, left+kw/2, 142, white)
		drawText(img, small, label, left+kw+7, 143, dim)
		return kw + 7 + textWidth(small, label)
	}
	okW := textWidth(keyFace, "OK") + 14 + 7 + textWidth(small, t.allow)
	escW := textWidth(keyFace, t.cancelKey) + 14 + 7 + textWidth(small, t.deny)
	start := cx - (okW+28+escW)/2
	start += hint("OK", t.allow, start) + 28
	hint(t.cancelKey, t.deny, start)

	out := make([]byte, w*h*2)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.RGBAAt(x, y)
			v := uint16(c.R>>3)<<11 | uint16(c.G>>2)<<5 | uint16(c.B>>3)
			i := (y*w + x) * 2
			out[i] = byte(v)
			out[i+1] = byte(v >> 8)
		}
	}
	return out, nil
}

// fbOverlay paints a prompt over the LCD and can restore what was there.
type fbOverlay struct {
	path  string
	geo   fbGeometry
	saved []byte
	frame []byte
	slots int
	show  string
	from  string
	lang  string
}

func newFBOverlay(path string, slots int, show, from, lang string) (*fbOverlay, error) {
	h := &screenHub{fbPath: path}
	geo, err := h.geometry()
	if err != nil {
		return nil, err
	}
	frame, err := renderPrompt(geo.width, geo.height, "", slots, show, from, lang)
	if err != nil {
		return nil, err
	}
	o := &fbOverlay{path: path, geo: geo, frame: frame, slots: slots, show: show, from: from, lang: lang}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	o.saved = make([]byte, geo.stride*geo.height)
	if _, err := f.ReadAt(o.saved, 0); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *fbOverlay) write(rows func(y int) []byte) error {
	return writeFB(o.path, o.geo, rows)
}

// writeFB copies one row per line into the framebuffer through a shared
// mapping. On the CardputerZero the LCD is a DRM panel with fbdev
// emulation: write(2) only fills the shadow buffer, while stores into the
// mapping go through deferred I/O, which is what sends pixels to the panel.
func writeFB(path string, geo fbGeometry, rows func(y int) []byte) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	m, err := syscall.Mmap(int(f.Fd()), 0, geo.stride*geo.height, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return err
	}
	defer syscall.Munmap(m)
	for y := 0; y < geo.height; y++ {
		copy(m[y*geo.stride:(y+1)*geo.stride], rows(y))
	}
	return nil
}

// setTyped redraws the prompt with the digits typed so far.
func (o *fbOverlay) setTyped(typed string) error {
	frame, err := renderPrompt(o.geo.width, o.geo.height, typed, o.slots, o.show, o.from, o.lang)
	if err != nil {
		return err
	}
	o.frame = frame
	return o.paint()
}

func (o *fbOverlay) paint() error {
	rb := o.geo.width * 2
	return o.write(func(y int) []byte { return o.frame[y*rb : (y+1)*rb] })
}

func (o *fbOverlay) restore() error {
	rb := o.geo.width * 2
	return o.write(func(y int) []byte { return o.saved[y*o.geo.stride : y*o.geo.stride+rb] })
}
