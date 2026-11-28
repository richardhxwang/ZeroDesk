package main

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
)

// Live connections per browser session. Sign-in is checked when a
// WebSocket (Terminal, the device screen, VNC, device apps) or an event
// stream opens; after that the connection runs on its own. When a session
// ends (log out, removed in Settings, Sign Out All, evicted, expired), every
// connection it opened is closed, so a removed browser cannot keep typing
// into a terminal it already had open. Closing a Terminal WebSocket also
// ends its shell.

type liveConn struct {
	cancel context.CancelFunc
	mu     sync.Mutex
	conn   net.Conn // set once a WebSocket takes over the connection
	closed bool
}

func (lc *liveConn) setConn(c net.Conn) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if lc.closed {
		c.Close()
		return
	}
	lc.conn = c
}

func (lc *liveConn) close() {
	lc.cancel()
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.closed = true
	if lc.conn != nil {
		lc.conn.Close()
	}
}

type liveConns struct {
	mu sync.Mutex
	m  map[string]map[*liveConn]struct{}
}

// longLived: requests that stay open after sign-in was checked.
func longLived(r *http.Request) bool {
	return upgradesToWebSocket(r) || strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

// track registers a long-lived request under owner (a session id) and
// returns the writer and request to serve it with, and a function to call
// when it ends. Other requests pass through unchanged.
func (lcs *liveConns) track(owner string, w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *http.Request, func()) {
	if !longLived(r) {
		return w, r, func() {}
	}
	return lcs.trackAll(owner, w, r)
}

// trackAll registers any request, for routes whose responses may stream
// without announcing it (web-app backends, zagent-web).
func (lcs *liveConns) trackAll(owner string, w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *http.Request, func()) {
	if owner == "" {
		return w, r, func() {}
	}
	ctx, cancel := context.WithCancel(r.Context())
	lc := &liveConn{cancel: cancel}
	lcs.mu.Lock()
	if lcs.m == nil {
		lcs.m = map[string]map[*liveConn]struct{}{}
	}
	if lcs.m[owner] == nil {
		lcs.m[owner] = map[*liveConn]struct{}{}
	}
	lcs.m[owner][lc] = struct{}{}
	lcs.mu.Unlock()
	done := func() {
		cancel()
		lcs.mu.Lock()
		delete(lcs.m[owner], lc)
		if len(lcs.m[owner]) == 0 {
			delete(lcs.m, owner)
		}
		lcs.mu.Unlock()
	}
	return &trackedWriter{ResponseWriter: w, lc: lc}, r.WithContext(ctx), done
}

// closeOwner closes everything a session opened.
func (lcs *liveConns) closeOwner(owner string) {
	lcs.mu.Lock()
	list := lcs.m[owner]
	delete(lcs.m, owner)
	lcs.mu.Unlock()
	for lc := range list {
		lc.close()
	}
}

func (lcs *liveConns) count(owner string) int {
	lcs.mu.Lock()
	defer lcs.mu.Unlock()
	return len(lcs.m[owner])
}

// trackedWriter remembers the connection a WebSocket hijacks.
type trackedWriter struct {
	http.ResponseWriter
	lc *liveConn
}

func (tw *trackedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, brw, err := http.NewResponseController(tw.ResponseWriter).Hijack()
	if err == nil {
		tw.lc.setConn(c)
	}
	return c, brw, err
}

func (tw *trackedWriter) Flush() {
	_ = http.NewResponseController(tw.ResponseWriter).Flush()
}

func (tw *trackedWriter) Unwrap() http.ResponseWriter { return tw.ResponseWriter }
