package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomedium"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/webp"
)

// The ZeroDesk entry in the CardputerZero launcher (`zerodesk-screen`): a
// small status screen with the address to open on a computer. ZeroDesk
// itself runs as a service; this screen shows it, and S shuts it down or
// starts it again. Pairing prompts from the service are drawn over it; ESC
// returns to the launcher.
//
// It reads the keyboard without grabbing it, so the service can still grab it
// to read the pairing answer.

type screenInfo struct {
	Running bool
	Host    string   // e.g. cardputerzero.local
	Addrs   []string // LAN IPv4 addresses
	Port80  bool
	Lang    string
	QR      string // sign-in link shown as a QR code; empty hides it
	Pending string // "starting" or "stopping" while S is being acted on
}

func pairingFlagPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	return filepath.Join(dir, "zerodesk-pairing")
}

func gatherScreenInfo() screenInfo {
	info := screenInfo{Lang: launcherLang()}
	host, _ := os.Hostname()
	info.Host = strings.ToLower(host) + ".local"
	for _, port := range []string{"80", "8686"} {
		if c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 300*time.Millisecond); err == nil {
			c.Close()
			info.Running = true
			if port == "80" {
				info.Port80 = true
			}
		}
	}
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || strings.HasPrefix(ifc.Name, "tailscale") || strings.HasPrefix(ifc.Name, "docker") {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				info.Addrs = append(info.Addrs, ipn.IP.String())
			}
		}
	}
	return info
}

// launcherLang follows the launcher's language setting when it has one.
func launcherLang() string {
	home, _ := os.UserHomeDir()
	b, err := os.ReadFile(filepath.Join(home, ".config", "cardputerzero", "config.json"))
	if err == nil {
		var cfg map[string]any
		if json.Unmarshal(b, &cfg) == nil {
			for _, k := range []string{"language", "lang", "locale"} {
				if v, ok := cfg[k].(string); ok && strings.HasPrefix(strings.ToLower(v), "zh") {
					return "zh"
				}
			}
		}
	}
	if strings.HasPrefix(os.Getenv("LANG"), "zh") {
		return "zh"
	}
	return "en"
}

type screenText struct {
	running, stopped, open, or, exit, scan, orOpen string
	shutDown, start, starting, stopping            string
}

func screenTextFor(lang string) screenText {
	if lang == "zh" {
		return screenText{"运行中", "未运行", "在电脑浏览器中打开", "或", "退出", "扫码登录", "或在浏览器打开", "关闭", "启动", "正在启动…", "正在关闭…"}
	}
	return screenText{"Running", "Not running", "Open in a browser on your computer", "or", "Exit", "Scan to sign in", "or open", "Shut Down", "Start", "Starting…", "Shutting down…"}
}

// appIcon decodes ZeroDesk's icon from the embedded web assets.
func appIcon(size int) image.Image {
	b, err := fs.ReadFile(webFS, "web/icons/cardputer-dark.webp")
	if err != nil {
		return nil
	}
	src, err := webp.Decode(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	// rounded tile mask
	r := float64(size) * 0.225
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := maxf(maxf(r-float64(x)-0.5, float64(x)+0.5-(float64(size)-r)), 0)
			dy := maxf(maxf(r-float64(y)-0.5, float64(y)+0.5-(float64(size)-r)), 0)
			if dx*dx+dy*dy > r*r {
				dst.SetRGBA(x, y, color.RGBA{})
			}
		}
	}
	return dst
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// renderScreen draws the status screen.
func renderScreen(w, h int, info screenInfo) (*image.RGBA, error) {
	f, err := loadFont(info.Lang)
	if err != nil {
		return nil, err
	}
	t := screenTextFor(info.Lang)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{14, 14, 16, 255}), image.Point{}, draw.Src)
	white := color.RGBA{242, 242, 243, 255}
	dim := color.RGBA{142, 142, 147, 255}
	green := color.RGBA{48, 209, 88, 255}
	red := color.RGBA{255, 69, 58, 255}

	title := face(f, 17)
	small := face(f, 12)
	big := face(f, 20)
	keyFace := face(f, 11)

	if info.Running && info.QR != "" {
		if renderQRScreen(img, info, t, title, small, keyFace, white, dim) {
			return img, nil
		}
		// the code did not fit: fall back to the address-only screen
		draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{14, 14, 16, 255}), image.Point{}, draw.Src)
	}

	// header: icon, name, status
	if ic := appIcon(26); ic != nil {
		draw.Draw(img, image.Rect(14, 10, 40, 36), ic, image.Point{}, draw.Over)
	}
	drawText(img, title, "ZeroDesk", 48, 29, white)
	status, sc := t.stopped, red
	if info.Running {
		status, sc = t.running, green
	}
	switch info.Pending {
	case "starting":
		status, sc = t.starting, dim
	case "stopping":
		status, sc = t.stopping, dim
	}
	sw := textWidth(small, status)
	fillRoundRect(img, image.Rect(w-14-sw-22, 13, w-14, 33), 10, color.RGBA{40, 40, 44, 255})
	fillRoundRect(img, image.Rect(w-14-sw-14, 20, w-14-sw-8, 26), 3, sc)
	drawText(img, small, status, w-14-sw-4, 27, white)

	// address
	port := ""
	if !info.Port80 {
		port = ":8686"
	}
	drawText(img, small, t.open, 16, 62, dim)
	drawText(img, big, "http://"+info.Host+port, 16, 88, white)
	if len(info.Addrs) > 0 {
		alt := t.or + "  http://" + info.Addrs[0] + port
		drawText(img, small, alt, 16, 110, dim)
	}

	drawKeyHints(img, t, info, keyFace, small, white, dim)
	return img, nil
}

// drawKeyHints draws "[ESC] Exit" and "[S] Shut Down" / "[S] Start" along
// the bottom.
func drawKeyHints(img *image.RGBA, t screenText, info screenInfo, keyFace, small font.Face, white, dim color.RGBA) {
	x := drawKeyHint(img, 16, 138, "ESC", t.exit, keyFace, small, white, dim)
	if info.Pending != "" {
		return
	}
	label := t.start
	if info.Running {
		label = t.shutDown
	}
	drawKeyHint(img, x+18, 138, "S", label, keyFace, small, white, dim)
}

// drawKeyHint draws one key cap (top at y) and its label from x; it returns
// where the label ends.
func drawKeyHint(img *image.RGBA, x, y int, key, label string, keyFace, small font.Face, white, dim color.RGBA) int {
	kw := max(textWidth(keyFace, key)+14, 22)
	fillRoundRect(img, image.Rect(x, y, x+kw, y+20), 5, color.RGBA{58, 58, 62, 255})
	drawCentered(img, keyFace, key, x+kw/2, y+14, white)
	drawText(img, small, label, x+kw+7, y+15, dim)
	return x + kw + 7 + textWidth(small, label)
}

// renderQRScreen puts the sign-in code on the right and the name, status and
// address on the left. It reports false when the code does not fit.
func renderQRScreen(img *image.RGBA, info screenInfo, t screenText, title, small, keyFace font.Face, white, dim color.RGBA) bool {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	side := h - 24
	box, err := drawQR(img, image.Rect(w-12-side, 12, w-12, 12+side), info.QR, 2)
	if err != nil {
		return false
	}
	right := box.Min.X - 14 // the left column ends here

	if ic := appIcon(26); ic != nil {
		draw.Draw(img, image.Rect(14, 12, 40, 38), ic, image.Point{}, draw.Over)
	}
	drawText(img, title, "ZeroDesk", 48, 31, white)

	label := face(mustFont(info.Lang), 15)
	drawText(img, label, t.scan, 16, 63, white)
	drawText(img, small, t.orOpen, 16, 83, dim)
	host := info.Host
	if !info.Port80 {
		host += ":8686"
	}
	hf := small
	for size := 13.0; size >= 9; size-- {
		hf = face(mustFont(info.Lang), size)
		if textWidth(hf, host) <= right-16 {
			break
		}
	}
	drawText(img, hf, host, 16, 100, white)

	// The code only shows while ZeroDesk runs, so the column has no status
	// line: the keys go one above the other (side by side they would run
	// into the code).
	if info.Pending == "stopping" {
		fillRoundRect(img, image.Rect(17, 118, 23, 124), 3, dim)
		drawText(img, small, t.stopping, 29, 125, dim)
	} else {
		drawKeyHint(img, 16, 111, "S", t.shutDown, keyFace, small, white, dim)
	}
	drawKeyHint(img, 16, 138, "ESC", t.exit, keyFace, small, white, dim)
	return true
}

// mustFont is loadFont for callers that already loaded it once.
func mustFont(lang string) *opentype.Font {
	f, err := loadFont(lang)
	if err != nil {
		f, _ = opentype.Parse(gomedium.TTF)
	}
	return f
}

func toRGB565(img *image.RGBA) []byte {
	b := img.Bounds()
	out := make([]byte, b.Dx()*b.Dy()*2)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			c := img.RGBAAt(x, y)
			v := uint16(c.R>>3)<<11 | uint16(c.G>>2)<<5 | uint16(c.B>>3)
			i := (y*b.Dx() + x) * 2
			out[i] = byte(v)
			out[i+1] = byte(v >> 8)
		}
	}
	return out
}

// runDeviceScreen is the `zerodesk-screen` program started by the launcher.
func runDeviceScreen(fbPath, kbdMatch string) int {
	geo, err := (&screenHub{fbPath: fbPath}).geometry()
	if err != nil {
		fmt.Fprintln(os.Stderr, "zerodesk-screen:", err)
		return 1
	}
	if f, err := os.OpenFile(fbPath, os.O_RDWR, 0); err != nil {
		fmt.Fprintln(os.Stderr, "zerodesk-screen:", err)
		return 1
	} else {
		f.Close()
	}

	// the code is only valid while it is on screen
	defer os.Remove(qrKeyPath())

	quit := make(chan struct{})
	toggle := make(chan struct{}, 1)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; close(quit) }()

	if dev, err := findKeyboardNode(kbdMatch); err == nil {
		if kf, err := os.Open(dev); err == nil {
			defer kf.Close()
			go func() {
				buf := make([]byte, 24*16)
				for {
					n, err := kf.Read(buf)
					if err != nil {
						return
					}
					for i := 0; i+24 <= n; i += 24 {
						typ := binary.LittleEndian.Uint16(buf[i+16:])
						code := binary.LittleEndian.Uint16(buf[i+18:])
						val := int32(binary.LittleEndian.Uint32(buf[i+20:]))
						if typ == evKey && code == 1 && val == 1 { // ESC
							select {
							case <-quit:
							default:
								close(quit)
							}
							return
						}
						if typ == evKey && code == 31 && val == 1 { // S
							select {
							case toggle <- struct{}{}:
							default:
							}
						}
					}
				}
			}()
		}
	}

	var last []byte
	var pending string
	var pendingUntil time.Time
	paint := func() {
		if _, err := os.Stat(pairingFlagPath()); err == nil {
			last = nil // the service's pairing prompt is on screen
			return
		}
		info := gatherScreenInfo()
		if pending != "" && (info.Running == (pending == "starting") || time.Now().After(pendingUntil)) {
			pending = ""
		}
		info.Pending = pending
		if info.Running && len(info.Addrs) > 0 {
			if k, err := currentQRKey(qrKeyPath(), time.Now()); err == nil {
				info.QR = qrLoginURL(info.Addrs[0], info.Port80, k.key)
			}
		}
		img, err := renderScreen(geo.width, geo.height, info)
		if err != nil {
			return
		}
		frame := toRGB565(img)
		if bytes.Equal(frame, last) {
			return
		}
		rb := geo.width * 2
		if writeFB(fbPath, geo, func(y int) []byte { return frame[y*rb : (y+1)*rb] }) == nil {
			last = frame
		}
	}
	paint()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-quit:
			return 0
		case <-tick.C:
			paint()
		case <-toggle:
			if pending != "" {
				continue
			}
			var err error
			if gatherScreenInfo().Running {
				pending, err = "stopping", stopZeroDesk()
			} else {
				pending, err = "starting", startZeroDesk()
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "zerodesk-screen:", err)
				pending = ""
			}
			pendingUntil = time.Now().Add(15 * time.Second)
			paint()
		}
	}
}
