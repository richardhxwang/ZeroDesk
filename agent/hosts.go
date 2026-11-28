package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Which names ZeroDesk answers to, and which requests may change things.
//
// Host: a page on another site can point its own name at this device's
// address (DNS rebinding) and then read ZeroDesk as if it were its own
// origin. ZeroDesk therefore answers only to its own names: localhost and
// loopback addresses, the device's current IP addresses, its hostname and
// <hostname>.local, zagent.local, and names the owner adds in Settings
// (for a reverse proxy or a custom domain).
//
// Origin: requests that change something (anything but GET, HEAD and
// OPTIONS, and WebSocket upgrades) must come from ZeroDesk's own pages. A
// browser says where a request comes from in Sec-Fetch-Site or Origin; a
// request with neither (curl, scripts with X-ZeroDesk-Token) is not from a
// browser page and is let through to the normal sign-in check.

const hostsFile = "hosts.json"

type hostPolicy struct {
	mu      sync.Mutex
	path    string
	extra   []string // names added in Settings, lower case
	ips     map[string]bool
	ipsAt   time.Time
	name    string // hostname, lower case
	disable bool   // -no-auth development mode: no checks
}

func loadHostPolicy() *hostPolicy {
	hp := &hostPolicy{path: filepath.Join(configDir(), hostsFile)}
	if h, err := os.Hostname(); err == nil {
		hp.name = strings.ToLower(h)
	}
	if b, err := os.ReadFile(hp.path); err == nil {
		var list []string
		if json.Unmarshal(b, &list) == nil {
			hp.extra = cleanHostList(list)
		}
	}
	return hp
}

// cleanHostList keeps valid host names or addresses (no scheme, port or
// path), lower-cased and without duplicates.
func cleanHostList(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, h := range in {
		h = strings.ToLower(strings.TrimSpace(h))
		h = strings.TrimSuffix(h, ".")
		if h == "" || len(h) > 253 || strings.ContainsAny(h, "/:@ \t?#") && net.ParseIP(strings.Trim(h, "[]")) == nil {
			continue
		}
		h = strings.Trim(h, "[]")
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}

// localIPs lists the device's addresses, refreshed at most every 10 s so a
// new DHCP lease is picked up without asking the kernel on every request.
func (hp *hostPolicy) localIPsLocked() map[string]bool {
	if hp.ips != nil && time.Since(hp.ipsAt) < 10*time.Second {
		return hp.ips
	}
	ips := map[string]bool{}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				ips[n.IP.String()] = true
			}
		}
	}
	hp.ips, hp.ipsAt = ips, time.Now()
	return ips
}

// allowed reports whether host (from the Host header, without the port) is
// one of this device's names.
func (hp *hostPolicy) allowed(host string) bool {
	if hp == nil || hp.disable {
		return true
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == zagentHost {
		return true
	}
	hp.mu.Lock()
	defer hp.mu.Unlock()
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || hp.localIPsLocked()[ip.String()] {
			return true
		}
		host = ip.String() // also matched against addresses added in Settings
	}
	if hp.name != "" && (host == hp.name || host == hp.name+".local") {
		return true
	}
	for _, h := range hp.extra {
		if host == h {
			return true
		}
	}
	return false
}

func (hp *hostPolicy) list() []string {
	hp.mu.Lock()
	defer hp.mu.Unlock()
	return append([]string{}, hp.extra...)
}

func (hp *hostPolicy) set(list []string) error {
	clean := cleanHostList(list)
	for i, h := range clean { // store addresses the way allowed() compares them
		if ip := net.ParseIP(h); ip != nil {
			clean[i] = ip.String()
		}
	}
	hp.mu.Lock()
	defer hp.mu.Unlock()
	if err := writeJSONFile(hp.path, clean); err != nil {
		return err
	}
	hp.extra = clean
	return nil
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

// requestScheme is the scheme the browser used: https when ZeroDesk serves
// TLS or a reverse proxy in front of it says so (X-Forwarded-Proto is set
// by the proxy; a page on another site cannot add it to a cross-site
// request without a CORS preflight, which ZeroDesk does not answer).
func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if p := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])); p == "https" || p == "http" {
		return p
	}
	return "http"
}

// hostPortKey normalises "host[:port]" for comparison, dropping the
// scheme's default port.
func hostPortKey(scheme, hostport string) string {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host, port = hostport, ""
	}
	if port == "" || scheme == "http" && port == "80" || scheme == "https" && port == "443" {
		port = ""
	}
	return strings.ToLower(strings.Trim(host, "[]")) + "|" + port
}

// sameOrigin reports whether a browser request comes from ZeroDesk's own
// pages. Origin, when sent, must match scheme, host and port exactly, and a
// request whose signals disagree is refused. Requests with neither Origin
// nor Sec-Fetch-Site are not from a page.
func sameOrigin(r *http.Request) bool {
	sfs := r.Header.Get("Sec-Fetch-Site")
	if sfs == "same-site" || sfs == "cross-site" {
		return false
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return true // no Origin: same-origin GET-like navigation, or not a browser
	}
	u, err := url.Parse(o)
	if err != nil || u.Host == "" || u.Scheme == "" {
		return false // includes "null" (sandboxed frames, file:)
	}
	scheme := requestScheme(r)
	return strings.EqualFold(u.Scheme, scheme) && hostPortKey(scheme, u.Host) == hostPortKey(scheme, r.Host)
}

// upgradesToWebSocket: any "websocket" token in Upgrade (it may list
// several protocols; the WebSocket library accepts such a list).
func upgradesToWebSocket(r *http.Request) bool {
	for _, v := range r.Header.Values("Upgrade") {
		for _, tok := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "websocket") {
				return true
			}
		}
	}
	return false
}

func changesState(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		// a WebSocket opens with GET but then sends input
		return upgradesToWebSocket(r)
	}
	return true
}

// guard applies the Host and Origin rules before anything else runs.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.noAuth {
			next.ServeHTTP(w, r)
			return
		}
		if !s.hosts.allowed(hostOnly(r.Host)) {
			log.Printf("guard: refused Host %q from %s", r.Host, clientIP(r))
			http.Error(w, "ZeroDesk does not answer to this name. Add it in Settings › General › Names.", http.StatusMisdirectedRequest)
			return
		}
		// /x/ on ZeroDesk's own names is web-app content: its sandboxed
		// frames send Origin "null", and every request there is checked
		// against its capability. On zagent.local the same path goes to
		// zagent-web, so it gets no exemption there.
		capPath := strings.HasPrefix(r.URL.Path, "/x/") && requestHost(r) != zagentHost
		if changesState(r) && !capPath && !sameOrigin(r) {
			log.Printf("guard: refused cross-site %s %s (Origin %q, Sec-Fetch-Site %q)", r.Method, r.URL.Path, r.Header.Get("Origin"), r.Header.Get("Sec-Fetch-Site"))
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-site request refused"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- Settings › General › Names ----

func (s *server) handleHostsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"hosts": s.hosts.list(), "hostname": s.hosts.name})
}

func (s *server) handleHostsPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hosts []string `json:"hosts"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&body); err != nil || len(body.Hosts) > 32 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if err := s.hosts.set(body.Hosts); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": s.hosts.list()})
}
