package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"fmt"
	"image"
	"image/color"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"rsc.io/qr"
)

// Sign in by scanning the QR code on the device screen. The launcher entry
// (zerodesk-screen) makes a one-time key, keeps it in a file only the device
// user can read and shows http://<address>/q/<key> as a QR code. Opening that
// link once sets the session cookie, like approving a pairing on the device:
// both need someone who can see the screen. The key expires after
// qrKeyLifetime and is deleted when used; the screen then shows a new one.

const (
	qrKeyLifetime = 3 * time.Minute
	qrKeyRenew    = 2 * time.Minute // the screen replaces a key this old
)

func qrKeyPath() string {
	return filepath.Join(filepath.Dir(pairingFlagPath()), "zerodesk-qr")
}

type qrKey struct {
	key     string
	expires time.Time
}

func newQRKey(now time.Time) qrKey {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	k := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
	return qrKey{key: k, expires: now.Add(qrKeyLifetime)}
}

func (k qrKey) encode() []byte {
	return []byte(k.key + " " + strconv.FormatInt(k.expires.Unix(), 10) + "\n")
}

// parseQRKeys reads one key per line: the current one first, then the
// previous one, which stays valid until it expires so a code scanned just
// before the screen changed it still works.
func parseQRKeys(b []byte) []qrKey {
	var keys []qrKey
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || len(f[0]) < 16 {
			continue
		}
		t, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			continue
		}
		keys = append(keys, qrKey{key: f[0], expires: time.Unix(t, 0)})
	}
	return keys
}

func writeQRKeys(path string, keys []qrKey) error {
	var b []byte
	for _, k := range keys {
		b = append(b, k.encode()...)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// currentQRKey returns the key to show, replacing it when it is missing or
// due for renewal. Used by the screen.
func currentQRKey(path string, now time.Time) (qrKey, error) {
	var keys []qrKey
	if b, err := os.ReadFile(path); err == nil {
		keys = parseQRKeys(b)
	}
	if len(keys) > 0 && now.Before(keys[0].expires.Add(-qrKeyLifetime+qrKeyRenew)) {
		return keys[0], nil
	}
	k := newQRKey(now)
	next := []qrKey{k}
	if len(keys) > 0 && now.Before(keys[0].expires) {
		next = append(next, keys[0])
	}
	if err := writeQRKeys(path, next); err != nil {
		return qrKey{}, err
	}
	return k, nil
}

// takeQRKey checks a key from a request against the file and deletes the
// file when one matches, so each code works once.
func takeQRKey(path, given string, now time.Time) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	ok := false
	for _, k := range parseQRKeys(b) {
		if now.Before(k.expires) && subtle.ConstantTimeCompare([]byte(k.key), []byte(given)) == 1 {
			ok = true
		}
	}
	return ok && os.Remove(path) == nil
}

func (s *server) handleQRLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	ip := clientIP(r)
	if ok, _, slot := s.loginLimit.reserve(ip, time.Now()); !ok {
		log.Printf("pair: QR sign-in refused, too many attempts (%s)", ip)
	} else if takeQRKey(qrKeyPath(), r.PathValue("key"), time.Now()) {
		s.loginLimit.refund(ip, slot)
		if err := s.startSession(w, r, "qr"); err != nil {
			log.Printf("pair: QR sign-in: %v", err)
		} else {
			log.Printf("pair: signed in with the device QR code (%s)", ip)
		}
	} else {
		log.Printf("pair: QR code not accepted, expired or already used (%s)", ip)
	}
	// on failure the browser lands on the pairing page as usual
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// qrLoginURL is what the screen encodes.
func qrLoginURL(addr string, port80 bool, key string) string {
	port := ":8686"
	if port80 {
		port = ""
	}
	return fmt.Sprintf("http://%s%s/q/%s", addr, port, key)
}

// qrModules encodes text and returns the module grid size and a lookup.
func qrModules(text string) (int, func(x, y int) bool, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return 0, nil, err
	}
	return c.Size, c.Black, nil
}

// drawQR draws the code with a white quiet zone of quiet modules inside r,
// using the largest whole-pixel module size that fits. It returns the
// rectangle it used.
func drawQR(img *image.RGBA, r image.Rectangle, text string, quiet int) (image.Rectangle, error) {
	n, black, err := qrModules(text)
	if err != nil {
		return image.Rectangle{}, err
	}
	total := n + 2*quiet
	scale := min(r.Dx(), r.Dy()) / total
	if scale < 1 {
		return image.Rectangle{}, fmt.Errorf("QR code needs %d px", total)
	}
	side := total * scale
	x0 := r.Min.X + (r.Dx()-side)/2
	y0 := r.Min.Y + (r.Dy()-side)/2
	box := image.Rect(x0, y0, x0+side, y0+side)
	fillRoundRect(img, box, float64(scale*quiet)/2, color.RGBA{255, 255, 255, 255})
	ink := color.RGBA{0, 0, 0, 255}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if !black(x, y) {
				continue
			}
			px, py := x0+(x+quiet)*scale, y0+(y+quiet)*scale
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetRGBA(px+dx, py+dy, ink)
				}
			}
		}
	}
	return box, nil
}

// clientIP is the address the request came from (ZeroDesk is reached
// directly, never through a proxy that would set X-Forwarded-For).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
