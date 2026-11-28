package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Zagent's own web page (zagent-web, ZeroAgent repo tools/zagent-web) listens
// on 127.0.0.1:42619. ZeroDesk owns port 80, so it forwards the mDNS name
// zagent.local there; those requests are marked as forwarded and zagent-web
// asks them to sign in with its own code. ZeroDesk's Zagent window reaches the
// chat endpoints through /api/zagent/web/*, behind ZeroDesk's sign-in.

const (
	zagentWebAddr = "127.0.0.1:42619"
	zagentHost    = "zagent.local"
)

var zagentWebTarget = &url.URL{Scheme: "http", Host: zagentWebAddr}

func requestHost(r *http.Request) string {
	host := strings.ToLower(r.Host)
	if h, _, ok := strings.Cut(host, ":"); ok && !strings.HasPrefix(host, "[") {
		return h
	}
	return host
}

// zagentWebHost sends requests for http://zagent.local to zagent-web.
func zagentWebHost(next http.Handler) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(zagentWebTarget)
			pr.SetXForwarded() // zagent-web: not a local caller, sign in
			pr.Out.Host = pr.In.Host
		},
		FlushInterval: -1, // chat replies stream as server-sent events
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestHost(r) == zagentHost {
			proxy.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var zagentWebPaths = map[string]bool{"chat": true, "chat/approve": true, "chat/stop": true, "status": true}

// handleZagentWeb relays the chat endpoints for ZeroDesk's Zagent window.
func handleZagentWeb() http.HandlerFunc {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(zagentWebTarget)
			pr.Out.URL.Path = "/api/" + pr.In.PathValue("path")
			pr.Out.URL.RawPath = ""
			// The caller already passed ZeroDesk's sign-in: no forwarding
			// headers (zagent-web then treats it as the device itself) and
			// no ZeroDesk cookie downstream.
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("Forwarded")
			pr.Out.Header.Del("Cookie")
		},
		FlushInterval: -1,
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !zagentWebPaths[r.PathValue("path")] {
			http.NotFound(w, r)
			return
		}
		ensureZagentWeb()
		proxy.ServeHTTP(w, r)
	}
}

// zagent-web starts on demand: Zagent enables it when its own sign-in code
// is shown, and its installer does not start it. ZeroDesk's Zagent window
// needs it as well, so ZeroDesk starts it (through Zagent's own user unit,
// with whatever address that unit is set to) when it does not answer. At
// most one attempt every 30 seconds, so a Zagent that cannot start is not
// restarted on every poll of the window.
var zagentWebStart struct {
	mu   sync.Mutex
	last time.Time
}

func zagentWebUp(timeout time.Duration) bool {
	c, err := net.DialTimeout("tcp", zagentWebAddr, timeout)
	if err == nil {
		c.Close()
	}
	return err == nil
}

func ensureZagentWeb() {
	if zagentWebUp(300 * time.Millisecond) {
		return
	}
	zagentWebStart.mu.Lock()
	defer zagentWebStart.mu.Unlock()
	if zagentWebUp(300*time.Millisecond) || time.Since(zagentWebStart.last) < 30*time.Second {
		return
	}
	zagentWebStart.last = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := systemctlUser(ctx, "start", "zagent-web.service"); err != nil {
		log.Printf("zagent: could not start zagent-web: %v", err)
		return
	}
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if zagentWebUp(300 * time.Millisecond) {
			log.Printf("zagent: started zagent-web for the Zagent window")
			return
		}
	}
	log.Printf("zagent: zagent-web started but does not answer on %s", zagentWebAddr)
}
