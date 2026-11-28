package main

import (
	"errors"
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
// zagent.local there, but only for a browser signed in to ZeroDesk (zagent-web
// still asks forwarded requests for its own code as well). zagent.local is a
// different site, so it has its own session cookie: a browser signed in to
// ZeroDesk gets one through /api/zagent/handoff, which hands a one-time
// ticket to zagent.local. ZeroDesk's Zagent window reaches the chat endpoints
// through /api/zagent/web/*, behind ZeroDesk's sign-in.

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

const (
	zagentHandoffPath = "/_zerodesk/handoff"
	zagentTicketTTL   = 30 * time.Second
)

// zagentTickets are one-time tickets that sign a browser in on zagent.local.
// A ticket belongs to the ZeroDesk session that asked for it; the Zagent
// session it becomes is that session's child and ends with it.
type zagentTickets struct {
	mu sync.Mutex
	m  map[string]zagentTicket
}

type zagentTicket struct {
	parent  string
	expires time.Time
}

const maxZagentTickets = 32

func (z *zagentTickets) issue(parent string) string {
	t := randomHex(24)
	now := time.Now()
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.m == nil {
		z.m = map[string]zagentTicket{}
	}
	for k, tk := range z.m {
		if now.After(tk.expires) {
			delete(z.m, k)
		}
	}
	if len(z.m) >= maxZagentTickets {
		for k := range z.m { // bounded: drop one
			delete(z.m, k)
			break
		}
	}
	z.m[t] = zagentTicket{parent: parent, expires: now.Add(zagentTicketTTL)}
	return t
}

// take uses up a ticket and returns the session it was issued to.
func (z *zagentTickets) take(t string) (string, bool) {
	z.mu.Lock()
	defer z.mu.Unlock()
	tk, ok := z.m[t]
	delete(z.m, t)
	return tk.parent, ok && time.Now().Before(tk.expires)
}

// portOf returns ":port" from a Host header, or "" for the default port.
func portOf(hostport string) string {
	if _, p, err := net.SplitHostPort(hostport); err == nil && p != "" && p != "80" {
		return ":" + p
	}
	return ""
}

// handleZagentHandoff (on ZeroDesk's own name) sends a signed-in browser to
// zagent.local with a ticket; a browser that is not signed in goes to the
// sign-in page.
func (s *server) handleZagentHandoff(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	r2, status := s.authenticateStatus(w, r)
	if status != 0 {
		// sign in first; the page comes back here afterwards (fixed path)
		http.Redirect(w, r, "/?after=zagent", http.StatusSeeOther)
		return
	}
	to := "http://" + zagentHost + portOf(r.Host) + "/"
	if !s.noAuth {
		cur, ok := sessionFrom(r2)
		if !ok { // a script with the access token has no browser session to hand off
			http.Error(w, "sign in from a browser first", http.StatusForbidden)
			return
		}
		to = "http://" + zagentHost + portOf(r.Host) + zagentHandoffPath + "?t=" + s.zagent.issue(cur.ID)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// withoutZeroDeskCredentials drops ZeroDesk's cookies and token header, so
// zagent-web never sees them; its own cookies on zagent.local pass through.
func withoutZeroDeskCredentials(h http.Header) {
	h.Del("X-ZeroDesk-Token")
	// parse the way net/http does when it authenticates, so a cookie that
	// counted as ours is never forwarded
	cookies := (&http.Request{Header: http.Header{"Cookie": h.Values("Cookie")}}).Cookies()
	h.Del("Cookie")
	var keep []string
	for _, c := range cookies {
		if c.Name != sessionCookie && c.Name != legacyCookie {
			keep = append(keep, (&http.Cookie{Name: c.Name, Value: c.Value, Quoted: c.Quoted}).String())
		}
	}
	if len(keep) > 0 {
		h.Set("Cookie", strings.Join(keep, "; "))
	}
}

// zagentWebHost sends requests for http://zagent.local to zagent-web, for
// browsers signed in to ZeroDesk.
func (s *server) zagentWebHost(next http.Handler) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(zagentWebTarget)
			pr.SetXForwarded() // zagent-web: not a local caller, sign in
			pr.Out.Host = pr.In.Host
			withoutZeroDeskCredentials(pr.Out.Header)
		},
		FlushInterval: -1, // chat replies stream as server-sent events
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestHost(r) != zagentHost {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == zagentHandoffPath {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			if parent, ok := s.zagent.take(r.URL.Query().Get("t")); ok && !s.noAuth {
				// the session that asked must still be signed in; the new
				// one ends with it
				if _, err := s.issueSession(w, r, "zagent", parent, nil); err != nil && !errors.Is(err, errNoParent) {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		r2, status := s.authenticateStatus(w, r)
		if status != 0 {
			// sign in on ZeroDesk first (same port); it sends the browser
			// back with a ticket
			home := "localhost"
			if s.hosts != nil && s.hosts.name != "" {
				home = s.hosts.name + ".local"
			}
			http.Redirect(w, r, "http://"+home+portOf(r.Host)+"/api/zagent/handoff", http.StatusSeeOther)
			return
		}
		rec, _ := sessionFrom(r2)
		w2, r3, done := s.conns.trackAll(rec.ID, w, r2)
		defer done()
		if rec.ID != "" && (!s.sessions.has(rec.ID) || !s.sessionUsableHere(rec, r)) { // signed out meanwhile
			http.Error(w, "signed out", http.StatusUnauthorized)
			return
		}
		proxy.ServeHTTP(w2, r3)
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
		proxy.ServeHTTP(w, r)
	}
}
