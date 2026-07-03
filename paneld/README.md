# paneld

Software for the **sunny-side-up** LCD + fan panel — the extension PCB in
[`../lcd-panel`](../lcd-panel) that plugs into the NanoPi R5C router's USB-A port
and adds a small IPS LCD and a proper 3-wire fan controller.

This is written in Go and runs **on the host** (the router, or your dev machine
for bench testing) — the board has no MCU of its own. All the intelligence lives
here; the board is a USB→SPI/GPIO bridge.

## What's on the board (and how the software talks to it)

The USB-A port carries USB 2.0 + 5V. On the board that lands on an **FTDI
FT232H** (U1), which the software drives in **MPSSE mode** to get SPI + GPIO:

| Signal    | FT232H pin | MPSSE bit    | Notes                                    |
|-----------|------------|--------------|------------------------------------------|
| LCD SCK   | ADBUS0     | low b0       | SPI clock                                |
| LCD MOSI  | ADBUS1     | low b1       | SPI data out                             |
| (MISO)    | ADBUS2     | low b2       | unused — the LCD is write-only           |
| LCD CS    | ADBUS3     | low b3       | chip select, active low                  |
| LCD BL    | ADBUS4     | low b4       | backlight, active high                   |
| LCD RST   | ADBUS5     | low b5       | reset, active low                        |
| LCD DC    | ADBUS6     | low b6       | data(1)/command(0)                       |
| **FAN gate** | ADBUS7  | low b7       | AO3400A MOSFET, low-side, 5V fan         |
| **FAN tach** | ACBUS1  | high b1      | 10k pull-up to 3V3                        |

Two consequences the code is built around:

1. **The fan gate shares the low byte with every LCD control line.** Any write
   to the low byte must preserve the other bits, so `ftdi.Device` keeps a shadow
   register and only ever flips the one bit you ask for. SPI byte-clocking
   (MPSSE `0x11`) leaves the other low-byte latches untouched, so a full LCD
   frame never disturbs the fan.
2. **The fan is low-side switched**, so the tach line is only valid while the
   gate is on. `MeasureTach` turns the fan on before counting.

The LCD is a **BuyDisplay 2.79" 142×428 IPS** with a **Novatek NV3007**
controller (4-wire SPI). The controller is always addressed in its native
142×428 orientation (the exact config the known-good reference drivers use, incl.
the 12-pixel column offset); display rotation is done in software in the blit.

## Layout

```
internal/board   pin map for THIS PCB (from the KiCad netlist)
internal/ftdi    FT232H via libftdi (ftdi.go = cgo transport, mpsse.go = protocol)
internal/lcd     NV3007 driver (nv3007.go) + verified init table (nv3007_init.go)
                 + RGB565 framebuffer & text (draw.go)
internal/fan     fan gate on/off + tach RPM
cmd/bringup      the minimal hardware smoke test
```

## Build & run on your dev machine (macOS/Linux)

libftdi is linked via cgo, so you need it plus pkg-config:

```sh
brew install libftdi pkg-config        # macOS
# sudo apt install libftdi1-dev pkg-config   # Debian/Ubuntu

make dev                                # -> bin/bringup
make run                                # build + run against a plugged-in board
# or: make run ARGS="-fan=false -rotate 90"
```

### What you should see

With the board plugged in, `bringup`:

- opens the FT232H, enables MPSSE, initialises the NV3007,
- draws a test screen: R/G/B/W colour bars, a yellow top-left corner marker, and
  status text (`sunny-side-up panel bring-up`, resolution, SPI clock),
- turns the backlight on,
- turns the fan on and prints the tach reading once a second, mirroring it onto
  the LCD.

On exit (Ctrl-C) it turns the fan and backlight off.

### Flags

| Flag | Default | Purpose |
|------|---------|---------|
| `-clock` | `15000000` | SPI clock (Hz) |
| `-rotate` | `90` | `0`/`180` portrait (142×428), `90`/`270` landscape (428×142) |
| `-invert` | `true` | display inversion — set `false` if the image is a photo negative |
| `-bgr` | `false` | set `true` if red and blue are swapped |
| `-fan` | `true` | drive the fan and read the tach — set `false` for an LCD-only board |
| `-tach-window` | `1s` | tach measurement window |
| `-vid`/`-pid` | `0` | override USB IDs (0 = FT232H default `0403:6014`) |

The colour bars and corner marker exist to make orientation/colour obvious on
first light. If the picture is rotated or mirrored, pick a different `-rotate`;
if colours are off, flip `-bgr` and/or `-invert`. These only change one register
each — the panel is working either way.

## Troubleshooting first light

- **`device not found`** — board not enumerated. On Linux the kernel may have
  grabbed it as a serial port; libftdi auto-detaches `ftdi_sio`, but if it
  fights you, `modprobe -r ftdi_sio`. On macOS libusb detaches Apple's FTDI
  driver automatically.
- **`MPSSE sync failed`** — the chip enumerated but isn't behaving as an FT232H
  in MPSSE mode. Re-plug; confirm it's `0403:6014`.
- **Backlight on but blank / white** — the init ran but nothing's addressed.
  Check the FPC seating; try a lower `-clock` (e.g. `6000000`).
- **Garbled/negative/miscoloured** — use `-invert`, `-bgr`, `-rotate` as above.
- **`tach: no signal`** — expected with no fan connected. With a fan, it means
  the gate isn't switching or the tach isn't wired; verify on the real board.

## Deploying to the router (OpenWrt, NanoPi R5C / aarch64 / musl)

The binary uses cgo, so a cross build needs a musl toolchain and the target's
libftdi. On the router:

```sh
opkg update && opkg install libftdi1     # pulls libusb-1.0
```

Then cross-compile with **one** of the Makefile targets (see the Makefile for
the required paths):

```sh
# Path A: zig as the cross C compiler (no OpenWrt SDK needed on your Mac)
brew install zig
make router-zig SYSROOT=/path/to/aarch64-sysroot     # sysroot = router's /usr/{include,lib}

# Path B: OpenWrt SDK toolchain (most reliable for OpenWrt)
make router-sdk STAGING_DIR=... TOOLCHAIN=...
```

`scp bin/bringup-arm64` to the router and run it with the board plugged in.

## Roadmap (next, on the real hardware)

- **Fan curve** off the two real temp sources: SoC via
  `/sys/class/thermal/thermal_zone*/temp` (RK3568), Wi-Fi via the MT7916
  (mt7915e/mt76) hwmon `temp1_input`.
- **Fan speed control** — low-frequency PWM of the gate (the SS14 flyback diode
  is there for it), or threshold on/off with hysteresis. Tach is only read while
  the gate is on.
- **Real UI** and a long-running **daemon** (procd service) rendering router
  status instead of the bring-up test screen.
