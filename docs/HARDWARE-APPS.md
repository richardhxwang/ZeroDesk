# Hardware apps

ZeroDesk ships native windows for the official CardputerZero hardware apps. Each one is a ZeroDesk app ([APP-SPEC.md](APP-SPEC.md)) with its web files in `hwapps/<id>/` and a backend in `agent/hw_*.go` that runs as `zerodesk hw <id>`. The id is the launcher id of the original app, so the native window replaces that app's virtual screen.

Every backend runs as the device user in its own transient user unit, with hardware access from group membership (`spi`, `gpio`, `i2c`, `dialout`, `video`, `input`). None of them needs root, so the Store edition includes all of them. The hardware starts when the first window opens and stops when the last one closes.

| App | id | Hardware | Original |
|---|---|---|---|
| Compass | `compass` | BMI270 + BMM150 over IIO sysfs | CardputerZero/Compass |
| Camera | `camera_app` | IMX219 on CSI, through the installed `rpicam-vid` / `rpicam-still` | CardputerZero/Camera |
| Keyboard Guide | `keyboard-guide` | TCA8418 keyboard, evdev (read-only, never grabbed) + modifier registers | CardputerZero/Keyboard-Guide |
| IR Remote | `ir-remote` | `/dev/lirc0` TX (GPIO 12), `/dev/lirc1` RX (GPIO 13) | CardputerZero/IR-Remote |
| IR Chat | `ir-chat` | same as IR Remote | CardputerZero/IR-Chat |
| LoRa | `cap-lora-1262` | Cap LoRa-1262: SX1262 on `/dev/spidev0.1` + GPIO, PI4IO expander at I2C 0x43 | CardputerZero/Cap-LoRa-1262 |
| GPS | `cap-lora-1262-gps` | Cap LoRa-1262: ATGM336H on `/dev/ttyS0`, 115200 baud | CardputerZero/Cap-LoRa-1262-GPS |
| NFC | `cap-cc1101-nfc` | Cap CC1101: ST25R3916 (read only) | CardputerZero/Cap-CC1101-NFC |
| SubG Chat | `cap-cc1101-subg-chat` | Cap CC1101: CC1101 on `/dev/spidev0.1` + GPIO | CardputerZero/Cap-CC1101-SubG-Chat |

## Working with the original apps

- **Same air formats.** LoRa (868 MHz, SF12, 125 kHz, 4/5, sync word 0x34), SubG Chat (868 MHz 2-FSK, 2.4 kbit/s, sync 0x12AD) and IR Chat use the originals' radio settings and packet framing, so a ZeroDesk window and the LCD app can talk to each other.
- **Same files.** IR Remote keeps buttons in `~/.config/IR-Remote/signals` (`.ir` files), Camera saves to `~/Pictures/DCIM/Camera` and `~/Videos/Camera`, and the chat apps share the originals' nickname files.
- **One user at a time.** Radio, IR, camera and GPS apps are `exclusive`: ZeroDesk does not start the backend while the LCD app runs. The LoRa and CC1101 backends lock `/dev/spidev0.1`, so only one Cap app uses the bus at a time.

## Differences from the originals

- Camera: zoom also applies to photos, and video recording is exposed (the original has the code but no button).
- IR Remote: the learn window waits 15 s instead of 6 s, and the idle gap before the first mark is dropped so NEC captures decode.
- NFC: reads only. There is no write path in the original NFC app, and the RFID app's write, emulation and key-cracking features are not included.
- NFC bus: the original uses `/dev/spidev0.2`, which appears only after the boot overlay `dtoverlay=spi0-spidev2-gpio22-overlay` is added (an administrator step). Without it, ZeroDesk drives the reader through `/dev/spidev0.1` with GPIO 22 as chip select and resets the chip if the LCD used the bus in between.
- LoRa: the radio settings are read-only, as in the original.

## Verified on a device

| App | Verified | Not verified |
|---|---|---|
| Compass | live heading, pitch and roll | – |
| Camera | preview at 15 fps, photo, video, camera-busy state | panning direction (dark room), USB cameras |
| Keyboard Guide | device detection, key tables, stream start and stop | a live key press (no one at the keyboard) |
| IR Remote / IR Chat | transmit (NEC codes, chat frames) | receive (decoding covered by unit tests) |
| LoRa | chip detected, one packet sent, receiving | a conversation with a second device |
| GPS | NMEA stream (ATGM336H), searching state | a position fix (tested indoors) |
| NFC / SubG Chat | "Cap CC1101 not found" state | the chips themselves (no working Cap CC1101 was attached) |

## Developing a hardware app

1. Register the backend from its own file: `func init() { hwApps["<id>"] = hwYourApp }`. `hwHub` in `agent/hw.go` streams server-sent events and starts and stops the hardware with the first and last subscriber.
2. Put the window in `hwapps/<id>/` with a manifest whose backend is `["zerodesk", "hw", "<id>"]`. `hwapps/stage.sh` copies the app and its icons (`ui/public/icons/off-<id>-{light,dark}.webp`) into the build.
3. For a quick loop on the device without redeploying ZeroDesk, build only `agent/hw.go`, your files and a stub `main` that calls `runHW(os.Args[2:])`, install it as `~/.local/share/zerodesk/apps/<id>/zd-hw`, and point the device copy of the manifest at `["zd-hw", "hw", "<id>"]`. Run `systemctl --user stop zerodesk-app-<id>` after replacing it; logs are in `journalctl --user -u zerodesk-app-<id>`.
