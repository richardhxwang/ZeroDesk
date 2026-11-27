package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tarEntry struct {
	name, body, link string
	dir              bool
	mode             int64
}

func writeTarGz(t *testing.T, entries []tarEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: 0o644}
		switch {
		case e.dir:
			h.Typeflag, h.Mode = tar.TypeDir, 0o755
		case e.link != "":
			h.Typeflag, h.Linkname = tar.TypeSymlink, e.link
		default:
			h.Typeflag, h.Size = tar.TypeReg, int64(len(e.body))
			if e.mode != 0 {
				h.Mode = e.mode
			}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	gz.Close()
	f.Close()
	return path
}

func TestUntarDropsSingleTopFolder(t *testing.T) {
	a := writeTarGz(t, []tarEntry{
		{name: "ohmyzsh-abc/", dir: true},
		{name: "ohmyzsh-abc/oh-my-zsh.sh", body: "x"},
		{name: "ohmyzsh-abc/themes/robbyrussell.zsh-theme", body: "y"},
	})
	dir := filepath.Join(t.TempDir(), "out")
	if err := untarGz(a, dir); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"oh-my-zsh.sh", "themes/robbyrussell.zsh-theme"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

func TestUntarKeepsRootFilesAndExecBit(t *testing.T) {
	a := writeTarGz(t, []tarEntry{{name: "starship", body: "#!bin", mode: 0o755}})
	dir := filepath.Join(t.TempDir(), "out")
	if err := untarGz(a, dir); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "starship"))
	if err != nil || st.Mode()&0o111 == 0 {
		t.Fatalf("program not unpacked as executable: %v %v", st, err)
	}
}

func TestUntarRefusesEscapes(t *testing.T) {
	for _, e := range [][]tarEntry{
		{{name: "../evil", body: "x"}},
		{{name: "a/../../evil", body: "x"}},
		{{name: "/etc/evil", body: "x"}},
	} {
		dir := filepath.Join(t.TempDir(), "out")
		if err := untarGz(writeTarGz(t, e), dir); err == nil {
			t.Errorf("%s: unpacked", e[0].name)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil")); err == nil {
			t.Errorf("%s: wrote outside the folder", e[0].name)
		}
	}
}

func TestUntarSkipsSymlinksThatLeave(t *testing.T) {
	a := writeTarGz(t, []tarEntry{
		{name: "pkg/", dir: true},
		{name: "pkg/ok", body: "x"},
		{name: "pkg/inside", link: "ok"},
		{name: "pkg/outside", link: "../../../etc/passwd"},
		{name: "pkg/absolute", link: "/etc/passwd"},
	})
	dir := filepath.Join(t.TempDir(), "out")
	if err := untarGz(a, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "inside")); err != nil {
		t.Error("link inside the add-on was dropped")
	}
	for _, n := range []string{"outside", "absolute"} {
		if _, err := os.Lstat(filepath.Join(dir, n)); err == nil {
			t.Errorf("link %s that leaves the add-on was made", n)
		}
	}
}

func TestRCBlockAddsOnceAndRemovesCleanly(t *testing.T) {
	rc := filepath.Join(t.TempDir(), ".bashrc")
	mine := "alias ll='ls -l'\nexport EDITOR=vim\n"
	os.WriteFile(rc, []byte(mine), 0o644)
	for range 2 {
		if err := setRCBlock(rc, "bash", true); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(rc)
	if n := strings.Count(string(b), rcBegin); n != 1 {
		t.Fatalf("block written %d times:\n%s", n, b)
	}
	if !strings.HasPrefix(string(b), mine) || !strings.Contains(string(b), "init.bash") {
		t.Fatalf("own lines changed or block missing:\n%s", b)
	}
	if err := setRCBlock(rc, "bash", false); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(rc)
	if string(b) != mine {
		t.Fatalf("after removing:\n%q\nwant\n%q", b, mine)
	}
}

func TestShellInitOrder(t *testing.T) {
	z := shellInit(shellState{Shell: "zsh", Prompt: "robbyrussell", Tools: []string{"zsh-plugins", "fzf", "zoxide"}}, "zsh")
	for _, want := range []string{`ZSH_THEME="robbyrussell"`, "oh-my-zsh.sh", "zoxide init zsh", "fzf --zsh", "zsh-autosuggestions.zsh", "zsh-syntax-highlighting.zsh"} {
		if !strings.Contains(z, want) {
			t.Errorf("init.zsh lacks %q:\n%s", want, z)
		}
	}
	// syntax highlighting must be sourced last
	if strings.Index(z, "zsh-syntax-highlighting.zsh") < strings.Index(z, "zsh-autosuggestions.zsh") || !strings.HasSuffix(strings.TrimSpace(z), `zsh-syntax-highlighting.zsh"`) {
		t.Errorf("highlighting is not last:\n%s", z)
	}
	b := shellInit(shellState{Shell: "bash", Prompt: "starship", Tools: []string{"fzf"}}, "bash")
	if !strings.Contains(b, "starship init bash") || !strings.Contains(b, "fzf --bash") || strings.Contains(b, "zsh") {
		t.Errorf("init.bash:\n%s", b)
	}
}

func TestApplyShellStateRefusesMissingAndWrongShell(t *testing.T) {
	loadPresets()
	home := t.TempDir()
	oldHome, oldRoot := shellHome, presetsRoot
	shellHome = func() string { return home }
	presetsRoot = func() string { return filepath.Join(home, "presets") }
	t.Cleanup(func() { shellHome, presetsRoot = oldHome, oldRoot })

	if err := applyShellState(shellState{Prompt: "starship"}); err == nil || !strings.Contains(err.Error(), "not downloaded") {
		t.Errorf("starship not downloaded: %v", err)
	}
	// pretend Oh My Zsh is downloaded: still refused on bash
	p := presetsByID["ohmyzsh"]
	os.MkdirAll(filepath.Join(home, "presets", "ohmyzsh"), 0o755)
	os.WriteFile(filepath.Join(home, "presets", "ohmyzsh", presetMarker), []byte(presetMarkerText(p)), 0o644)
	if err := applyShellState(shellState{Shell: "bash", Prompt: "robbyrussell"}); err == nil || !strings.Contains(err.Error(), "needs zsh") {
		t.Errorf("Oh My Zsh on bash: %v", err)
	}
	if err := applyShellState(shellState{Prompt: "nope"}); err == nil {
		t.Error("unknown prompt accepted")
	}
	// everything off writes no rc files
	if err := applyShellState(shellState{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".bashrc")); err == nil {
		t.Error("made a .bashrc with nothing on")
	}
}
