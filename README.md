# ZeroDesk

**A desktop for your CardputerZero, in your computer's browser.**

ZeroDesk turns the M5Stack CardputerZero into a macOS-style desktop that you open from any computer on the same network. Drag and resize windows, launch apps from the Dock, manage files in Finder, run a Terminal, change settings, play music and the piano, record voice memos, and use the CardputerZero's own screen and apps with your mouse and keyboard. Nothing to install on the computer.

![ZeroDesk: opening Finder and Piano from the Dock and Apps](docs/media/hero.gif)

## Contents

- [Features](#features)
- [What you need](#what-you-need)
- [Install](#install)
- [First connection](#first-connection)
- [Keyboard shortcuts](#keyboard-shortcuts)
- [Troubleshooting and FAQ](#troubleshooting-and-faq)
- [Security and privacy](#security-and-privacy)
- [Update](#update)
- [Uninstall](#uninstall)
- [For developers](#for-developers)
- [Acknowledgements](#acknowledgements)
- [License](#license)

## Features

### Desktop, Dock and appearance

Windows with the familiar traffic lights, resizable from every edge and corner. The Dock magnifies under the pointer and shows which apps are running. Light and dark appearance, or automatic by time of day, in English or Chinese.

![Dock, Settings and switching to dark appearance](docs/media/desktop.gif)

| Light | Dark |
|---|---|
| ![Desktop, light](docs/media/desktop-light.jpg) | ![Desktop, dark](docs/media/desktop-dark.jpg) |

### Apps

Every app installed on the CardputerZero, sorted by category, with search. Type to filter, press Return to open.

![Apps: categories and search](docs/media/launchpad.gif)

| Light | Dark |
|---|---|
| ![Apps, light](docs/media/apps-light.jpg) | ![Apps, dark](docs/media/apps-dark.jpg) |

### Finder

Browse the device's files, upload by dragging files from your computer into a window, download, rename, move to the Trash, preview with Space, and install `.deb` packages with a double-click.

![Finder: opening a folder and previewing a file](docs/media/finder.gif)

### Terminal

A full shell on the device.

![Terminal](docs/media/terminal.gif)

### Music

Plays the songs in the device's `~/Music` folder: a library with search, a player bar with shuffle and repeat, and drag-and-drop to add songs.

![Music](docs/media/music.gif)

### Piano

An 88-key piano with real grand-piano samples. Play with the mouse (slide across the keys for a glissando) or with the keyboard; sustain pedal and octave controls in the toolbar.

![Piano](docs/media/piano.gif)

### Voice Memos

Records with the CardputerZero's microphone. Scrub through the waveform, skip 15 seconds, rename and delete recordings.

![Voice Memos](docs/media/voicememos.gif)

### Settings

Wi-Fi, network, battery, general (restart, developer options), appearance, display brightness and sleep, sound, expansion-port power, and date and time.

![Settings](docs/media/settings.gif)

### The CardputerZero's screen

The **Cardputer** window shows the device's own screen live. Click it and type on your computer's keyboard; scroll with the trackpad or mouse wheel; an on-screen copy of the device keyboard is one click away. Choose **Pixel** for an exact, sharp pixel grid or **Smooth** for an upscaled picture that fills the window.

![The Cardputer window with keyboard input](docs/media/cardputer.gif)

### Linux desktop

The device's graphical Linux desktop in a window.

![Linux desktop](docs/media/linux.gif)

### Other apps

Apps you install on the CardputerZero (from its App Store or as `.deb` files) appear in **Apps**:

- Apps that come with a ZeroDesk window open as normal windows, like the built-in apps (below: Level, an example app).
- Other apps run on your computer in a window that shows their screen; use your keyboard, mouse wheel and, where the app supports it, the mouse. Choose **Open on Zero** to run them on the device instead.

![Level and IR Remote running in windows](docs/media/apps.gif)

## What you need

- A CardputerZero with its standard system (the image with the APPLaunch launcher, Debian 13).
- The CardputerZero and your computer on the same network (Wi-Fi or Ethernet).
- A current browser on the computer: Chrome, Edge, Safari or Firefox.

## Install

ZeroDesk comes in two editions built from the same source, with the same features:

| | Store edition | GitHub edition |
|---|---|---|
| Where | CardputerZero Store | this repository's **Releases** page |
| File | `zerodesk_<version>-<rev>_arm64.deb` | `zerodesk_<version>-<rev>+full_arm64.deb` |
| Features | all | all |

**From the CardputerZero Store** (once ZeroDesk is listed there): open **Store** on the device, search for **ZeroDesk** or its share code **ZDSK**, and install.

**From GitHub:**

1. Download the latest `zerodesk_<version>-<rev>+full_arm64.deb` from this repository's **Releases** page.
2. Copy it to the CardputerZero, for example with `scp`, or on a USB drive.
3. On the CardputerZero (in a terminal, or over SSH), run:

   ```bash
   sudo apt install ./zerodesk_<version>-<rev>+full_arm64.deb
   ```

ZeroDesk starts right away and every time the device starts. It needs about 6 MB of memory while nobody is connected. Its entry in the device's launcher shows whether it is running and the address to open.

## First connection

1. **Open the address.** In the browser on your computer, go to **http://cardputerzero.local**. If that page does not open, use the device's IP address instead (the launcher's **IP Panel** app shows it): `http://<ip-address>`.
2. **Read the code.** The browser shows a four-digit code.

   ![The browser asks to confirm on the device](docs/media/pair-browser.jpg)

3. **Confirm on the device.** The CardputerZero shows the same code. Press **OK** on its keyboard within 60 seconds; any other key declines.

   ![The confirmation on the CardputerZero's screen](docs/media/pair-device.png)

4. **Done.** The desktop opens. This browser stays connected until you log out (menu bar or Settings → General → Log Out).

Can't press a key on the device right now? Choose **Use an access token** and enter the token stored on the device in `~/.config/zerodesk/token` (for example `cat ~/.config/zerodesk/token` over SSH).

![Connecting with an access token](docs/media/pair-token.jpg)

## Keyboard shortcuts

| Where | Keys | Action |
|---|---|---|
| Apps | type | Search |
| Apps | Return / Esc | Open the first result / close |
| Finder | ↑ ↓ | Select |
| Finder | Return | Rename |
| Finder | Space | Quick Look |
| Finder | ⌘⌫ or Delete | Move to Trash |
| Finder | ⌘↑ / ⌘↓ or ⌘O | Enclosing folder / open |
| Music | Space | Play / pause |
| Music | Return | Play the selected song |
| Music | ⌘← / ⌘→ | Previous / next song |
| Music | ⌘F | Search |
| Music | ⌘⌫ | Move the selected song to the Trash |
| Voice Memos | Space | Play / pause |
| Voice Memos | Return / ⌘⌫ | Rename / delete the selected recording |
| Piano | A S D F G H J K L ; ' | White keys (W E T Y U O P: black keys) |
| Piano | Z / X | Octave down / up |
| Piano | Tab | Sustain pedal |
| Cardputer and app windows | any key | Sent to the device (⌘ shortcuts stay with your computer) |
| Cardputer and app windows | scroll | Arrow keys, both directions |
| Menus, sheets | Esc | Close |

## Troubleshooting and FAQ

**`cardputerzero.local` does not open.** Make sure the computer and the CardputerZero are on the same network, then use the IP address instead. Some networks and VPNs block device names; the IP address always works. Turn off a VPN on the computer if the IP address does not work either.

**I pressed OK but nothing happened.** The request lasts 60 seconds. Click **Connect again**, then press OK while the code is on the device's screen.

**The Cardputer window is blurry or too small.** Choose **Pixel** in the window's toolbar for a sharp pixel grid, or **Smooth** to fill the window; make the window larger for a larger picture.

**Typing in the Cardputer window does nothing.** Click the screen first; a ring around it shows that it receives your keys.

**The Linux desktop window keeps showing "Starting".** The device needs its graphical desktop running (the standard image starts it automatically). It can take several seconds on a slow network.

**Piano sounds like a simple synthesizer at first.** The first time, the device downloads the piano samples (about 6 MB); until then a built-in synthesizer plays.

**An app runs on the device instead of in a window.** Some apps need hardware access only the device can give; choose them in Apps and pick **Open on Zero**.

**ZeroDesk asks for a password.** Installing packages, removing apps, setting the clock by hand and turning ADB on or off need administrator rights. Enter the device user's password (the one you use with `sudo`); it is used for that one action and not stored.

**Does ZeroDesk change how the device works on its own?** No. The launcher and its apps keep working as before.

## Security and privacy

- **What is on the network:** ZeroDesk answers HTTP on ports 80 and 8686 of the CardputerZero. Every page and command except the login screen needs a signed-in browser.
- **How signing in works:** a browser gets in only after someone presses OK on the device itself, or by entering the device's access token. The browser then keeps a session cookie that scripts cannot read and that is never sent to other sites. Log out to remove it.
- **Encryption:** the connection is plain HTTP, like most devices on a home network. Use ZeroDesk on networks you trust.
- **Privileges:** ZeroDesk runs as the device's normal user, never as root. Actions that need administrator rights ask for your password (unless the device allows `sudo` without one); the password is used once and not stored.
- **Apps with their own windows** run in an isolated frame: they cannot read your session or call ZeroDesk's functions except through the permissions they declare.
- **Your data stays on the device.** ZeroDesk sends nothing anywhere. The one outside request is the first use of Piano, when the device downloads the piano samples from a public sample library.

## Update

The Store updates ZeroDesk like any other app. For the GitHub edition, download the newer `.deb` and install it the same way. Your settings, connected browsers and app data are kept.

## Uninstall

```bash
sudo apt remove zerodesk
```

To also remove the access token and app data: `rm -rf ~/.config/zerodesk ~/.local/share/zerodesk`.

## For developers

- [docs/APP-SPEC.md](docs/APP-SPEC.md): give a CardputerZero app its own ZeroDesk window (manifest, sandbox, JavaScript SDK), with the example in [examples/level](examples/level).
- [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md): architecture, building, packaging and deploying ZeroDesk.

## Acknowledgements

Inspired by [macOS Web](https://github.com/PuruVJ/macos-web) by PuruVJ. ZeroDesk builds on [Svelte](https://svelte.dev), [neodrag](https://github.com/PuruVJ/neodrag), [wavesurfer.js](https://wavesurfer.xyz), [smplr](https://github.com/danigb/smplr) with the public-domain Splendid Grand Piano samples, [Framework7 Icons](https://framework7.io/icons/), [xterm.js](https://xtermjs.org), [noVNC](https://novnc.com), [WebSR](https://github.com/sb2702/websr), [popmotion](https://popmotion.io), [coder/websocket](https://github.com/coder/websocket), [creack/pty](https://github.com/creack/pty) and Go's x/image and x/text, and is packaged with [nfpm](https://nfpm.goreleaser.com). Their licences and copyright notices are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## License

ZeroDesk is released under the MIT License; see [LICENSE](LICENSE).
