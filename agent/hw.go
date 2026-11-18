package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Backends of the bundled hardware apps (hwapps/<id>/ in the repository,
// docs/HARDWARE-APPS.md). Each runs as `zerodesk hw <app>` in its own
// transient user unit started by the web-app host (docs/APP-SPEC.md §4) and
// serves HTTP on $ZERODESK_SOCKET. They run as the device user: hardware
// access comes from group membership, never from root.

var hwApps = map[string]func(mux *http.ServeMux) func(){
	"compass": hwCompass,
}

func runHW(args []string) int {
	debug.SetMemoryLimit(12 << 20)
	debug.SetGCPercent(50)
	if len(args) == 0 || hwApps[args[0]] == nil {
		fmt.Fprintln(os.Stderr, "usage: zerodesk hw <app>")
		return 2
	}
	sock := os.Getenv("ZERODESK_SOCKET")
	if sock == "" {
		fmt.Fprintln(os.Stderr, "ZERODESK_SOCKET not set")
		return 2
	}
	mux := http.NewServeMux()
	stop := hwApps[args[0]](mux)
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		log.Print(err)
		return 1
	}
	_ = os.Chmod(sock, 0o600)
	srv := &http.Server{Handler: mux}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		if stop != nil {
			stop()
		}
		_ = srv.Close()
	}()
	err = srv.Serve(ln)
	if stop != nil {
		stop()
	}
	if err != nil && err != http.ErrServerClosed {
		log.Print(err)
		return 1
	}
	return 0
}

// ---- helpers shared by the hardware backends ----

func hwJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func hwError(w http.ResponseWriter, status int, err string) {
	hwJSON(w, status, map[string]string{"error": err})
}

// hwHub fans events out to server-sent-event subscribers. onFirst/onLast
// start and stop the hardware reader so nothing is polled without a window.
type hwHub struct {
	mu      sync.Mutex
	subs    map[chan []byte]bool
	onFirst func()
	onLast  func()
}

func newHWHub(onFirst, onLast func()) *hwHub {
	return &hwHub{subs: map[chan []byte]bool{}, onFirst: onFirst, onLast: onLast}
}

func (h *hwHub) publish(event string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	msg := []byte("event: " + event + "\ndata: " + string(b) + "\n\n")
	h.mu.Lock()
	for c := range h.subs {
		select {
		case c <- msg:
		default: // slow reader: drop rather than block the hardware loop
		}
	}
	h.mu.Unlock()
}

func (h *hwHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// serve streams events; hello (if not nil) is sent first so a window never
// starts empty.
func (h *hwHub) serve(w http.ResponseWriter, r *http.Request, hello func() (string, any)) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	c := make(chan []byte, 64)
	h.mu.Lock()
	first := len(h.subs) == 0
	h.subs[c] = true
	h.mu.Unlock()
	if first && h.onFirst != nil {
		h.onFirst()
	}
	defer func() {
		h.mu.Lock()
		delete(h.subs, c)
		last := len(h.subs) == 0
		h.mu.Unlock()
		if last && h.onLast != nil {
			h.onLast()
		}
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if hello != nil {
		if ev, v := hello(); ev != "" {
			b, _ := json.Marshal(v)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev, b)
		}
	}
	fl.Flush()
	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case m := <-c:
			if _, err := w.Write(m); err != nil {
				return
			}
			fl.Flush()
		case <-keep.C:
			if _, err := w.Write([]byte(": keep-alive\n\n")); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// ---- sysfs ----

func sysRead(path string) (string, error) {
	b, err := os.ReadFile(path)
	return strings.TrimSpace(string(b)), err
}

func sysFloat(path string) (float64, error) {
	s, err := sysRead(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(s, 64)
}

// iioDevice finds an IIO device by its name attribute (the index differs
// between units).
func iioDevice(name string) string {
	dirs, _ := filepath.Glob("/sys/bus/iio/devices/iio:device*")
	for _, d := range dirs {
		if n, err := sysRead(filepath.Join(d, "name")); err == nil && n == name {
			return d
		}
	}
	return ""
}
