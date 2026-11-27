#!/usr/bin/env python3
"""Build agent/presets/catalog.json: the downloadable Terminal presets.

Nothing here ships in the package except the catalog itself. The device
downloads a preset's files only when someone picks it in Settings, checks
each file against the sha256 recorded here and keeps it under
~/.local/share/zerodesk/presets/<id>/.

Every source is pinned (a commit, a release tag or an npm version), so a
file never changes under its checksum. To add a preset, add it to THEMES or
FONTS and run:  python3 scripts/presets-catalog.py
"""
import hashlib
import json
import sys
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "agent" / "presets" / "catalog.json"

# Terminal colour schemes: iTerm2-Color-Schemes (MIT), Windows Terminal format,
# which has the same keys as xterm.js apart from purple/cursorColor.
SCHEMES_COMMIT = "39d0d4d40d14e9955cad02aa869ae63e5372d646"
SCHEMES_SOURCE = "https://github.com/mbadolato/iTerm2-Color-Schemes"
THEMES = [
    # (id, display name, light variant file, dark variant file) in windowsterminal/.
    # A family with both follows Appearance like the built-in scheme.
    # Solarized first: agnoster, the classic zsh theme that needs a Powerline
    # font, is made for Solarized (issue #3)
    ("solarized", "Solarized", "iTerm2 Solarized Light", "iTerm2 Solarized Dark"),
    ("dracula", "Dracula", None, "Dracula"),
    ("nord", "Nord", "Nord Light", "Nord"),
    ("catppuccin", "Catppuccin", "Catppuccin Latte", "Catppuccin Mocha"),
    ("tokyonight", "Tokyo Night", "TokyoNight Day", "TokyoNight Storm"),
    ("gruvbox", "Gruvbox", "Gruvbox Light", "Gruvbox Dark"),
    ("one-half", "One Half", "One Half Light", "One Half Dark"),
    ("rose-pine", "Rosé Pine", "Rose Pine Dawn", "Rose Pine"),
    ("github", "GitHub", "GitHub Light Default", "GitHub Dark Default"),
    ("kanagawa", "Kanagawa", "Kanagawa Lotus", "Kanagawa Wave"),
    ("everforest", "Everforest", "Everforest Light Med", "Everforest Dark Med"),
    ("monokai-pro", "Monokai Pro", "Monokai Pro Light", "Monokai Pro"),
    ("ayu", "Ayu", "Ayu Light", "Ayu"),
    ("apple", "Apple System Colors", "Apple System Colors Light", "Apple System Colors"),
    ("night-owl", "Night Owl", "Night Owlish Light", "Night Owl"),
]

# Fonts. Fontsource (OFL) serves Latin subsets as small woff2 files; the Nerd
# Fonts (Powerline and icon glyphs, used by zsh/fish prompt themes) come from
# the Nerd Fonts release tag as whole .ttf files.
FONTSOURCE = "5.3.0"
NERD = "v3.5.1"
FONTS = [
    # The Nerd Fonts first: MesloLGS is what Powerlevel10k recommends, and both
    # carry the Powerline glyphs zsh prompt themes draw with (issue #3).
    ("meslo-nerd", "MesloLGS Nerd Font", "MesloLGS Nerd Font Mono", True,
     [(w, f"https://raw.githubusercontent.com/ryanoasis/nerd-fonts/{NERD}/patched-fonts/Meslo/S/MesloLGSNerdFontMono-{s}.ttf")
      for w, s in ((400, "Regular"), (700, "Bold"))],
     "Apache-2.0", "https://www.nerdfonts.com"),
    ("jetbrains-mono-nerd", "JetBrains Mono Nerd Font", "JetBrainsMono Nerd Font Mono", True,
     [(w, f"https://raw.githubusercontent.com/ryanoasis/nerd-fonts/{NERD}/patched-fonts/JetBrainsMono/Ligatures/JetBrainsMonoNerdFontMono-{s}.ttf")
      for w, s in ((400, "Regular"), (700, "Bold"))],
     "OFL-1.1", "https://www.nerdfonts.com"),
    # (id, display name, css family, nerd, [(weight, url)], license, source)
    *[
        (fid, name, name, False,
         [(w, f"https://cdn.jsdelivr.net/npm/@fontsource/{fid}@{FONTSOURCE}/files/{fid}-latin-{w}-normal.woff2") for w in (400, 700)],
         lic, f"https://fontsource.org/fonts/{fid}")
        for fid, name, lic in [
            ("jetbrains-mono", "JetBrains Mono", "OFL-1.1"),
            ("fira-code", "Fira Code", "OFL-1.1"),
            ("ibm-plex-mono", "IBM Plex Mono", "OFL-1.1"),
            ("source-code-pro", "Source Code Pro", "OFL-1.1"),
            ("cascadia-code", "Cascadia Code", "OFL-1.1"),
            ("ubuntu-mono", "Ubuntu Mono", "UFL-1.0"),
        ]
    ],
]


def fetch(url: str) -> bytes:
    req = urllib.request.Request(url, headers={"User-Agent": "zerodesk-presets-catalog"})
    with urllib.request.urlopen(req, timeout=120) as r:
        return r.read()


def entry(url: str, name: str, data: bytes) -> dict:
    return {"name": name, "url": url, "size": len(data), "sha256": hashlib.sha256(data).hexdigest()}


def theme(tid: str, label: str, light: str | None, dark: str | None) -> dict:
    files, preview = [], {}
    for mode, fname in (("light", light), ("dark", dark)):
        if not fname:
            continue
        url = (f"https://raw.githubusercontent.com/mbadolato/iTerm2-Color-Schemes/{SCHEMES_COMMIT}/windowsterminal/"
               + urllib.parse.quote(fname) + ".json")
        data = fetch(url)
        c = json.loads(data)
        # enough to draw the tile before anything is downloaded
        preview[mode] = [c[k] for k in ("background", "foreground", "red", "green", "yellow", "blue", "purple", "cyan")]
        files.append(entry(url, f"{mode}.json", data))
    return {"id": tid, "name": label, "preview": preview, "license": "MIT", "source": SCHEMES_SOURCE, "files": files}


def font(fid, name, family, nerd, files, lic, source) -> dict:
    out = []
    for weight, url in files:
        data = fetch(url)
        ext = Path(urllib.parse.urlparse(url).path).suffix
        e = entry(url, f"{weight}{ext}", data)
        e["weight"] = weight
        out.append(e)
    return {"id": fid, "name": name, "family": family, "nerd": nerd, "license": lic, "source": source, "files": out}


def main() -> int:
    themes = []
    for t in THEMES:
        print("theme", t[0], file=sys.stderr)
        themes.append(theme(*t))
    fonts = []
    for f in FONTS:
        print("font ", f[0], file=sys.stderr)
        fonts.append(font(*f))
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(json.dumps({"themes": themes, "fonts": fonts}, ensure_ascii=False, indent=1) + "\n")
    print(f"wrote {OUT.relative_to(ROOT)}: {len(themes)} themes, {len(fonts)} fonts", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
