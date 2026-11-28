package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Pairing: a browser asks to pair and shows a short code; the person
// holding the device types that code on its keyboard and presses OK. While
// waiting, ZeroDesk grabs the keyboard (EVIOCGRAB) so those presses are not
// also delivered to the launcher or the foreground app. Someone on the same
// Wi-Fi without the device in hand cannot sign in, and a request the person
// did not start cannot be approved by a stray Enter.
//
// One request waits at a time. A browser can replace its own request only by
// naming it (its id), not just by coming from the same address, since
// browsers behind one proxy share an address. A new prompt from an address
// needs pairCooldown since its last one, however that one ended; each
// address can also start only a few per ten minutes.

const (
	pairTimeout  = 60 * time.Second
	pairCooldown = 30 * time.Second
	pairDigits   = 4
	keyEsc       = 1
	keyBackspace = 14
	keyEnter     = 28
	keyKPEnter   = 96
	eviocgrab    = 0x40044590 // _IOW('E', 0x90, int)
)

type pairState struct {
	mu       sync.Mutex
	id       string
	code     string
	ip       string // who asked
	approved bool
	denied   bool
	// unavailable: the prompt could not be shown or the keyboard could not
	// be grabbed, so pairing cannot work
	unavailable bool
	expires     time.Time
	cancel      context.CancelFunc
	// lastPrompt: when each address last put a prompt on the device
	lastPrompt map[string]time.Time
	confirm    string // how the current request is confirmed, fixed when it starts
	// lastEnd: when each address's last prompt left the screen; the next
	// one is not shown before pairCooldown after that, even when it replaces
	// a request that was still waiting
	lastEnd map[string]time.Time
}

func randomCode() string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%0*d", pairDigits, int(binary.BigEndian.Uint16(b))%10000)
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func findKeyboardNode(match string) (string, error) {
	nodes, _ := filepath.Glob("/sys/class/input/event*")
	for _, n := range nodes {
		b, err := os.ReadFile(filepath.Join(n, "device", "name"))
		if err == nil && strings.Contains(strings.ToLower(string(b)), match) {
			return "/dev/input/" + filepath.Base(n), nil
		}
	}
	return "", fmt.Errorf("no input device matching %q", match)
}

// digitKeys maps evdev key codes to the digit they type (number row and keypad).
var digitKeys = map[uint16]byte{
	2: '1', 3: '2', 4: '3', 5: '4', 6: '5', 7: '6', 8: '7', 9: '8', 10: '9', 11: '0',
	79: '1', 80: '2', 81: '3', 75: '4', 76: '5', 77: '6', 71: '7', 72: '8', 73: '9', 82: '0',
}

// Pairing confirmation, chosen in Settings › General:
//   - pairConfirmCode (default): the browser shows the code and it is typed
//     on the device, so only someone who can see that browser can approve.
//   - pairConfirmOK: the device shows the same code as the browser and OK
//     approves; the person compares the two.
const (
	pairConfirmCode = "code"
	pairConfirmOK   = "ok"
	pairingFile     = "pairing.json"
)

type pairSettings struct {
	Confirm string `json:"confirm"`
}

func loadPairSettings() pairSettings {
	ps := pairSettings{Confirm: pairConfirmCode}
	if b, err := os.ReadFile(filepath.Join(configDir(), pairingFile)); err == nil {
		var got pairSettings
		if json.Unmarshal(b, &got) == nil && got.Confirm == pairConfirmOK {
			ps.Confirm = pairConfirmOK
		}
	}
	return ps
}

func (s *server) handlePairingGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, loadPairSettings())
}

// pairingMu: one change to pairing.json at a time, applied in the order
// they arrive.
var pairingMu sync.Mutex

func (s *server) handlePairingPut(w http.ResponseWriter, r *http.Request) {
	// How this device accepts new browsers is changed only from a signed-in
	// browser, not with the access token alone.
	if _, ok := sessionFrom(r); !ok && !s.noAuth {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "change this from a signed-in browser"})
		return
	}
	var body pairSettings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil ||
		body.Confirm != pairConfirmCode && body.Confirm != pairConfirmOK {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	pairingMu.Lock()
	defer pairingMu.Unlock()
	if err := writeJSONFile(filepath.Join(configDir(), pairingFile), body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// codeEntry follows what is typed on the device: digits fill the slots,
// Backspace removes one, OK answers (true only when the code matches), Esc
// denies. Other keys are ignored. With okOnly (pairConfirmOK) nothing is
// typed: OK allows and Esc denies.
type codeEntry struct {
	want   string
	typed  []byte
	okOnly bool
}

// key returns done=true when the prompt is answered.
func (c *codeEntry) key(code uint16) (done, ok, changed bool) {
	if c.okOnly {
		switch code {
		case keyEsc:
			return true, false, false
		case keyEnter, keyKPEnter:
			return true, true, false
		}
		return false, false, false
	}
	switch {
	case code == keyEsc:
		return true, false, false
	case code == keyEnter || code == keyKPEnter:
		if len(c.typed) < len(c.want) {
			return false, false, false // not finished: wait for the rest
		}
		return true, subtle.ConstantTimeCompare(c.typed, []byte(c.want)) == 1, false
	case code == keyBackspace:
		if len(c.typed) == 0 {
			return false, false, false
		}
		c.typed = c.typed[:len(c.typed)-1]
		return false, false, true
	}
	if d, isDigit := digitKeys[code]; isDigit && len(c.typed) < len(c.want) {
		c.typed = append(c.typed, d)
		return false, false, true
	}
	return false, false, false
}

// waitForAnswer grabs the keyboard and follows the code typed on it. It
// returns true when the right code is confirmed with OK, false when it is
// denied (Esc, or OK on a wrong code), or (false, nil) when ctx ends. While
// it waits it keeps repainting the prompt, because the app underneath may
// redraw over it.
func waitForAnswer(ctx context.Context, match string, overlay *fbOverlay, want string, okOnly bool) (bool, error) {
	dev, err := findKeyboardNode(match)
	if err != nil {
		return false, err
	}
	f, err := os.OpenFile(dev, os.O_RDONLY, 0)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", dev, err)
	}
	defer f.Close()
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), eviocgrab, 1); errno != 0 {
		return false, fmt.Errorf("grab keyboard: %w", errno)
	}
	defer syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), eviocgrab, 0) //nolint:errcheck

	done := make(chan bool, 1)
	typed := make(chan string, 8)
	go func() {
		entry := &codeEntry{want: want, okOnly: okOnly}
		buf := make([]byte, 24*16)
		for {
			n, err := f.Read(buf)
			if err != nil {
				done <- false
				return
			}
			for i := 0; i+24 <= n; i += 24 {
				typ := binary.LittleEndian.Uint16(buf[i+16:])
				code := binary.LittleEndian.Uint16(buf[i+18:])
				val := int32(binary.LittleEndian.Uint32(buf[i+20:]))
				if typ != evKey || val != 1 {
					continue
				}
				fin, ok, changed := entry.key(code)
				if fin {
					done <- ok
					return
				}
				if changed {
					select {
					case typed <- string(entry.typed):
					default:
					}
				}
			}
		}
	}()
	repaint := time.NewTicker(150 * time.Millisecond)
	defer repaint.Stop()
	for {
		if err := overlay.paint(); err != nil {
			return false, fmt.Errorf("prompt: %w", err)
		}
		select {
		case ok := <-done:
			return ok, nil
		case t := <-typed:
			if err := overlay.setTyped(t); err != nil {
				return false, fmt.Errorf("prompt: %w", err)
			}
		case <-ctx.Done():
			f.Close() // unblocks the reader goroutine
			return false, nil
		case <-repaint.C:
		}
	}
}

func (s *server) handlePairStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Lang    string `json:"lang"`
		Replace string `json:"replace"` // id of this browser's own waiting request
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body)
	lang := "en"
	if body.Lang == "zh" {
		lang = "zh"
	}
	ip := clientIP(r)
	now := time.Now()

	p := &s.pair
	p.mu.Lock()
	pending := p.cancel != nil && now.Before(p.expires)
	own := pending && p.ip == ip && body.Replace != "" && body.Replace == p.id
	// One prompt at a time: only the browser that started it can replace it.
	if pending && !own {
		wait := p.expires.Sub(now)
		p.mu.Unlock()
		w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)+1))
		writeJSON(w, http.StatusConflict, map[string]any{"error": "another computer is waiting for the device", "retryIn": int(wait/time.Second) + 1})
		return
	}
	// A new prompt from this address needs pairCooldown since its last one,
	// however that one ended (denied, cancelled, expired).
	if last, ok := p.lastPrompt[ip]; ok && now.Sub(last) < pairCooldown {
		p.mu.Unlock()
		writeRetryAfterReason(w, pairCooldown-now.Sub(last), "cooldown")
		return
	}
	if ok, wait := s.pairLimit.allow(ip, now); !ok {
		p.mu.Unlock()
		writeRetryAfter(w, wait)
		return
	}
	if p.lastPrompt == nil {
		p.lastPrompt = map[string]time.Time{}
	}
	for k, t := range p.lastPrompt {
		if now.Sub(t) >= pairCooldown {
			delete(p.lastPrompt, k)
		}
	}
	if len(p.lastPrompt) >= 4096 { // cannot remember another address: refuse
		p.mu.Unlock()
		writeRetryAfter(w, pairCooldown)
		return
	}
	p.lastPrompt[ip] = now
	if p.cancel != nil {
		p.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), pairTimeout)
	id, code := randomID(), randomCode()
	// the browser and the device follow the same mode for this request,
	// even if the setting changes while it waits
	confirm := loadPairSettings().Confirm
	p.id, p.code, p.ip, p.confirm = id, code, ip, confirm
	p.approved, p.denied, p.unavailable = false, false, false
	p.expires, p.cancel = now.Add(pairTimeout), cancel
	p.mu.Unlock()

	go s.runPairing(ctx, cancel, id, code, ip, lang, confirm)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "code": code, "expiresIn": int(pairTimeout.Seconds()), "confirm": confirm})
}

// pairLock serialises device prompts so a replaced request has fully
// restored the screen before the next one paints.
var pairLock sync.Mutex

func (s *server) runPairing(ctx context.Context, cancel context.CancelFunc, id, code, ip, lang, confirm string) {
	defer cancel()
	pairLock.Lock()
	defer pairLock.Unlock()
	if ctx.Err() != nil {
		return
	}
	s.pair.mu.Lock()
	ended, hadPrompt := s.pair.lastEnd[ip]
	s.pair.mu.Unlock()
	if wait := pairCooldown - time.Since(ended); hadPrompt && wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
	}
	// Wake the LCD only if the launcher's screensaver blanked it. Apps keep
	// the screen on, so a blank screen means the launcher is in front, where
	// Shift does nothing; inside apps a key press could trigger something.
	if screenIsOff() {
		if err := s.keys.send(42, 1); err == nil {
			_ = s.keys.send(42, 0)
			time.Sleep(250 * time.Millisecond)
		}
	}
	// tells the launcher entry (zerodesk-screen) to leave the LCD to the prompt
	_ = os.WriteFile(pairingFlagPath(), nil, 0o600)
	// Without a visible prompt nobody can tell what the keys would approve,
	// so pairing is refused rather than waiting on a blind keyboard.
	var ok bool
	okOnly := confirm == pairConfirmOK
	show := ""
	if okOnly {
		show = code
	}
	overlay, err := newFBOverlay(s.fbPath, len(code), show, ip, lang)
	if err == nil {
		ok, err = waitForAnswer(ctx, s.kbdMatch, overlay, code, okOnly)
		_ = overlay.restore()
	} else {
		err = fmt.Errorf("overlay: %w", err)
	}
	_ = os.Remove(pairingFlagPath())
	if err != nil {
		log.Printf("pair: %v", err)
	}
	p := &s.pair
	p.mu.Lock()
	// the cooldown counts from when the prompt left the screen
	if p.lastPrompt != nil {
		p.lastPrompt[ip] = time.Now()
	}
	if p.lastEnd == nil {
		p.lastEnd = map[string]time.Time{}
	}
	for k, t := range p.lastEnd {
		if time.Since(t) >= pairCooldown {
			delete(p.lastEnd, k)
		}
	}
	p.lastEnd[ip] = time.Now()
	if p.id == id {
		p.approved = ok
		p.unavailable = err != nil
		p.denied = !ok && err == nil && ctx.Err() == nil
		if !ok {
			p.cancel = nil
		}
	}
	p.mu.Unlock()
	switch {
	case ok:
		log.Printf("pair: approved on device (%s)", ip)
	case err == nil && ctx.Err() == nil:
		log.Printf("pair: denied on device (%s)", ip)
	}
}

// handlePairStatus is polled by the browser; once approved it starts a
// session exactly once, and only for the browser that asked.
func (s *server) handlePairStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	p := &s.pair
	p.mu.Lock()
	defer p.mu.Unlock()
	if id == "" || id != p.id || clientIP(r) != p.ip {
		writeJSON(w, http.StatusOK, map[string]string{"state": "expired"})
		return
	}
	// expiry first: an approval nobody collected in time is not reusable
	if time.Now().After(p.expires) && !p.denied && !p.unavailable {
		p.id, p.approved, p.cancel = "", false, nil
		writeJSON(w, http.StatusOK, map[string]string{"state": "expired"})
		return
	}
	switch {
	case p.approved:
		p.id, p.approved, p.cancel = "", false, nil
		if err := s.startSession(w, r, "pair"); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"state": "approved"})
	case p.denied:
		writeJSON(w, http.StatusOK, map[string]string{"state": "denied"})
	case p.unavailable:
		writeJSON(w, http.StatusOK, map[string]string{"state": "unavailable"})
	case p.cancel == nil:
		writeJSON(w, http.StatusOK, map[string]string{"state": "expired"})
	default:
		writeJSON(w, http.StatusOK, map[string]string{"state": "waiting"})
	}
}

func (s *server) handlePairCancel(w http.ResponseWriter, r *http.Request) {
	p := &s.pair
	p.mu.Lock()
	if r.URL.Query().Get("id") == p.id && clientIP(r) == p.ip {
		if p.cancel != nil {
			p.cancel()
		}
		p.id, p.approved, p.cancel = "", false, nil
	}
	p.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func screenIsOff() bool {
	m, _ := filepath.Glob("/sys/class/backlight/*/brightness")
	for _, p := range m {
		if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) == "0" {
			return true
		}
	}
	return false
}
