// Package panel wires the FT232H, fan, and NV3007 LCD into a single ready-to-use
// unit and supervises the USB link, reconnecting when it drops (the board's
// connector has proven flaky) instead of letting the process die.
package panel

import (
	"context"
	"log"
	"time"

	"github.com/kaievns/sunny-side-up/paneld/internal/board"
	"github.com/kaievns/sunny-side-up/paneld/internal/fan"
	"github.com/kaievns/sunny-side-up/paneld/internal/ftdi"
	"github.com/kaievns/sunny-side-up/paneld/internal/lcd"
)

// Config holds everything needed to bring the panel up.
type Config struct {
	ClockHz int
	VID     int
	PID     int
	LCD     lcd.Options
}

// Panel is an initialised, drawable panel: MPSSE enabled, LCD reset+init'd, fan
// off. The backlight starts off - turn it on after the first frame.
type Panel struct {
	dev     *ftdi.Device
	Display *lcd.NV3007
	Fan     *fan.Fan
}

// Open connects to the board and initialises everything.
func Open(cfg Config) (*Panel, error) {
	clock := cfg.ClockHz
	if clock == 0 {
		clock = 15_000_000
	}
	dev, err := ftdi.Open(cfg.VID, cfg.PID)
	if err != nil {
		return nil, err
	}
	if err := dev.EnableMPSSE(clock, board.LowIdle, board.LowDirMask); err != nil {
		dev.Close()
		return nil, err
	}
	f := fan.New(dev)
	if err := f.SetOn(false); err != nil {
		dev.Close()
		return nil, err
	}
	display := lcd.New(dev, cfg.LCD)
	if err := display.Init(); err != nil {
		dev.Close()
		return nil, err
	}
	return &Panel{dev: dev, Display: display, Fan: f}, nil
}

// Device exposes the underlying FT232H for extras that share the transport
// (the v2 boards' ATtiny SPI client). All device access is serialized by the
// ftdi layer's own mutex.
func (p *Panel) Device() *ftdi.Device { return p.dev }

// NewFramebuffer returns a framebuffer matching the display size.
func (p *Panel) NewFramebuffer() *lcd.Framebuffer {
	return lcd.NewFramebuffer(p.Display.Width(), p.Display.Height())
}

// Blit pushes a frame.
func (p *Panel) Blit(fb *lcd.Framebuffer) error { return p.Display.Blit(fb) }

// Backlight switches the backlight.
func (p *Panel) Backlight(on bool) error { return p.Display.Backlight(on) }

// Close turns the fan and backlight off and releases the device (best effort).
func (p *Panel) Close() {
	_ = p.Fan.SetOn(false)
	_ = p.Display.Backlight(false)
	_ = p.dev.Close()
}

// Supervise opens the panel and runs render(ctx, p). If Open fails or render
// returns a non-nil error (a device failure), it tears down and retries with
// backoff until ctx is cancelled. render should return nil only to stop for good
// (typically when it observes ctx.Done); any other return is treated as a
// dropped link and triggers a reconnect - so the render loop can just return its
// Blit error and let the supervisor recover.
func Supervise(ctx context.Context, cfg Config, render func(ctx context.Context, p *Panel) error) error {
	const minWait, maxWait = 500 * time.Millisecond, 5 * time.Second
	wait := minWait
	firstFail := true
	for {
		if ctx.Err() != nil {
			return nil
		}
		p, err := Open(cfg)
		if err != nil {
			if firstFail {
				log.Printf("panel: %v", err)
				log.Printf("panel: waiting for the board (will keep retrying; plug it in / reseat)...")
				firstFail = false
			}
			if !sleepCtx(ctx, wait) {
				return nil
			}
			wait = min(wait*2, maxWait)
			continue
		}
		wait, firstFail = minWait, true
		log.Printf("panel: connected")

		err = render(ctx, p)
		p.Close()
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			return nil
		}
		log.Printf("panel: link lost (%v); reconnecting...", err)
		if !sleepCtx(ctx, wait) {
			return nil
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
