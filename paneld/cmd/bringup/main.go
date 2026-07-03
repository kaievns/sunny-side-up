// Command bringup is the minimal hardware smoke test for the lcd-panel board.
//
// It opens the FT232H, turns on the LCD (backlight + a test screen), turns on
// the fan, and reads the tachometer - proving the whole signal chain works
// before any real firmware is written. Run it with the board plugged into USB:
//
//	go run ./cmd/bringup            # everything
//	go run ./cmd/bringup -fan=false # LCD only (e.g. a board with no fan wired)
//
// It prints what it does to the console and mirrors the key facts onto the LCD,
// then refreshes once a second until Ctrl-C, restoring the fan/backlight off.
package main

import (
	"flag"
	"fmt"
	"image/color"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kaievns/sunny-side-up/paneld/internal/board"
	"github.com/kaievns/sunny-side-up/paneld/internal/fan"
	"github.com/kaievns/sunny-side-up/paneld/internal/ftdi"
	"github.com/kaievns/sunny-side-up/paneld/internal/lcd"
)

func main() {
	clockHz := flag.Int("clock", 15_000_000, "SPI clock in Hz")
	vid := flag.Int("vid", 0, "USB vendor id (0 = FT232H default 0x0403)")
	pid := flag.Int("pid", 0, "USB product id (0 = FT232H default 0x6014)")
	driveFan := flag.Bool("fan", true, "drive the fan on and measure its tachometer")
	tachWin := flag.Duration("tach-window", time.Second, "tach measurement window")
	rotate := flag.Int("rotate", 270, "display rotation: 0/180 portrait (142x428), 90/270 landscape (428x142)")
	invert := flag.Bool("invert", true, "enable display inversion (set false if the image is a photo negative)")
	bgr := flag.Bool("bgr", false, "use BGR color order (set true if red and blue are swapped)")
	flag.Parse()

	opts := lcd.Options{Rotation: *rotate, Invert: *invert, BGR: *bgr}
	if err := run(*clockHz, *vid, *pid, *driveFan, *tachWin, opts); err != nil {
		log.Fatalf("bringup: %v", err)
	}
}

func run(clockHz, vid, pid int, driveFan bool, tachWin time.Duration, opts lcd.Options) error {
	log.Printf("opening FT232H (vid=0x%04x pid=0x%04x)...", orDefault(vid, ftdi.DefaultVID), orDefault(pid, ftdi.DefaultPID))
	dev, err := ftdi.Open(vid, pid)
	if err != nil {
		return err
	}
	defer dev.Close()

	log.Printf("enabling MPSSE, SPI clock ~%d Hz", clockHz)
	if err := dev.EnableMPSSE(clockHz, board.LowIdle, board.LowDirMask); err != nil {
		return err
	}

	f := fan.New(dev)
	// Make sure the fan starts from a known-off state.
	if err := f.SetOn(false); err != nil {
		return err
	}

	log.Printf("initialising NV3007 LCD (rotation=%d invert=%v bgr=%v)...", opts.Rotation, opts.Invert, opts.BGR)
	display := lcd.New(dev, opts)
	if err := display.Init(); err != nil {
		return err
	}
	log.Printf("LCD ready: %dx%d", display.Width(), display.Height())

	// Clean shutdown on Ctrl-C.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer func() {
		_ = f.SetOn(false)
		_ = display.Backlight(false)
	}()

	fb := lcd.NewFramebuffer(display.Width(), display.Height())

	// Draw an initial frame and switch the backlight on.
	drawScreen(fb, clockHz, 0, fan.Tach{}, driveFan)
	if err := display.Blit(fb); err != nil {
		return err
	}
	if err := display.Backlight(true); err != nil {
		return err
	}
	log.Printf("backlight on, test screen drawn")

	start := time.Now()
	var lastTach fan.Tach
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		if driveFan {
			t, err := f.MeasureTach(tachWin)
			if err != nil {
				return err
			}
			lastTach = t
			if t.SawToggle {
				log.Printf("tach: ~%d RPM (%d pulses / %d samples over %s)",
					t.RPM, t.RisingEdges, t.Samples, t.Window.Round(time.Millisecond))
			} else {
				log.Printf("tach: no signal (line steady; %d samples) - fan not spinning or not connected",
					t.Samples)
			}
		}

		elapsed := int(time.Since(start).Seconds())
		drawScreen(fb, clockHz, elapsed, lastTach, driveFan)
		if err := display.Blit(fb); err != nil {
			return err
		}

		select {
		case <-sig:
			log.Printf("shutting down: fan off, backlight off")
			return nil
		case <-ticker.C:
		}
	}
}

// drawScreen renders the bring-up test screen: color bars to check color and
// orientation, plus live status text.
func drawScreen(fb *lcd.Framebuffer, clockHz, elapsed int, t fan.Tach, driveFan bool) {
	fb.Fill(lcd.Black)

	// Color bars down the right edge to verify RGB order and that the origin is
	// where we think it is.
	barW := 24
	bars := []color.RGBA{lcd.Red, lcd.Green, lcd.Blue, lcd.White}
	for i, c := range bars {
		fb.FillRect(fb.W-barW, i*fb.H/len(bars), barW, fb.H/len(bars), c)
	}
	// A small marker in the top-left so orientation is unambiguous.
	fb.FillRect(0, 0, 6, 6, lcd.Yellow)

	x := 10
	y := 4
	y = fb.Text(x, y, "sunny-side-up  panel bring-up", lcd.Cyan)
	y = fb.Text(x, y, fmt.Sprintf("FT232H MPSSE OK   %dx%d  @ %d MHz", fb.W, fb.H, clockHz/1_000_000), lcd.White)

	if driveFan {
		if t.SawToggle {
			y = fb.Text(x, y, fmt.Sprintf("fan: ON   tach ~%d RPM (%d p)", t.RPM, t.RisingEdges), lcd.Green)
		} else {
			y = fb.Text(x, y, "fan: ON   tach: no signal", lcd.Orange)
		}
	} else {
		y = fb.Text(x, y, "fan: disabled (-fan=false)", lcd.Orange)
	}
	fb.Text(x, y, fmt.Sprintf("uptime: %ds  %s", elapsed, spinner(elapsed)), lcd.White)
}

func spinner(n int) string {
	return string([]byte{"|/-\\"[n%4]})
}

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}
