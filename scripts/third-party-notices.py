#!/usr/bin/env python3
"""Regenerate THIRD_PARTY_NOTICES.md from the licence files of what ZeroDesk ships.

Usage: python3 scripts/third-party-notices.py   (from the repository root, after
`pnpm install` in ui/ and `go mod download` in agent/)
"""
import json
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
NM = ROOT / "ui" / "node_modules"

MIT = """Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE."""

# (title, what it is used for, licence text source)
NPM = [
    ("svelte", "UI framework"),
    ("@neodrag/svelte", "window dragging"),
    ("@neodrag/core", "window dragging"),
    ("wavesurfer.js", "Voice Memos waveforms"),
    ("smplr", "Piano sample playback"),
    ("framework7-icons", "toolbar and sidebar glyphs"),
    ("@xterm/xterm", "Terminal"),
    ("@xterm/addon-fit", "Terminal"),
    ("@novnc/novnc", "Linux Desktop window"),
    ("@websr/webgl", "AI upscaling of screen windows"),
    ("popmotion", "Dock animation"),
]


def licence_file(d: Path):
    for p in sorted(d.iterdir()):
        if p.name.lower().startswith(("license", "licence", "copying")) and p.is_file():
            return p.read_text(errors="replace").strip()
    return None


def main():
    out = [
        "# Third-party notices",
        "",
        "ZeroDesk includes or builds on the software and assets below. Their licences require these notices.",
        "",
        "## macOS Web",
        "",
        "Parts of the window and desktop shell started from macOS Web by Puru Vijay (https://github.com/PuruVJ/macos-web).",
        "",
        "```",
        (ROOT / "ui" / "LICENSE").read_text().strip(),
        "```",
        "",
    ]
    for name, use in NPM:
        d = NM / name
        pkg = json.loads((d / "package.json").read_text())
        text = licence_file(d)
        if text is None:
            author = pkg.get("author")
            author = author.get("name") if isinstance(author, dict) else (author or name)
            text = f"MIT License\n\nCopyright (c) {author}\n\n{MIT}" if pkg.get("license") == "MIT" else None
        out += [f"## {name} {pkg['version']} ({pkg.get('license')})", "", f"Used for: {use}.", ""]
        if name == "@novnc/novnc":
            out += ["noVNC is distributed unmodified; its source is available at https://github.com/novnc/noVNC.", ""]
        out += ["```", text or "(see the package)", "```", ""]

    out += [
        "## Splendid Grand Piano samples",
        "",
        "Piano samples recorded by Akai and released into the public domain; SFZ mapping by kinwie (https://github.com/sfzinstruments/SplendidGrandPiano), converted for the web by smpldsnds. Downloaded by the device on first use.",
        "",
        "## Terminal presets",
        "",
        "Downloaded by the device only when picked in Settings › Terminal; the package carries the list (`agent/presets/catalog.json`, with a few colours of each scheme for its tile). "
        "Colour schemes: iTerm2-Color-Schemes by Mark Badolato and contributors (https://github.com/mbadolato/iTerm2-Color-Schemes, MIT). "
        "Fonts: JetBrains Mono, Fira Code, IBM Plex Mono, Source Code Pro and Cascadia Code (SIL Open Font License 1.1) and Ubuntu Mono (Ubuntu Font Licence 1.0), from Fontsource (https://fontsource.org); "
        "MesloLGS Nerd Font (Apache 2.0) and JetBrains Mono Nerd Font (OFL 1.1) from Nerd Fonts (https://www.nerdfonts.com, MIT for the patcher). "
        "Shell add-ons, unpacked into the device's ~/.local/share/zerodesk/shell: Starship (ISC, https://starship.rs), Oh My Zsh (MIT, https://ohmyz.sh), Powerlevel10k (MIT, https://github.com/romkatv/powerlevel10k), "
        "zsh-autosuggestions (MIT) and zsh-syntax-highlighting (BSD-3-Clause) (https://github.com/zsh-users), fzf (MIT, https://github.com/junegunn/fzf) and zoxide (MIT, https://github.com/ajeetdsouza/zoxide). "
        "Each preset's licence and source are listed in the catalog.",
        "",
        "## WebSR network weights (websr/cnn-m, websr/cnn-s)",
        "",
        "From https://github.com/sb2702/websr (MIT, same licence as @websr/webgl above), vendored in `ui/public/websr/`.",
        "",
    ]

    mods = subprocess.run(
        ["go", "list", "-deps", "-f", "{{if not .Standard}}{{with .Module}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}", "."],
        cwd=ROOT / "agent", capture_output=True, text=True, check=True,
    ).stdout.split("\n")
    seen = set()
    for line in mods:
        parts = line.split()
        if len(parts) != 3 or parts[0] in seen:
            continue
        seen.add(parts[0])
        text = licence_file(Path(parts[2]))
        out += [f"## Go module {parts[0]} {parts[1]}", "", "```", text or "(see the module)", "```", ""]

    out += [
        "## Build tools (not shipped)",
        "",
        "nfpm (https://github.com/goreleaser/nfpm, MIT) builds the Debian package; Vite and TypeScript build the web UI.",
        "",
    ]
    (ROOT / "THIRD_PARTY_NOTICES.md").write_text("\n".join(out))


if __name__ == "__main__":
    main()
