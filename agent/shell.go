package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Shell add-ons (Settings › Terminal): prompt styles and tools for the
// device's own shell, downloaded like the other presets (agent/presets.go)
// and unpacked under ~/.local/share/zerodesk/shell/<id>/.
//
// What is turned on lives in ~/.config/zerodesk/shell.json. From it ZeroDesk
// writes ~/.config/zerodesk/shell/init.bash and init.zsh, and adds one
// marked block to ~/.bashrc and ~/.zshrc that sources them; turning
// everything off takes the block out again. ZeroDesk's Terminal starts zsh
// when it is chosen there; the device's login shell is not changed.

type shellState struct {
	// "bash" or "zsh": what ZeroDesk's Terminal starts
	Shell string `json:"shell"`
	// "", "starship", "robbyrussell", "agnoster" (Oh My Zsh themes) or "powerlevel10k"
	Prompt string `json:"prompt"`
	// ids of the tools that are on: "fzf", "zoxide", "zsh-plugins"
	Tools []string `json:"tools"`
}

var (
	shellMu   sync.Mutex
	shellHome = func() string { h, _ := os.UserHomeDir(); return h }
)

func shellDataDir() string   { return filepath.Join(shellHome(), ".local", "share", "zerodesk", "shell") }
func shellConfigDir() string { return filepath.Join(shellHome(), ".config", "zerodesk") }

// the prompt styles and which add-on each one needs
var shellPrompts = map[string]string{
	"":              "",
	"starship":      "starship",
	"robbyrussell":  "ohmyzsh",
	"agnoster":      "ohmyzsh",
	"powerlevel10k": "powerlevel10k",
}

func loadShellState() shellState {
	st := shellState{Shell: "bash"}
	b, err := os.ReadFile(filepath.Join(shellConfigDir(), "shell.json"))
	if err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Shell != "zsh" {
		st.Shell = "bash"
	}
	return st
}

func zshPath() string {
	p, err := exec.LookPath("zsh")
	if err != nil {
		return ""
	}
	return p
}

// terminalShell is what ZeroDesk's Terminal runs: zsh when chosen and
// installed, otherwise the user's login shell.
func terminalShell() string {
	if loadShellState().Shell == "zsh" {
		if z := zshPath(); z != "" {
			return z
		}
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/bash"
}

// GET /api/shell
func handleShellGet(w http.ResponseWriter, r *http.Request) {
	shellMu.Lock()
	defer shellMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"state": loadShellState(), "zsh": zshPath() != ""})
}

// POST /api/shell {shell, prompt, tools}: what is on; the add-ons it needs
// must have been downloaded (POST /api/presets/{id}) first.
func handleShellSet(w http.ResponseWriter, r *http.Request) {
	var st shellState
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&st); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	shellMu.Lock()
	defer shellMu.Unlock()
	if err := applyShellState(st); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": loadShellState(), "zsh": zshPath() != ""})
}

// POST /api/shell/zsh {password}: install zsh with apt.
func handleShellInstallZsh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	if zshPath() != "" {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	err := withAdmin(r.Context(), body.Password, func() error {
		cmd := exec.CommandContext(r.Context(), "sudo", "-n", "env", "DEBIAN_FRONTEND=noninteractive",
			"apt-get", "install", "-y", "-o", "DPkg::Lock::Timeout=120", "zsh")
		if out, err := cmd.CombinedOutput(); err != nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			return fmt.Errorf("apt-get install zsh: %s", lines[len(lines)-1])
		}
		return nil
	})
	if writeAdminError(w, err, nil) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func applyShellState(st shellState) error {
	loadPresets()
	if st.Shell != "zsh" {
		st.Shell = "bash"
	}
	need, ok := shellPrompts[st.Prompt]
	if !ok {
		return fmt.Errorf("unknown prompt %q", st.Prompt)
	}
	var ids []string
	if need != "" {
		ids = append(ids, need)
	}
	tools := []string{}
	for _, t := range st.Tools {
		if (t == "fzf" || t == "zoxide" || t == "zsh-plugins") && !slices.Contains(tools, t) {
			tools = append(tools, t)
			ids = append(ids, t)
		}
	}
	st.Tools = tools
	for _, id := range ids {
		p := presetsByID[id]
		if p == nil || !presetInstalled(p) {
			return fmt.Errorf("%s is not downloaded", id)
		}
		if p.Zsh && st.Shell != "zsh" {
			return fmt.Errorf("%s needs zsh", p.Name)
		}
	}
	if st.Shell == "zsh" && zshPath() == "" {
		return errors.New("zsh is not installed")
	}
	if err := os.MkdirAll(filepath.Join(shellConfigDir(), "shell"), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(filepath.Join(shellConfigDir(), "shell.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	on := st.Prompt != "" || len(st.Tools) > 0
	for _, sh := range []string{"bash", "zsh"} {
		init := filepath.Join(shellConfigDir(), "shell", "init."+sh)
		if err := os.WriteFile(init, []byte(shellInit(st, sh)), 0o644); err != nil {
			return err
		}
		// a .zshrc is only made when zsh is in use; an existing one is always kept in step
		rc := filepath.Join(shellHome(), "."+sh+"rc")
		if _, err := os.Stat(rc); err != nil && !(on && sh == st.Shell) {
			continue
		}
		if err := setRCBlock(rc, sh, on); err != nil {
			return err
		}
	}
	return nil
}

const (
	rcBegin = "# >>> ZeroDesk shell add-ons >>>"
	rcEnd   = "# <<< ZeroDesk shell add-ons <<<"
)

// setRCBlock adds (on) or removes the marked block that sources init.<sh>.
func setRCBlock(rc, sh string, on bool) error {
	old, err := os.ReadFile(rc)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(old)
	if i := strings.Index(text, rcBegin); i >= 0 {
		if j := strings.Index(text[i:], rcEnd); j >= 0 {
			end := i + j + len(rcEnd)
			if end < len(text) && text[end] == '\n' {
				end++
			}
			text = strings.TrimRight(text[:i], "\n") + "\n" + text[end:]
			if strings.TrimSpace(text) == "" {
				text = ""
			}
		}
	}
	if on {
		if text != "" {
			// one blank line between the user's own lines and ours
			text = strings.TrimRight(text, "\n") + "\n\n"
		}
		text += rcBegin + "\n" +
			"# Settings > Terminal in ZeroDesk; turning the add-ons off there removes this.\n" +
			fmt.Sprintf("[ -r \"$HOME/.config/zerodesk/shell/init.%s\" ] && . \"$HOME/.config/zerodesk/shell/init.%s\"\n", sh, sh) +
			rcEnd + "\n"
	}
	if text == string(old) {
		return nil
	}
	tmp := rc + ".zerodesk-tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, rc)
}

// shellInit is init.bash / init.zsh for the state.
func shellInit(st shellState, sh string) string {
	data := shellDataDir()
	var b strings.Builder
	b.WriteString("# Written by ZeroDesk from Settings > Terminal; changes here are overwritten.\n")
	b.WriteString(fmt.Sprintf("export PATH=\"%s:$PATH\"\n", filepath.Join(data, "bin")))
	on := func(t string) bool { return slices.Contains(st.Tools, t) }
	if sh == "zsh" {
		switch st.Prompt {
		case "robbyrussell", "agnoster":
			b.WriteString(fmt.Sprintf("export ZSH=%q\nZSH_THEME=%q\nplugins=(git)\nDISABLE_AUTO_UPDATE=true\nZSH_DISABLE_COMPFIX=true\nsource \"$ZSH/oh-my-zsh.sh\"\n",
				filepath.Join(data, "ohmyzsh", "ohmyzsh"), st.Prompt))
		default:
			b.WriteString("autoload -Uz compinit && compinit -u\n")
		}
		switch st.Prompt {
		case "powerlevel10k":
			b.WriteString(fmt.Sprintf("source %q\n[[ -r ~/.p10k.zsh ]] && source ~/.p10k.zsh\n", filepath.Join(data, "powerlevel10k", "powerlevel10k", "powerlevel10k.zsh-theme")))
		case "starship":
			b.WriteString("eval \"$(starship init zsh)\"\n")
		}
		if on("zoxide") {
			b.WriteString("eval \"$(zoxide init zsh)\"\n")
		}
		if on("fzf") {
			b.WriteString("source <(fzf --zsh)\n")
		}
		if on("zsh-plugins") {
			// syntax highlighting has to come last
			b.WriteString(fmt.Sprintf("source %q\nsource %q\n",
				filepath.Join(data, "zsh-plugins", "autosuggestions", "zsh-autosuggestions.zsh"),
				filepath.Join(data, "zsh-plugins", "highlighting", "zsh-syntax-highlighting.zsh")))
		}
		return b.String()
	}
	if st.Prompt == "starship" {
		b.WriteString("eval \"$(starship init bash)\"\n")
	}
	if on("zoxide") {
		b.WriteString("eval \"$(zoxide init bash)\"\n")
	}
	if on("fzf") {
		b.WriteString("eval \"$(fzf --bash)\"\n")
	}
	return b.String()
}

/* ---- unpacking ---- */

// installShellPreset unpacks a downloaded add-on's archives into
// shell/<id>/<name>/ and links its program into shell/bin.
func installShellPreset(ctx context.Context, p *preset, archives string) error {
	dest := filepath.Join(shellDataDir(), p.ID)
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	for _, f := range p.Files {
		stem := strings.TrimSuffix(f.Name, ".tar.gz")
		if err := untarGz(filepath.Join(archives, f.Name), filepath.Join(dest, stem)); err != nil {
			os.RemoveAll(dest)
			return fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	if p.Bin != "" {
		bin := filepath.Join(shellDataDir(), "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			return err
		}
		target := filepath.Join(dest, strings.TrimSuffix(p.Files[0].Name, ".tar.gz"), p.Bin)
		if st, err := os.Stat(target); err != nil || st.Mode()&0o111 == 0 {
			return fmt.Errorf("%s: no program %s in the archive", p.ID, p.Bin)
		}
		link := filepath.Join(bin, p.Bin)
		os.Remove(link)
		if err := os.Symlink(target, link); err != nil {
			return err
		}
	}
	return nil
}

// untarGz unpacks a .tar.gz into dir, streaming. A single top-level folder
// (as in GitHub's source archives) is dropped. Nothing may land outside dir.
func untarGz(archive, dir string) error {
	walk := func(fn func(hdr *tar.Header, r io.Reader) error) error {
		f, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if hdr.Typeflag == tar.TypeXGlobalHeader || hdr.Typeflag == tar.TypeXHeader {
				continue
			}
			if err := fn(hdr, tr); err != nil {
				return err
			}
		}
	}
	clean := func(name string) string { return strings.TrimPrefix(filepath.ToSlash(name), "./") }

	// pass 1: refuse absolute and ".." names outright, and see whether
	// everything is inside one top-level folder
	prefix, single := "", true
	if err := walk(func(hdr *tar.Header, _ io.Reader) error {
		name := clean(hdr.Name)
		if strings.HasPrefix(name, "/") || slices.Contains(strings.Split(name, "/"), "..") {
			return fmt.Errorf("unsafe path %q", hdr.Name)
		}
		top, _, nested := strings.Cut(name, "/")
		if !nested && hdr.Typeflag != tar.TypeDir {
			single = false
		} else if prefix == "" {
			prefix = top + "/"
		} else if top+"/" != prefix {
			single = false
		}
		return nil
	}); err != nil {
		return err
	}
	if !single {
		prefix = ""
	}

	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	inside := func(p string) bool { return p == root || strings.HasPrefix(p, root+string(os.PathSeparator)) }
	var total int64
	// pass 2: unpack
	return walk(func(hdr *tar.Header, r io.Reader) error {
		name := strings.TrimPrefix(clean(hdr.Name), prefix)
		if name == "" || name == "/" {
			return nil
		}
		if filepath.IsAbs(name) || slices.Contains(strings.Split(name, "/"), "..") {
			return fmt.Errorf("unsafe path %q", hdr.Name)
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		if !inside(path) {
			return fmt.Errorf("unsafe path %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			return os.MkdirAll(path, 0o755)
		case tar.TypeReg:
			total += hdr.Size
			if total > 256<<20 {
				return errors.New("archive too large")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if hdr.Mode&0o111 != 0 {
				mode = 0o755
			}
			out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, r)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			return err
		case tar.TypeSymlink:
			// only links that stay inside the add-on
			target := hdr.Linkname
			if filepath.IsAbs(target) || !inside(filepath.Clean(filepath.Join(filepath.Dir(path), target))) {
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			os.Remove(path)
			return os.Symlink(target, path)
		}
		return nil
	})
}
