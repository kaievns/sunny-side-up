// Package tiny speaks the lcd-panel v2 SPI protocol to the on-board ATtiny414
// fan/backlight controller (firmware/tinyfan). Framing per
// docs/v2-software-design.md: fixed 4-byte frames [cmd][arg0][arg1][crc8] in
// both directions, CRC-8 Dallas/Maxim, offset-by-one responses (the slave
// shifts out its answer to frame N while the master clocks frame N+1).
//
// Electrical: the ATtiny shares the LCD's SCK/MOSI, with its own select on
// ACBUS0 (/CS_TINY, active low) and MISO on ADBUS2 (the MPSSE DI pin). The
// tinyAVR slave needs a slow clock (SCK <= CLK_PER/4), so every transaction
// drops the MPSSE clock to ~1 MHz and restores the LCD clock afterwards.
package tiny

import (
	"fmt"

	"github.com/kaievns/sunny-side-up/paneld/internal/ftdi"
)

// Protocol command bytes (mirror firmware/tinyfan/main.c).
const (
	cmdPing     = 0x01
	cmdFanDuty  = 0x10
	cmdBL       = 0x11
	cmdFanKick  = 0x12
	cmdStatus   = 0x20
	cmdFailsafe = 0x30
	respFlag    = 0x80
	respErr     = 0xFF
)

// Flags is the firmware status byte.
type Flags uint8

const (
	FlagKicking        Flags = 0x01
	FlagStallFault     Flags = 0x02
	FlagFailsafeActive Flags = 0x04
	FlagHostStale      Flags = 0x08
)

func (f Flags) String() string {
	s := ""
	add := func(on Flags, name string) {
		if f&on != 0 {
			if s != "" {
				s += "|"
			}
			s += name
		}
	}
	add(FlagKicking, "KICKING")
	add(FlagStallFault, "STALL_FAULT")
	add(FlagFailsafeActive, "FAILSAFE")
	add(FlagHostStale, "HOST_STALE")
	if s == "" {
		s = "ok"
	}
	return s
}

// csTinyBit is /CS_TINY's ACBUS bit (ACBUS0).
const csTinyBit = 0

// defaultBusHz is the SPI clock for tiny transactions. The tinyAVR slave's
// SCK synchronizer hard-limits SCK to CLK_PER/4 — 2.5 MHz at the 10 MHz
// core, but only 833 kHz at the 3.33 MHz reset default. Found on hardware
// (2026-07-18): clocking past the limit aliases the RX bits (left-shift
// corruption) and every frame CRC-fails. 250 kHz is legal at ANY core clock
// with 3x margin, and throughput is irrelevant (4-byte frames, USB-latency
// dominated).
const defaultBusHz = 250_000

// retries is how many times a transaction is re-attempted on a bad-CRC or
// mismatched response before giving up.
const retries = 3

// Client drives the ATtiny over an already-open FT232H. Not safe for
// concurrent use with other device traffic mid-transaction; callers serialize
// (paneld's single loop / tinycheck's single thread already do).
type Client struct {
	dev   *ftdi.Device
	lcdHz int // clock to restore after a tiny transaction
	busHz int // SPI clock during tiny transactions
}

// New wraps dev. lcdClockHz is the clock restored after each transaction
// (pass the LCD's SPI clock, e.g. 15_000_000).
func New(dev *ftdi.Device, lcdClockHz int) *Client {
	return &Client{dev: dev, lcdHz: lcdClockHz, busHz: defaultBusHz}
}

// SetBusHz overrides the SPI clock used for tiny transactions (bring-up knob).
func (c *Client) SetBusHz(hz int) {
	if hz > 0 {
		c.busHz = hz
	}
}

// crc8 is CRC-8 Dallas/Maxim (poly 0x31 reflected = 0x8C), identical to the
// firmware's implementation.
func crc8(d []byte) byte {
	var crc byte
	for _, b := range d {
		for i := 0; i < 8; i++ {
			mix := (crc ^ b) & 1
			crc >>= 1
			if mix != 0 {
				crc ^= 0x8C
			}
			b >>= 1
		}
	}
	return crc
}

func frame(cmd, a0, a1 byte) [4]byte {
	f := [4]byte{cmd, a0, a1, 0}
	f[3] = crc8(f[:3])
	return f
}

// Frame builds a protocol frame with its CRC — exported for bench tooling
// (tinycheck -debug) so raw streams always carry a valid checksum.
func Frame(cmd, a0, a1 byte) [4]byte { return frame(cmd, a0, a1) }

// xferFrame clocks one 4-byte frame while /CS_TINY is asserted and returns
// the 4 bytes the slave shifted back (= its response to the PREVIOUS frame).
func (c *Client) xferFrame(f [4]byte) ([4]byte, error) {
	var in [4]byte
	if _, err := c.dev.SetClockHz(c.busHz); err != nil {
		return in, err
	}
	defer c.dev.SetClockHz(c.lcdHz)

	if err := c.dev.SetHighPin(csTinyBit, false); err != nil {
		return in, err
	}
	defer c.dev.SetHighPin(csTinyBit, true)

	for i := 0; i < 4; i++ {
		b, err := c.dev.SPIExchangeByte(f[i])
		if err != nil {
			return in, err
		}
		in[i] = b
	}
	return in, nil
}

// resyncSlave pulses /CS_TINY with no clocks: the firmware's /SS rising-edge
// ISR resets its frame index and flushes its SPI TX path.
func (c *Client) resyncSlave() {
	c.dev.SetHighPin(csTinyBit, false)
	c.dev.SetHighPin(csTinyBit, true)
}

// transact sends the command frame, then clocks a PING frame to shift out the
// command's response (offset-by-one protocol). Returns the response frame
// payload after validating CRC and the echoed command byte. Retries with a
// slave resync on failure.
func (c *Client) transact(cmd, a0, a1 byte) (d0, d1 byte, err error) {
	want := cmd | respFlag
	req := frame(cmd, a0, a1)
	ping := frame(cmdPing, 0, 0)

	for attempt := 0; attempt < retries; attempt++ {
		if _, err = c.xferFrame(req); err != nil {
			return 0, 0, err // transport errors aren't protocol desync; bail
		}
		var resp [4]byte
		if resp, err = c.xferFrame(ping); err != nil {
			return 0, 0, err
		}
		if crc8(resp[:3]) == resp[3] && resp[0] == want {
			return resp[1], resp[2], nil
		}
		err = fmt.Errorf("tiny: bad response %x to cmd %#02x (attempt %d)", resp, cmd, attempt+1)
		c.resyncSlave()
	}
	return 0, 0, err
}

// Ping returns the firmware version and status flags.
func (c *Client) Ping() (version byte, flags Flags, err error) {
	d0, d1, err := c.transact(cmdPing, 0, 0)
	return d0, Flags(d1), err
}

// SetFanDuty commands a fan duty (0-255; firmware soft-starts and clamps).
func (c *Client) SetFanDuty(duty byte) error {
	_, _, err := c.transact(cmdFanDuty, duty, 0)
	return err
}

// SetBL sets backlight brightness (0-255).
func (c *Client) SetBL(b byte) error {
	_, _, err := c.transact(cmdBL, b, 0)
	return err
}

// Kick forces a fan kick cycle.
func (c *Client) Kick() error {
	_, _, err := c.transact(cmdFanKick, 0, 0)
	return err
}

// Status returns the tach RPM and status flags.
func (c *Client) Status() (rpm uint16, flags Flags, err error) {
	// STATUS's response carries rpm16; flags ride on the next PING instead,
	// so issue STATUS then read flags from a Ping round.
	hi, lo, err := c.transact(cmdStatus, 0, 0)
	if err != nil {
		return 0, 0, err
	}
	_, flags, err = c.Ping()
	return uint16(hi)<<8 | uint16(lo), flags, err
}

// SetFailsafe configures the watchdog failsafe (fan duty on host silence, and
// the silence timeout in seconds).
func (c *Client) SetFailsafe(duty, timeoutS byte) error {
	_, _, err := c.transact(cmdFailsafe, duty, timeoutS)
	return err
}
