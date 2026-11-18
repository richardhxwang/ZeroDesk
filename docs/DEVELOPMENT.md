# ZeroDesk development

Developer notes. End-user documentation is in the [README](../README.md).

## Layout

| Path | What |
|---|---|
| `agent/` | Device service (Go, single static arm64 binary). The web UI is embedded, so every front-end change needs a Go rebuild |
| `agent/deploy/zerodesk.service` | systemd **user** unit, listens on `:8686` and `:80` |
| `ui/` | Front end: Svelte 5 (runes) + Vite; the window shell started from [macOS Web](https://github.com/PuruVJ/macos-web) by PuruVJ (MIT, see `ui/LICENSE` and `THIRD_PARTY_NOTICES.md`). Path alias `🍎/` = `ui/src/`. Build output goes to `agent/web/` |
| `packaging/` | Debian package (nfpm): `build-deb.sh`, the package's systemd user unit, maintainer scripts |
| `examples/level/` | Example app with its own ZeroDesk window (docs/APP-SPEC.md) |
| `vfb/` | `LD_PRELOAD` virtual framebuffer for running third-party device apps inside a window |
| `design/icon-gen/` | Generated icon artwork; `ui/scripts/build-icons.py` turns it into `ui/public/icons/*.webp` |
| `docs/macos-reference.md` | Measured macOS 26/27 specs (window, sidebar, toolbar, lists). Follow it |
| `docs/cardputer-settings.md` | Every device setting and the command or file behind it |
| `docs/APP-SPEC.md` | Format for apps that ship a native ZeroDesk window |

## Windows and their data sources

| Window | Source |
|---|---|
| Cardputer | `/dev/fb0` (320×170 RGB565), only changed rows are sent; keys are written to the `tca8418` evdev node |
| Linux desktop | system wayvnc started on demand (127.0.0.1 only, stopped 20 s after the last viewer), shown with noVNC |
| Finder | `/api/fs/*`, uploads stream to disk, `.deb` installs via `sudo dpkg -i` |
| Terminal | pty + xterm.js |
| Apps | `/usr/share/APPLaunch/applications/*.desktop`; opening one stops the launcher service, runs the app and restores the launcher when it exits |
| Settings | `/api/settings`, `/api/wifi/*`, `/api/system/reboot` (see `docs/cardputer-settings.md`) |
| Voice Memos | `/api/rec/*`, `/api/audio/peaks` |
| Web apps | `/api/webapps/*` and `/x/<capability>/` (docs/APP-SPEC.md); backends as transient `zerodesk-app-<id>` user units |
| Piano | Splendid Grand Piano samples via `/api/piano/{name}` (fetched once by the device, cached in `~/.local/share/zerodesk/piano`) |

## Virtual screens: sharpness and input

- Scaling (`ui/src/lib/upscale.ts`): **Pixel** picks a whole number of device pixels per LCD pixel and snaps the canvas to the device-pixel grid (`ScreenView.svelte`). **Smooth** fills the window with WebSR (`@websr/webgl`, network `websr/cnn-m`, falling back to `cnn-s`; weights vendored in `ui/public/websr/`, sha256-checked against the WebSR docs) and falls back to a sharp-bilinear shader. WebGPU is not used: it needs a secure context and ZeroDesk is plain HTTP.
- Scrolling: wheel notches and trackpad pixels become arrow keys (vertical and horizontal); a pause starts a new gesture.
- Mouse: apps on a virtual screen get a pointer FIFO in `LV_LINUX_MOUSE_DEVICE` (absolute `ABS_X/ABS_Y` + `BTN_TOUCH`/`BTN_LEFT`, screen pixels). Only apps that honour that variable (cp0_lvgl's `cp0_keyboard_create_lvgl_input_devices`) react; the official apps are keypad-only, so their mouse support comes from native ZeroDesk windows. The device's own screen gets no pointer: LVGL opens the device blocking, so a FIFO in the launcher's environment would stall its start-up whenever ZeroDesk is not running.

## Build

```bash
cd ui && pnpm install && pnpm build
cd ../agent && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o build/zerodesk .
```

## Editions

One codebase, two Debian packages, same package name `zerodesk` and the same Store-compliant layout. The goal is no difference in features; every row below that differs is a bug to remove.

| | Store edition | GitHub edition |
|---|---|---|
| Built with | `go build -tags store` (`agent/edition_store.go`) | default build (`agent/edition.go`) |
| File | `zerodesk_<version>-<rev>_arm64.deb` | `zerodesk_<version>-<rev>+full_arm64.deb` |
| Distributed | CardputerZero Store | GitHub Releases |
| Features | all | all |
| Administrator actions (install/remove packages, set the clock, ADB) | password prompt; none when `sudo` works without one | same |
| Launcher entries that start through `sudo` (virtual screen, Open on Zero) | started with the app's own sudo rule | same |

Earlier Store-only restrictions and how they were lifted:

| Restriction (removed) | Why it was lifted |
|---|---|
| Store edition always asked for the password | The system Store's own prompt (`cp0_sudo`) runs `sudo -S -v`, which passes without a password when `sudo` allows it; ZeroDesk now does the same in both editions |
| Store edition refused to start launcher entries whose `Exec` begins with `sudo` (the original NFC, SubG Chat, LoRa GPS apps) | Their root rights come from the app's own package (`/etc/sudoers.d/m5cardputerzero-cap-*`), exactly as when the launcher starts them; ZeroDesk holds no rights of its own |

`/api/session` reports the edition (`edition: "store" | "full"`).

### Root audit (Store rule: "Applications that run with root privileges are not allowed")

ZeroDesk runs as the system service `zerodesk.service` with `User=pi` (Store policy: system services must name a non-root user; same layout as the published `czproxy`). At start it sets `XDG_RUNTIME_DIR` and the session bus address itself (`agent/sessionenv.go`) so it can still reach the user's systemd (launcher, web-app backends) and PulseAudio.

| Was | Now | Why it is allowed |
|---|---|---|
| `sudo setcap` in deploy | `setcap cap_net_bind_service` in `postinst` (install time) | Port 80 without root; the published `czproxy` package does the same |
| `sudo timedatectl set-ntp` | `systemctl start/stop systemd-timesyncd` | Raspberry Pi OS polkit rules let any user manage that unit |
| `systemctl reboot` | unchanged | polkit allows members of `sudo` (factory-test rules) |
| `sudo kill` of a root foreground app | hold ESC (key injection) | the launcher's own force-quit ends the app with the launcher's rights |
| `sudo dpkg -i`, `sudo apt-get remove`, `sudo date`/`hwclock`, `sudo cardputer-adb` | user enters their password; `sudo -S -v`, the fixed command with `sudo -n`, then `sudo -K` (`agent/privileged.go`) | the same model as the system Store's `cp0_sudo` password prompt; ZeroDesk holds no rights of its own and keeps no password |
| starting `sudo …` launcher entries (virtual screen, Open on Zero) | started as the launcher does, with the app's own sudo rule | the rights belong to the app's package, not to ZeroDesk |

Nothing is left out of the Store edition. The hardware apps are being built on branch `feat/hardware-apps` (docs/HARDWARE-APPS.md); any edition difference they need will be listed in the table above.

## Launcher entry

`/usr/bin/zerodesk-screen` (a link to `zerodesk`, `agent/devicescreen.go`) is the app the launcher starts: a 320×170 status screen with ZeroDesk's state and the address to open on a computer, ESC to quit. It reads the keyboard without grabbing it, so the service can still grab it for a pairing prompt; while that prompt is up (`$XDG_RUNTIME_DIR/zerodesk-pairing` exists) it does not redraw.

## Package

```bash
packaging/build-deb.sh 0.2.0 1      # -> dist/zerodesk_0.2.0-1_arm64.deb and dist/zerodesk_0.2.0-1+full_arm64.deb
scripts/store-check.sh dist/zerodesk_0.2.0-1_arm64.deb
```

`build-deb.sh` needs pnpm, Go, Docker (the virtual-screen library is compiled in an arm64 `debian:trixie` container) and Python with Pillow (`packaging/store-assets.py` makes the 100 px launcher icon and the 256 px Store icon). `store-check.sh` runs, in Docker, the same checks as `czdev publish` before it uploads anything (with a stub GitHub login) and the packages repository's validate-pr checks, using fresh clones of `CardputerZero/AppBuilder` and `CardputerZero/packages` in `.cache/`. Nothing is uploaded.

Package contents: `/usr/bin/zerodesk`, `/usr/bin/zerodesk-screen`, `/usr/lib/systemd/system/zerodesk.service`, `/usr/share/APPLaunch/applications/zerodesk.desktop` and its icon, `/usr/lib/zerodesk/zd_vfb.so`, `/usr/share/doc/zerodesk/`. Store listing: `app-builder.json`, `store/icon.png`, `store/screenshots/` (rendered by `TestRenderScreen` with a placeholder address from 192.0.2.0/24).

## Deploy to a device

```bash
./deploy.sh pi@<device-host>
```

Builds both parts, copies the binary and the unit, compiles the virtual-framebuffer library on the device, re-grants `cap_net_bind_service` (replacing the file drops it) and restarts the service. Run it with a timeout; SSH over a flaky Wi-Fi link can hang.

The device's `pi` user has **no** passwordless sudo (only a cached sudo timestamp can make `sudo -n` work). `deploy.sh` then skips `setcap`, and port 80 stays off until someone runs `sudo setcap cap_net_bind_service=+ep ~/.local/bin/zerodesk` on the device.

A development install lives in `~/.local/bin/zerodesk` with a user unit in `~/.config/systemd/user/`. It and the package's system service both listen on 8686/80, so stop one before running the other.

Web UI auth: cookie `zd_token` = contents of `~/.config/zerodesk/token` on the device. Never print or commit the token.

## Screenshot checks

Before calling a UI change done, screenshot it in light and dark at 1440 and 375 px wide: Playwright with the local Chrome (`channel: 'chrome'`), `deviceScaleFactor: 2`, cookie `zd_token`, and `localStorage['zerodesk:preferences'] = {"appearance":"light"|"dark","theme":{"scheme":"light"|"dark"},"lang":"zh"}` via `addInitScript`. Screenshot `section.window.active` for app windows. Dock items have `aria-label`. Check for line overflow and visible scrollbars.

## UI rules

1. Research the real macOS app / HIG first; match real layouts (`docs/macos-reference.md`).
2. Reuse existing libraries instead of hand-rolling (in use: wavesurfer.js, framework7-icons, smplr).
3. Official CardputerZero apps get native macOS-style web apps, not an emulated screen.
4. Icons: original art in macOS 27 grammar, light and dark variants, flat, real tile corner shape; third-party icons without a white plate.
5. No blue accent, no gradients in UI chrome (icon art is exempt). Short labels, no explanatory prose.

## Known gotchas

- lightningcss drops `backdrop-filter` when a `-webkit-` version follows it: write only the unprefixed property.
- The default ALSA device has no capture; the recorder uses `plughw:N,M` from `/proc/asound/pcm`.
- `~/.config/cardputerzero/config.json` belongs to the launcher; the agent only replaces top-level keys (`brightness`, `dark_time`, `volume`) and the launcher may overwrite them.
- Piano sample names mix case (`PP`, `Mp`, `Mf`, `MF`, `FF`); the agent's name check must allow lower case.

## Measured footprint (2026-11-17, real device)

| State | ZeroDesk | wayvnc |
|---|---|---|
| Idle | 5.9 MB | not running |
| Device screen + Linux desktop open | 8.7 MB / 7% CPU | 39 MB / 10% CPU |

## Open work

1. Native apps for the official CardputerZero apps (Compass, Camera, IR Remote / IR Chat, LoRa, LoRa GPS, NFC, SubG Chat, Keyboard Guide, Factory Test), built in the docs/APP-SPEC.md format where it fits. Hardware notes per app: Compass reads BMI270/BMM150 from IIO sysfs (find the device by `name`, the index differs between units); IR apps use `/dev/lirc*` (mode2 receive, pulse send, 38 kHz); GPS is NMEA on `/dev/ttyS0` at 115200; LoRa (SX1262), NFC (ST25R3916) and SubG (CC1101) need user-space SPI drivers and share SPI0 and GPIO 22/23/26, and the Cap 5 V rail must never be switched off while the LCD is in use; Camera needs libcamera (`rpicam-*`).
2. Voice Memos: keep the playhead centred from the start (wavesurfer's autoCenter only centres after it scrolls).
3. The virtual-screen window: clarify with the user what still needs to change before touching it.
