# tinyfan — ATtiny414 fan + backlight firmware

Autonomous PWM controller for the lcd-panel v2 board. The ATtiny is a **dumb
executor with a failsafe**; `paneld` owns the fan curve and brightness policy
(see [../../docs/v2-software-design.md](../../docs/v2-software-design.md)).

**Status: bench-proven on real hardware (2026-07-18)** — SPI protocol live
(`tinycheck` PING/BL/FAN/STATUS), tach verified, duty→RPM calibrated.

## What the firmware does

- 10 MHz core (OSC20M ÷2 — factory `OSCCFG=0x02`, no fuse writes ever).
- TCA0 split PWM: **fan** on PB0/WO0 @ 31.25 kHz (high-side buck), **backlight**
  on PA5/WO5 @ 40 kHz. 100 % maps to CMP=PER (a CMP of PER+1 never matches
  and parks the pin — found on hardware).
- SPI slave, 4-byte CRC-8 frames (`PING`, `FAN_DUTY`, `BL`, `STATUS`,
  `FAILSAFE_CFG`, `FAN_KICK` stub), offset-by-one responses, `/SS`-rising
  resync with a level-check glitch filter.
- **CRC-8 is table-driven in flash and must stay that way**: the unbuffered
  SPI transmitter reloads from DATA every byte, so the ISR has to finish
  inside the inter-byte gap. Bit-banged CRC inline (~130 µs) silently
  corrupted every response while reception stayed perfect — the nastiest bug
  of the bring-up. Keep the SPI ISR under ~20 µs.
- Tach edge counting on PB1 → RPM over a 1 s RTC/PIT window.
- Fan **soft-start** (~1 step/ms — protects L1 from saturation) and a
  **MIN_DUTY=26 clamp**: bench sweep showed a hard stall cliff at duty 16-20
  (the buck's light-load DCM lift compresses the range to duty 20..255 →
  3000..4440 rpm); nonzero commands below 26 are raised to it.
- From-reset **failsafe**: 60 s without a valid frame → fan 70 %, BL capped
  at 30 % (never raised from off).
- `BRINGUP_HELLO` (default 1): two backlight swells at boot as a liveness
  cue. Set to 0 for production. `SPI_PROBE=1` builds a diagnostic image
  where BL brightness traces ISR activity.

Not yet (M2+): full kickstart-and-hold, stall latch, tach input capture.

## Build & flash

Toolchain: `avr-gcc` with tinyAVR-1 support. macOS:
`brew tap osx-cross/avr && brew trust osx-cross/avr && brew install avr-gcc@14`
(keg-only — put `/opt/homebrew/opt/avr-gcc@14/bin` and
`/opt/homebrew/opt/avr-binutils/bin` on PATH). `avrdude` ≥ 7.3 via brew.

```sh
make                 # -> tinyfan.hex
make sig             # UPDI link test (reads 0x1e 0x92 0x22), no firmware
make flash           # SerialUPDI flash via the UPDI Friend
make PORT=/dev/cu.usbserial-XXXX flash
```

Programming rig: Adafruit UPDI Friend (switch on **3V**) → board TC2030 pads
— **as fabbed: pad 1 = 3V3, pad 3 = UPDI, pad 5 = GND** — Friend V+
**unconnected** (the board self-powers over its own USB, plugged **directly
into the Mac** — the board does not enumerate through hubs). Flashing works
regardless since UPDI is independent of the FT232H.

## Bring-up diagnostics

Three keepers, one per debug layer (build `make <name>.hex`, flash
`make flash-<name>`, then reflash `tinyfan.hex` when done):

| | proves | how to read it |
|---|---|---|
| `blink.c` | the chip executes at all | raw GPIO, no peripherals: BL blinks 2 Hz, then fan full-spin 4 s, repeat |
| `bringup.c` | PWM outputs + power stages | TCA0: BL breathes twice, then fan soft-starts to 100 % |
| `spiecho.c` | the SPI wire, bit-exact | echoes every received byte; run `tinycheck -debug` — reads = previous sent byte; corruption shape names the fault |

The 2026-07-18 bring-up used a longer ladder of one-off probes (ISR-echo,
frame-engine bisections, /SS monitors) — deleted after the hunt; their
findings live in the doc's bring-up notes and the comments in `main.c`.

## Bench tools (host side)

`paneld/cmd/tinycheck` (PING/BL/fan/sweep/watch/-debug) and `paneld/cmd/white`
(paint the panel so the BL is visible) — run with `sudo` on macOS.
