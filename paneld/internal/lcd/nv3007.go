// Package lcd drives the BuyDisplay 2.79" 142x428 IPS panel (Novatek NV3007
// controller) over 4-wire SPI via the FT232H.
//
// The controller is always addressed in its native portrait orientation
// (142x428, the exact configuration used by the known-good reference drivers).
// Display rotation is done in software in Blit, so the fragile MADCTL row/column
// swap never has to be trusted - the worst a wrong rotation setting can do is
// show the image turned or mirrored, never garbled.
package lcd

import (
	"fmt"
	"time"

	"github.com/kaievns/sunny-side-up/paneld/internal/board"
	"github.com/kaievns/sunny-side-up/paneld/internal/ftdi"
)

// MIPI DCS command bytes used outside the vendor init block.
const (
	cmdSLPOUT  = 0x11
	cmdINVOFF  = 0x20
	cmdINVON   = 0x21
	cmdDISPON  = 0x29
	cmdCASET   = 0x2A
	cmdRASET   = 0x2B
	cmdRAMWR   = 0x2C
	cmdMADCTL  = 0x36
	madctlBGR  = 0x08 // MADCTL BGR bit
)

// initStep is one entry of a command sequence: a command, its data bytes, and a
// delay to observe afterwards.
type initStep struct {
	cmd   byte
	data  []byte
	delay time.Duration
}

// Options configures the display at Init time.
type Options struct {
	// Rotation is the logical orientation in degrees: 0 or 180 = portrait
	// (142x428), 90 or 270 = landscape (428x142). Default (zero value 0) is
	// portrait; the bring-up command defaults to 90.
	Rotation int
	// Invert sends display-inversion-on (0x21). The tested TFT_eSPI config for
	// this panel enables it; if the image looks like a photo negative, set
	// false. Default true.
	Invert bool
	// BGR sets the MADCTL BGR bit. If red and blue look swapped, toggle this.
	BGR bool
}

// DefaultOptions returns landscape, inverted, RGB - the best first guess.
func DefaultOptions() Options { return Options{Rotation: 90, Invert: true, BGR: false} }

// NV3007 is an initialised display ready to receive frames.
type NV3007 struct {
	dev  *ftdi.Device
	opts Options
}

// New returns a driver bound to the device. Call Init before drawing.
func New(dev *ftdi.Device, opts Options) *NV3007 {
	return &NV3007{dev: dev, opts: opts}
}

// Width and Height report the logical (post-rotation) display size.
func (l *NV3007) Width() int  { w, _ := logicalDims(l.opts.Rotation); return w }
func (l *NV3007) Height() int { _, h := logicalDims(l.opts.Rotation); return h }

func logicalDims(rot int) (w, h int) {
	if rot == 90 || rot == 270 {
		return nativeH, nativeW // 428 x 142
	}
	return nativeW, nativeH // 142 x 428
}

// selectChip drives CS low (active). CS is the only device on the bus, so we
// hold it asserted for the whole session.
func (l *NV3007) selectChip() error { return l.dev.SetLowPin(board.PinCS, false) }

// command sends a command byte (DC low) then optional data bytes (DC high).
func (l *NV3007) command(cmd byte, data ...byte) error {
	if err := l.dev.SetLowPin(board.PinDC, false); err != nil {
		return err
	}
	if err := l.dev.SPIWrite([]byte{cmd}); err != nil {
		return err
	}
	if len(data) > 0 {
		if err := l.dev.SetLowPin(board.PinDC, true); err != nil {
			return err
		}
		if err := l.dev.SPIWrite(data); err != nil {
			return err
		}
	}
	return nil
}

// Reset performs a hardware reset via the RST line (the NV3007 has no software
// reset command).
func (l *NV3007) Reset() error {
	for _, s := range []struct {
		high bool
		wait time.Duration
	}{
		{true, 10 * time.Millisecond},  // ensure released
		{false, 20 * time.Millisecond}, // assert reset (active low)
		{true, 150 * time.Millisecond}, // release, let the controller boot
	} {
		if err := l.dev.SetLowPin(board.PinRST, s.high); err != nil {
			return err
		}
		time.Sleep(s.wait)
	}
	return nil
}

// Init resets the panel, runs the verified vendor register block, then applies
// orientation, inversion, pixel format, sleep-out and display-on. The backlight
// is left off; call Backlight(true) once a frame is drawn.
func (l *NV3007) Init() error {
	if err := l.selectChip(); err != nil {
		return err
	}
	if err := l.Reset(); err != nil {
		return err
	}
	for i, s := range nv3007VendorInit {
		if err := l.command(s.cmd, s.data...); err != nil {
			return fmt.Errorf("nv3007 vendor init step %d (cmd 0x%02X): %w", i, s.cmd, err)
		}
		if s.delay > 0 {
			time.Sleep(s.delay)
		}
	}

	// Native orientation (no MADCTL rotation bits); we rotate in software.
	madctl := byte(0x00)
	if l.opts.BGR {
		madctl |= madctlBGR
	}
	if err := l.command(cmdMADCTL, madctl); err != nil {
		return err
	}

	inv := byte(cmdINVOFF)
	if l.opts.Invert {
		inv = cmdINVON
	}
	if err := l.command(inv); err != nil {
		return err
	}

	if err := l.command(cmdSLPOUT); err != nil {
		return err
	}
	time.Sleep(slpOutDelay)
	if err := l.command(cmdDISPON); err != nil {
		return err
	}
	time.Sleep(dispOnDelay)
	return nil
}

// Backlight switches the LCD backlight (active high, ADBUS4).
func (l *NV3007) Backlight(on bool) error {
	return l.dev.SetLowPin(board.PinBL, on)
}

// setAddrWindowNative sets the RAM write window over the full native panel
// (with the controller column/row offset) and issues RAMWR, leaving DC high so
// the following bytes are pixel data.
func (l *NV3007) setAddrWindowNative() error {
	x0 := colOff
	x1 := colOff + nativeW - 1
	y0 := rowOff
	y1 := rowOff + nativeH - 1
	if err := l.command(cmdCASET, byte(x0>>8), byte(x0), byte(x1>>8), byte(x1)); err != nil {
		return err
	}
	if err := l.command(cmdRASET, byte(y0>>8), byte(y0), byte(y1>>8), byte(y1)); err != nil {
		return err
	}
	if err := l.dev.SetLowPin(board.PinDC, false); err != nil {
		return err
	}
	if err := l.dev.SPIWrite([]byte{cmdRAMWR}); err != nil {
		return err
	}
	return l.dev.SetLowPin(board.PinDC, true)
}

// Blit pushes a full framebuffer to the panel, rotating it in software to the
// native orientation. The framebuffer must match the logical display size.
func (l *NV3007) Blit(fb *Framebuffer) error {
	lw, lh := logicalDims(l.opts.Rotation)
	if fb.W != lw || fb.H != lh {
		return fmt.Errorf("nv3007: framebuffer %dx%d does not match display %dx%d (rotation %d)",
			fb.W, fb.H, lw, lh, l.opts.Rotation)
	}

	// Build the pixel stream in native scan order: for each native row (ny,
	// 0..nativeH-1) emit native columns (nx, 0..nativeW-1), mapping each native
	// pixel back to a logical pixel per the rotation.
	out := make([]byte, 0, nativeW*nativeH*2)
	for ny := 0; ny < nativeH; ny++ {
		for nx := 0; nx < nativeW; nx++ {
			lx, ly := rotateNativeToLogical(l.opts.Rotation, nx, ny)
			i := (ly*fb.W + lx) * 4
			r := fb.Pix[i+0]
			g := fb.Pix[i+1]
			b := fb.Pix[i+2]
			v := (uint16(r&0xF8) << 8) | (uint16(g&0xFC) << 3) | (uint16(b) >> 3)
			out = append(out, byte(v>>8), byte(v&0xFF))
		}
	}

	if err := l.setAddrWindowNative(); err != nil {
		return err
	}
	return l.dev.SPIWrite(out)
}

// rotateNativeToLogical maps a native pixel (nx in 0..nativeW-1, ny in
// 0..nativeH-1) to the logical framebuffer pixel for the given rotation.
func rotateNativeToLogical(rot, nx, ny int) (lx, ly int) {
	switch rot {
	case 90:
		return ny, nativeW - 1 - nx
	case 180:
		return nativeW - 1 - nx, nativeH - 1 - ny
	case 270:
		return nativeH - 1 - ny, nx
	default: // 0
		return nx, ny
	}
}
