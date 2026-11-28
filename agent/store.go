package main

import (
	"bufio"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The App Store window: the official CardputerZero catalogue (the same
// registry the on-device AppStore reads), with what is installed and what
// can be updated. Packages are downloaded by the Zero, checked against the
// registry's sha256 and installed with apt, which also installs the
// packages they depend on. Icons and screenshots are fetched by the Zero
// and cached, so the computer needs no access to GitHub.

var storeSources = map[string]string{
	// https://cardputerzero.github.io/generated/registry.json redirects to
	// plain http://cardputer.cc, so ask the HTTPS site directly (store_net.go)
	"github": "https://cardputer.cc/generated/registry.json",
	"cn":     "https://cardputer-zero-repo.oss-cn-shenzhen.aliyuncs.com/packages/cn/registry.json",
}

const (
	storeCacheTTL = 10 * time.Minute
	maxStoreAsset = 4 << 20
	maxStoreDeb   = 256 << 20
)

type storeApp struct {
	UUID        string            `json:"uuid"`
	Title       string            `json:"title"`
	Summary     string            `json:"summary"`
	Description string            `json:"description"`
	Locales     map[string]locale `json:"locales"`
	I18n        map[string]locale `json:"i18n"`
	Categories  []string          `json:"categories"`
	Author      struct {
		Name   string `json:"display_name"`
		GitHub string `json:"github"`
	} `json:"author"`
	Version     string          `json:"version"`
	PublishedAt string          `json:"published_at"`
	UpdatedAt   string          `json:"updated_at"`
	License     string          `json:"license"`
	SourceRepo  string          `json:"source_repo"`
	Permissions map[string]bool `json:"permissions"`
	Icon        string          `json:"icon"`
	Screenshots []string        `json:"screenshots"`
	Download    struct {
		Package string `json:"package"`
		URL     string `json:"url"`
		SHA256  string `json:"sha256"`
		Size    int64  `json:"size"`
	} `json:"download"`
}

type locale struct {
	Title       string `json:"title,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
}

// catalogApp is what the window gets: the registry entry plus local state.
type catalogApp struct {
	ID          string            `json:"id"`
	Package     string            `json:"package"`
	Title       string            `json:"title"`
	Summary     string            `json:"summary"`
	Description string            `json:"description"`
	Locales     map[string]locale `json:"locales,omitempty"`
	Categories  []string          `json:"categories"`
	Author      string            `json:"author"`
	Version     string            `json:"version"`
	PublishedAt string            `json:"published_at"`
	UpdatedAt   string            `json:"updated_at"`
	License     string            `json:"license"`
	SourceRepo  string            `json:"source_repo"`
	Permissions map[string]bool   `json:"permissions"`
	Icon        string            `json:"icon"`
	Screenshots []string          `json:"screenshots"`
	Size        int64             `json:"size"`
	Installed   string            `json:"installed,omitempty"`
	Update      bool              `json:"update,omitempty"`
	DesktopID   string            `json:"desktop_id,omitempty"`
}

type storeCache struct {
	mu      sync.Mutex
	source  string
	fetched time.Time
	apps    []storeApp
}

var store storeCache

var storeClient = &http.Client{Timeout: 60 * time.Second, CheckRedirect: storeCheckRedirect}

// storeOrder follows the on-device AppStore: the China mirror inside
// China, GitHub elsewhere; each falls back to the other.
func storeOrder(ctx context.Context) []string {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://ipinfo.io/country", nil)
	c := &http.Client{Timeout: 3 * time.Second}
	if res, err := c.Do(req); err == nil {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 16))
		res.Body.Close()
		if strings.TrimSpace(string(b)) == "CN" {
			return []string{"cn", "github"}
		}
	}
	return []string{"github", "cn"}
}

func (s *storeCache) catalog(ctx context.Context, force bool) ([]storeApp, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force && s.apps != nil && time.Since(s.fetched) < storeCacheTTL {
		return s.apps, s.source, nil
	}
	var last error
	order := []string{s.source}
	if s.source == "" {
		order = storeOrder(ctx)
	} else if s.source == "cn" {
		order = append(order, "github")
	} else {
		order = append(order, "cn")
	}
	for _, src := range order {
		apps, err := fetchRegistry(ctx, storeSources[src])
		if err != nil {
			last = err
			continue
		}
		s.apps, s.source, s.fetched = apps, src, time.Now()
		return apps, src, nil
	}
	if s.apps != nil {
		return s.apps, s.source, nil // stale beats nothing
	}
	return nil, "", last
}

func fetchRegistry(ctx context.Context, u string) ([]storeApp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	res, err := storeClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry: %s", res.Status)
	}
	var reg struct {
		Apps []storeApp `json:"apps"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&reg); err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	return reg.Apps, nil
}

// installedPackages reads dpkg's status file: package -> installed version.
func installedPackages() map[string]string {
	f, err := os.Open("/var/lib/dpkg/status")
	if err != nil {
		return nil
	}
	defer f.Close()
	out := map[string]string{}
	var pkg, ver, status string
	flush := func() {
		if pkg != "" && strings.HasSuffix(status, " installed") {
			out[pkg] = ver
		}
		pkg, ver, status = "", "", ""
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "Package: "):
			pkg = strings.TrimPrefix(line, "Package: ")
		case strings.HasPrefix(line, "Version: "):
			ver = strings.TrimPrefix(line, "Version: ")
		case strings.HasPrefix(line, "Status: "):
			status = strings.TrimPrefix(line, "Status: ")
		}
	}
	flush()
	return out
}

// packageDesktopID is the launcher entry an installed package added.
func packageDesktopID(pkg string) string {
	for _, name := range []string{pkg + ".list", pkg + ":arm64.list", pkg + ":all.list"} {
		b, err := os.ReadFile(filepath.Join("/var/lib/dpkg/info", name))
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "/usr/share/APPLaunch/applications/") && strings.HasSuffix(l, ".desktop") {
				return strings.TrimSuffix(filepath.Base(l), ".desktop")
			}
		}
	}
	return ""
}

func assetURL(u string) string {
	if u == "" {
		return ""
	}
	return "/api/store/asset?u=" + url.QueryEscape(u)
}

// GET /api/store/catalog[?refresh=1]
func handleStoreCatalog(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	apps, source, err := store.catalog(ctx, r.URL.Query().Get("refresh") == "1")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "the App Store cannot be reached: " + err.Error()})
		return
	}
	installed := installedPackages()
	out := make([]catalogApp, 0, len(apps))
	for _, a := range apps {
		c := catalogApp{
			ID: a.UUID, Package: a.Download.Package, Title: a.Title, Summary: a.Summary,
			Description: a.Description, Locales: a.Locales, Categories: a.Categories,
			Author: a.Author.Name, Version: a.Version, PublishedAt: a.PublishedAt,
			UpdatedAt: a.UpdatedAt, License: a.License, SourceRepo: a.SourceRepo,
			Permissions: a.Permissions, Icon: assetURL(a.Icon), Size: a.Download.Size,
		}
		if len(c.Locales) == 0 {
			c.Locales = a.I18n
		}
		for _, s := range a.Screenshots {
			c.Screenshots = append(c.Screenshots, assetURL(s))
		}
		if v, ok := installed[c.Package]; ok {
			c.Installed = v
			c.Update = compareDebVersions(a.Version, v) > 0
			c.DesktopID = packageDesktopID(c.Package)
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": source, "apps": out})
}

func storeCacheDir(sub string) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "zerodesk", "store", sub)
}

func allowedStoreURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || !storeURLAllowed(u) {
		return nil, false
	}
	return u, true
}

// GET /api/store/asset?u=<icon or screenshot URL from the registry>
func handleStoreAsset(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("u")
	u, ok := allowedStoreURL(raw)
	if !ok {
		http.Error(w, "not an App Store asset", http.StatusBadRequest)
		return
	}
	// icons and screenshots only: anything else could be served as a page
	ext := strings.ToLower(filepath.Ext(u.Path))
	if !storeImageExt[ext] {
		http.Error(w, "not an image", http.StatusBadRequest)
		return
	}
	sum := sha1.Sum([]byte(raw))
	path := filepath.Join(storeCacheDir("assets"), hex.EncodeToString(sum[:])+ext)
	if _, err := os.Stat(path); err != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := download(ctx, raw, path, maxStoreAsset, nil); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	setFileSafetyHeaders(w.Header())
	http.ServeFile(w, r, path)
}

var storeImageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true}

// download saves url to path (through a temporary file), reporting bytes.
func download(ctx context.Context, raw, path string, limit int64, progress func(done, total int64)) error {
	if _, ok := allowedStoreURL(raw); !ok {
		return errors.New("not an App Store address")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	c := &http.Client{CheckRedirect: storeCheckRedirect} // downloads may take minutes; ctx bounds them
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", res.Status)
	}
	if res.ContentLength > limit {
		return errors.New("download too large")
	}
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	var done int64
	buf := make([]byte, 32<<10)
	for {
		n, rerr := res.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				f.Close()
				os.Remove(tmp)
				return err
			}
			done += int64(n)
			if done > limit {
				f.Close()
				os.Remove(tmp)
				return errors.New("download too large")
			}
			if progress != nil {
				progress(done, res.ContentLength)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(tmp)
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var storeInstallLock sync.Mutex

// POST /api/store/install {"id": uuid, "password": ...}
// Streams text lines: "[download] <percent>", apt's output, "[exit] 0|<error>".
func handleStoreInstall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	apps, _, err := store.catalog(r.Context(), false)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	var app *storeApp
	for i := range apps {
		if apps[i].UUID == body.ID {
			app = &apps[i]
		}
	}
	if app == nil || app.Download.URL == "" || app.Download.Package == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such app"})
		return
	}
	// Every app in both registries has a checksum (243/243, 2026-11-28); a
	// package without one is not installed rather than installed unchecked.
	if !validSHA256(app.Download.SHA256) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "the App Store has no valid checksum for this package, so it cannot be checked"})
		return
	}
	if !storeInstallLock.TryLock() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "another app is being installed"})
		return
	}
	defer storeInstallLock.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Minute)
	defer cancel()
	// The password is checked first (the page asks again if it is wrong),
	// but root is only held for dpkg/apt: the download and the checksum run
	// as the device user.
	if writeAdminError(w, checkAdmin(ctx, body.Password), nil) {
		return
	}
	lw := newLineStream(w)
	path := filepath.Join(storeCacheDir("packages"), filepath.Base(app.Download.Package+"_"+app.Version+".deb"))
	defer os.Remove(path)
	last := -1
	err = download(ctx, app.Download.URL, path, maxStoreDeb, func(done, total int64) {
		if total <= 0 {
			total = app.Download.Size
		}
		if total > 0 {
			if p := int(done * 100 / total); p != last {
				last = p
				lw.line(fmt.Sprintf("[download] %d", p))
			}
		}
	})
	if err == nil {
		var sum string
		if sum, err = fileSHA256(path); err == nil && !strings.EqualFold(sum, app.Download.SHA256) {
			err = errors.New("the downloaded package does not match the App Store's checksum")
		}
	}
	if err == nil {
		adminMu.Lock()
		lw.run(ctx, storeRootInstallCmd(ctx, path, app.Download.SHA256, body.Password, !sudoWithoutPassword(ctx)))
		adminMu.Unlock()
	}
	if err != nil {
		lw.line("[exit] " + err.Error())
		return
	}
	log.Printf("store: installed %s %s", app.Download.Package, app.Version)
}

func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// storeRootInstallScript runs as root: it copies the package into a
// directory only root can write, checks the checksum again on that copy and
// installs exactly that copy, so the file in the device user's cache can
// change after the first check without effect.
const storeRootInstallScript = `set -e
exec </dev/null
src=$1; sum=$2; max=$3; shift 3
d=$(mktemp -d /tmp/zerodesk-pkg.XXXXXX)
trap 'rm -rf "$d"' EXIT
head -c "$((max + 1))" -- "$src" > "$d/p.deb"
if [ "$(wc -c < "$d/p.deb")" -gt "$max" ]; then
	echo "the package is larger than the App Store allows"
	exit 1
fi
if ! echo "$sum  $d/p.deb" | sha256sum -c --status; then
	echo "the package changed after it was checked"
	exit 1
fi
"$@" "$d/p.deb"`

// storeRootInstallCmd installs a checked Store package with one root
// command. With a password, sudo runs with -k: it asks for the password
// for this command only and leaves no cached credential that something
// else could use while dependencies download.
func storeRootInstallCmd(ctx context.Context, path, sha, password string, needPassword bool) *exec.Cmd {
	inner := []string{"env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "--no-install-recommends",
		"-o", "Dpkg::Use-Pty=0", "-o", "APT::Status-Fd=1"}
	if f, err := debControl(ctx, path); err == nil && dependsSatisfied(f["Pre-Depends"]+", "+f["Depends"], installedPackages()) {
		inner = []string{"env", "DEBIAN_FRONTEND=noninteractive", "dpkg", "--status-fd", "1", "-i"}
	}
	prefix := []string{"-n"}
	if needPassword {
		prefix = []string{"-k", "-S", "-p", ""}
	}
	args := append(prefix, "sh", "-c", storeRootInstallScript, "sh", path, strings.ToLower(sha), strconv.FormatInt(int64(maxStoreDeb), 10))
	cmd := exec.CommandContext(ctx, "sudo", append(args, inner...)...)
	if prefix[0] != "-n" {
		cmd.Stdin = strings.NewReader(password + "\n")
	}
	return cmd
}

// installCmd installs a local .deb. When everything it depends on is
// already installed it uses dpkg directly: apt needs about 18 s on a
// CardputerZero just to read the package lists. Otherwise apt installs the
// missing packages too.
func installCmd(ctx context.Context, path string) *exec.Cmd {
	if f, err := debControl(ctx, path); err == nil && dependsSatisfied(f["Pre-Depends"]+", "+f["Depends"], installedPackages()) {
		// --status-fd 1: "processing: unpack|configure: <pkg>" and "status: <pkg>: installed"
		return exec.CommandContext(ctx, "sudo", "-n", "env", "DEBIAN_FRONTEND=noninteractive",
			"dpkg", "--status-fd", "1", "-i", path)
	}
	return aptInstallCmd(ctx, path)
}

// dependsSatisfied checks a Depends field against the installed packages.
// Anything it cannot decide (virtual packages, unknown operators) counts as
// missing, so apt handles it.
func dependsSatisfied(field string, installed map[string]string) bool {
	for _, group := range strings.Split(field, ",") {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		ok := false
		for _, alt := range strings.Split(group, "|") {
			if depSatisfied(strings.TrimSpace(alt), installed) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func depSatisfied(dep string, installed map[string]string) bool {
	name, cons, _ := strings.Cut(dep, "(")
	name = strings.TrimSpace(name)
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[:i] // python3:any, libc6:arm64
	}
	have, ok := installed[name]
	if !ok {
		return false
	}
	cons = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(cons), ")"))
	if cons == "" {
		return true
	}
	var op string
	for _, o := range []string{">=", "<=", ">>", "<<", "="} {
		if strings.HasPrefix(cons, o) {
			op = o
			break
		}
	}
	want := strings.TrimSpace(strings.TrimPrefix(cons, op))
	c := compareDebVersions(have, want)
	switch op {
	case ">=":
		return c >= 0
	case "<=":
		return c <= 0
	case ">>":
		return c > 0
	case "<<":
		return c < 0
	case "=":
		return c == 0
	}
	return false
}

// aptInstallCmd installs a local .deb together with the packages it depends on.
func aptInstallCmd(ctx context.Context, path string) *exec.Cmd {
	// APT::Status-Fd=1 adds machine-readable progress to the output:
	// "dlstatus:<n>:<percent>:<text>" while dependencies download,
	// "pmstatus:<pkg>:<percent>:<text>" while dpkg unpacks and configures.
	return exec.CommandContext(ctx, "sudo", "-n", "env", "DEBIAN_FRONTEND=noninteractive",
		"apt-get", "install", "-y", "--no-install-recommends", "-o", "Dpkg::Use-Pty=0",
		"-o", "APT::Status-Fd=1", path)
}

// lineStream writes a text/plain response line by line.
type lineStream struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
}

func newLineStream(w http.ResponseWriter) *lineStream {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	f, _ := w.(http.Flusher)
	// send the headers now: the page shows its progress as soon as the
	// password is accepted, not when the command prints its first line
	if f != nil {
		f.Flush()
	}
	return &lineStream{w: w, flusher: f}
}

func (l *lineStream) write(b []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write(b)
	if l.flusher != nil {
		l.flusher.Flush()
	}
}

func (l *lineStream) line(s string) { l.write([]byte(s + "\n")) }

// run streams cmd's output and ends with "[exit] 0" or "[exit] <error>".
func (l *lineStream) run(ctx context.Context, cmd *exec.Cmd) {
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		l.line("[exit] " + err.Error())
		return
	}
	go func() {
		err := cmd.Wait()
		if err != nil {
			fmt.Fprintf(pw, "\n[exit] %v\n", err)
		} else {
			io.WriteString(pw, "\n[exit] 0\n")
		}
		pw.Close()
	}()
	buf := make([]byte, 4096)
	for {
		n, err := pr.Read(buf)
		if n > 0 {
			l.write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}
