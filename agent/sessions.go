package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Browser sessions. Each signed-in browser gets its own random token in
// the zd_session cookie, and the server keeps only that token's SHA-256,
// so a copy of sessions.json cannot be used to sign in. The device access
// token (~/.config/zerodesk/token) signs a browser in or authorises a
// script (X-ZeroDesk-Token); it is no longer stored in a cookie.
//
// Principal, device, session and organization are separate identifiers so
// that more people, AI agents and devices can be added later without
// changing what a session record means. Today there is one principal per
// device (its owner) and one role (admin).

const (
	sessionCookie     = "zd_session"
	legacyCookie      = "zd_token"  // up to 0.4.x this cookie held the device token
	noticeCookie      = "zd_notice" // a reason for the sign-in page to show, no credential
	noticeUpgraded    = "upgraded"
	sessionTTL        = 30 * 24 * time.Hour
	sessionTouchEvery = time.Hour // sliding expiry is written at most this often
	maxSessions       = 64
)

type sessionRec struct {
	ID             string    `json:"id"`   // public id, for listing and revoking
	Hash           string    `json:"hash"` // SHA-256 of the cookie value
	PrincipalID    string    `json:"principal_id"`
	PrincipalKind  string    `json:"principal_kind"` // human | ai_employee | service
	DeviceID       string    `json:"device_id"`
	OrganizationID string    `json:"organization_id,omitempty"`
	Role           string    `json:"role"`
	Method         string    `json:"method"`              // pair | qr | token | upgrade | zagent
	ParentID       string    `json:"parent_id,omitempty"` // zagent: the session it was handed off from
	UserAgent      string    `json:"user_agent,omitempty"`
	IP             string    `json:"ip,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	LastSeen       time.Time `json:"last_seen"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type deviceIdentity struct {
	DeviceID string `json:"device_id"`
	OwnerID  string `json:"owner_principal_id"`
}

type sessionStore struct {
	mu     sync.Mutex
	dir    string
	ident  deviceIdentity
	byHash map[string]*sessionRec
	now    func() time.Time
	// onRevoke runs (without the lock held) for every removed session, so
	// what it opened, such as web-app windows, closes with it.
	onRevoke func(id string)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on Linux
	}
	return hex.EncodeToString(b)
}

func hashSessionToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "zerodesk")
}

// newSessionStore loads (or creates) the device identity and the session
// list from dir. Expired sessions are dropped on load.
func newSessionStore(dir string) (*sessionStore, error) {
	st := &sessionStore{dir: dir, byHash: map[string]*sessionRec{}, now: time.Now}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	idPath := filepath.Join(dir, "identity.json")
	if b, err := os.ReadFile(idPath); err == nil {
		_ = json.Unmarshal(b, &st.ident)
	}
	if st.ident.DeviceID == "" || st.ident.OwnerID == "" {
		if st.ident.DeviceID == "" {
			st.ident.DeviceID = "dev_" + randomHex(10)
		}
		if st.ident.OwnerID == "" {
			st.ident.OwnerID = "usr_" + randomHex(10)
		}
		if err := writeJSONFile(idPath, st.ident); err != nil {
			return nil, err
		}
	}
	if b, err := os.ReadFile(st.path()); err == nil {
		var list []*sessionRec
		if json.Unmarshal(b, &list) == nil {
			now := st.now()
			for _, rec := range list {
				if rec.Hash != "" && now.Before(rec.ExpiresAt) {
					st.byHash[rec.Hash] = rec
				}
			}
		}
	}
	return st, nil
}

func (st *sessionStore) path() string { return filepath.Join(st.dir, "sessions.json") }

// writeJSONFile writes v atomically with mode 0600, through a temporary
// file of its own so concurrent writers never share one.
func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, b)
}

// writeFileAtomic replaces path with b (mode 0600) through a temporary file.
func writeFileAtomic(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o600)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// writeSessions saves a session set. Every change is written first and
// applied to memory only once it is on disk, so a revocation that could not
// be saved is reported as failed and cannot come back after a restart.
func (st *sessionStore) writeSessions(set map[string]*sessionRec) error {
	list := make([]*sessionRec, 0, len(set))
	for _, rec := range set {
		list = append(list, rec)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	return writeJSONFile(st.path(), list)
}

func (st *sessionStore) saveLocked() error { return st.writeSessions(st.byHash) }

// withoutLocked returns the session set minus the sessions drop selects and
// every session handed off from a removed one, and the removed ids.
func (st *sessionStore) withoutLocked(drop func(rec *sessionRec) bool) (map[string]*sessionRec, []string) {
	next := make(map[string]*sessionRec, len(st.byHash))
	gone := map[string]bool{}
	for h, rec := range st.byHash {
		if drop(rec) {
			gone[rec.ID] = true
		} else {
			next[h] = rec
		}
	}
	for changed := true; changed; { // hand-offs of hand-offs
		changed = false
		for h, rec := range next {
			if rec.ParentID != "" && gone[rec.ParentID] {
				gone[rec.ID] = true
				delete(next, h)
				changed = true
			}
		}
	}
	ids := make([]string, 0, len(gone))
	for id := range gone {
		ids = append(ids, id)
	}
	return next, ids
}

// create starts a session for the device owner and returns the cookie value.
func (st *sessionStore) create(r *http.Request, method string) (string, sessionRec, error) {
	return st.createWith(r, method, "")
}

var (
	errSessionsFull = errors.New("too many signed-in browsers: sign one out in Settings")
	errNoParent     = errors.New("signed out")
)

// createWith starts a session. parent, if set, must still be signed in and
// the new session ends with it.
func (st *sessionStore) createWith(r *http.Request, method, parent string) (string, sessionRec, error) {
	tok := randomHex(32)
	now := st.now()
	rec := &sessionRec{
		ID:            "ses_" + randomHex(8),
		Hash:          hashSessionToken(tok),
		PrincipalID:   st.ident.OwnerID,
		PrincipalKind: "human",
		DeviceID:      st.ident.DeviceID,
		Role:          "admin",
		Method:        method,
		ParentID:      parent,
		CreatedAt:     now,
		LastSeen:      now,
		ExpiresAt:     now.Add(sessionTTL),
	}
	if r != nil {
		rec.IP = clientIP(r)
		if ua := r.UserAgent(); len(ua) > 300 {
			rec.UserAgent = ua[:300]
		} else {
			rec.UserAgent = ua
		}
	}
	st.mu.Lock()
	next, evicted := st.withoutLocked(func(rec *sessionRec) bool { return !now.Before(rec.ExpiresAt) })
	if parent != "" {
		alive := false
		for _, p := range next {
			if p.ID == parent {
				alive = true
			}
		}
		if !alive {
			st.mu.Unlock()
			return "", sessionRec{}, errNoParent
		}
	}
	if len(next) >= maxSessions {
		// A new browser does not push out one that is signed in; the owner
		// removes old ones in Settings (expired ones are already gone).
		st.mu.Unlock()
		return "", sessionRec{}, errSessionsFull
	}
	next[rec.Hash] = rec
	if err := st.writeSessions(next); err != nil {
		st.mu.Unlock()
		return "", sessionRec{}, err
	}
	st.byHash = next
	out := *rec
	st.mu.Unlock()
	st.notify(evicted)
	return tok, out, nil
}

// lookup returns the session for a cookie value. refresh is true when the
// expiry slid forward, so the caller can renew the cookie.
func (st *sessionStore) lookup(tok string) (rec sessionRec, ok, refresh bool) {
	if tok == "" {
		return sessionRec{}, false, false
	}
	h := hashSessionToken(tok)
	now := st.now()
	st.mu.Lock()
	defer st.mu.Unlock()
	r := st.byHash[h]
	// the map lookup is by hash, so a timing difference only reveals
	// whether some session's hash starts with the same bytes
	if r == nil || subtle.ConstantTimeCompare([]byte(r.Hash), []byte(h)) != 1 {
		return sessionRec{}, false, false
	}
	if !now.Before(r.ExpiresAt) {
		return sessionRec{}, false, false // removed for good by the next change
	}
	if now.Sub(r.LastSeen) >= sessionTouchEvery {
		r.LastSeen = now
		r.ExpiresAt = now.Add(sessionTTL)
		_ = st.saveLocked()
		refresh = true
	}
	return *r, true, refresh
}

func (st *sessionStore) list() []sessionRec {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]sessionRec, 0, len(st.byHash))
	now := st.now()
	for _, rec := range st.byHash {
		if now.Before(rec.ExpiresAt) {
			out = append(out, *rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}

// has reports whether a session id is signed in.
func (st *sessionStore) has(id string) bool {
	if id == "" {
		return false
	}
	now := st.now()
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, rec := range st.byHash {
		if rec.ID == id {
			return now.Before(rec.ExpiresAt)
		}
	}
	return false
}

// revokeWhere removes every session for which drop returns true, and the
// sessions handed off from them. Nothing changes if it cannot be saved.
func (st *sessionStore) revokeWhere(drop func(rec *sessionRec) bool) (int, error) {
	st.mu.Lock()
	next, gone := st.withoutLocked(drop)
	if len(gone) > 0 {
		if err := st.writeSessions(next); err != nil {
			st.mu.Unlock()
			return 0, err
		}
		st.byHash = next
	}
	st.mu.Unlock()
	st.notify(gone)
	return len(gone), nil
}

func (st *sessionStore) revoke(id string) (bool, error) {
	n, err := st.revokeWhere(func(rec *sessionRec) bool { return rec.ID == id })
	return n > 0, err
}

func (st *sessionStore) revokeToken(tok string) error {
	h := hashSessionToken(tok)
	_, err := st.revokeWhere(func(rec *sessionRec) bool { return rec.Hash == h })
	return err
}

func (st *sessionStore) notify(ids []string) {
	if st.onRevoke == nil {
		return
	}
	for _, id := range ids {
		st.onRevoke(id)
	}
}

// ---- request context ----

type sessionCtxKey struct{}

func withSession(ctx context.Context, rec sessionRec) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, rec)
}

// sessionFrom returns the browser session that authorised r, if any (a
// script using X-ZeroDesk-Token, or -no-auth, has none).
func sessionFrom(r *http.Request) (sessionRec, bool) {
	rec, ok := r.Context().Value(sessionCtxKey{}).(sessionRec)
	return rec, ok
}

// ---- cookies ----

func setSessionCookie(w http.ResponseWriter, tok string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL / time.Second),
	})
}

// setNotice leaves a reason for the sign-in page (readable by its script,
// holds nothing secret) until this browser signs in again.
func setNotice(w http.ResponseWriter, reason string) {
	http.SetCookie(w, &http.Cookie{Name: noticeCookie, Value: reason, Path: "/", MaxAge: 30 * 24 * 3600, SameSite: http.SameSiteStrictMode})
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

// errBadCredential: the token checked under credMu was not (or no longer) valid.
var errBadCredential = errors.New("wrong token")

// issueSession signs the browser behind r in. check, if set, runs under
// credMu together with the creation, so Sign Out All (which replaces the
// token under the same lock) cannot interleave with a sign-in that is
// checking the old token. parent ties the session to another (Zagent).
func (s *server) issueSession(w http.ResponseWriter, r *http.Request, method, parent string, check func() bool) (sessionRec, error) {
	if s.sessions == nil { // -no-auth
		return sessionRec{}, nil
	}
	s.credMu.Lock()
	defer s.credMu.Unlock()
	if check != nil && !check() {
		return sessionRec{}, errBadCredential
	}
	tok, rec, err := s.sessions.createWith(r, method, parent)
	if err != nil {
		return sessionRec{}, err
	}
	setSessionCookie(w, tok)
	clearCookie(w, legacyCookie)
	clearCookie(w, noticeCookie)
	return rec, nil
}

// startSession signs the browser behind r in (pairing, QR, Zagent hand-off
// already proved who it is).
func (s *server) startSession(w http.ResponseWriter, r *http.Request, method string) error {
	_, err := s.issueSession(w, r, method, "", nil)
	return err
}

// sweepExpired removes expired sessions for good (and what they opened).
func (st *sessionStore) sweepExpired() {
	now := st.now()
	expired := func(rec *sessionRec) bool { return !now.Before(rec.ExpiresAt) }
	if _, err := st.revokeWhere(expired); err != nil {
		log.Printf("sessions: could not remove expired sessions: %v", err)
		// Not saved, but they no longer sign anyone in (lookup checks the
		// expiry): close what they opened anyway.
		st.mu.Lock()
		_, gone := st.withoutLocked(expired)
		st.mu.Unlock()
		st.notify(gone)
	}
}

// ---- API: Settings › Signed-in browsers ----

type sessionView struct {
	ID        string    `json:"id"`
	Method    string    `json:"method"`
	UserAgent string    `json:"user_agent"`
	IP        string    `json:"ip"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
	Current   bool      `json:"current"`
}

func (s *server) handleSessionsList(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil {
		writeJSON(w, http.StatusOK, map[string]any{"sessions": []sessionView{}})
		return
	}
	cur, _ := sessionFrom(r)
	out := []sessionView{}
	for _, rec := range s.sessions.list() {
		out = append(out, sessionView{rec.ID, rec.Method, rec.UserAgent, rec.IP, rec.CreatedAt, rec.LastSeen, rec.ID == cur.ID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (s *server) handleSessionRevoke(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such session"})
		return
	}
	ok, err := s.sessions.revoke(r.PathValue("id"))
	switch {
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not sign it out: " + err.Error()})
	case !ok:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such session"})
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// handleSessionsReset signs out every other browser and replaces the device
// access token, so a copied token or an old zd_token cookie stops working.
func (s *server) handleSessionsReset(w http.ResponseWriter, r *http.Request) {
	if s.sessions == nil {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	cur, _ := sessionFrom(r)
	// Under credMu no sign-in can check the old token in between. The token
	// is replaced first: if that fails nothing is signed out and the page
	// says so; once it is replaced the old token cannot sign anyone in again.
	s.credMu.Lock()
	defer s.credMu.Unlock()
	if err := s.rotateToken(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not replace the access token: " + err.Error()})
		return
	}
	// From a browser: keep that browser (and its Zagent hand-off). From a
	// script (access token, no browser session): sign everyone out.
	n, err := s.sessions.revokeWhere(func(rec *sessionRec) bool {
		return cur.ID == "" || rec.ID != cur.ID && rec.ParentID != cur.ID
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "the access token was replaced, but other browsers could not be signed out: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "signed_out": n})
}
