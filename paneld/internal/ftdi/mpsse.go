package ftdi

import (
	"fmt"
	"time"
)

// MPSSE command opcodes we use (FT232H, per FTDI AN-108 / AN-135).
const (
	cmdClockOutMSBNeg = 0x11 // clock bytes out, MSB first, data changes on -ve edge (SPI mode 0)
	cmdSetLowByte     = 0x80 // set ADBUS data + direction
	cmdSetHighByte    = 0x82 // set ACBUS data + direction
	cmdReadHighByte   = 0x83 // read ACBUS pins -> 1 byte
	cmdSendImmediate  = 0x87 // flush pending read data back to the host now
	cmdDisableDiv5    = 0x8A // use the full 60MHz master clock (not /5)
	cmdDisableAdaptive = 0x97 // no adaptive clocking
	cmdDisable3Phase  = 0x8D // no three-phase clocking (plain SPI)
	cmdSetClkDivisor  = 0x86 // set clock divisor (value low, value high)
	cmdBadCommand     = 0xAA // deliberately invalid, used to sync/verify MPSSE
)

// mpsseMasterClockHz is the FT232H MPSSE master clock with /5 disabled.
const mpsseMasterClockHz = 60_000_000

// maxSPIChunk is the largest single clock-out command payload. The MPSSE length
// field is (N-1) across two bytes, so N can be up to 65536.
const maxSPIChunk = 65536

// EnableMPSSE configures the MPSSE engine for SPI mode 0 at approximately
// clockHz, and drives the low byte (ADBUS) to lowIdle with directions lowDir.
// It must be called once after Open.
func (d *Device) EnableMPSSE(clockHz int, lowIdle, lowDir byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.syncMPSSE(); err != nil {
		return err
	}

	divisor := clockDivisor(clockHz)
	setup := []byte{
		cmdDisableDiv5,
		cmdDisableAdaptive,
		cmdDisable3Phase,
		cmdSetClkDivisor, byte(divisor & 0xFF), byte((divisor >> 8) & 0xFF),
		// Initial low-byte state and directions.
		cmdSetLowByte, lowIdle, lowDir,
	}
	if err := d.write(setup); err != nil {
		return err
	}
	d.lowShadow = lowIdle
	d.lowDir = lowDir
	return nil
}

// syncMPSSE sends a deliberately bad command and checks the MPSSE echoes the
// 0xFA "bad command" marker, proving the engine is alive and in sync. Caller
// holds d.mu.
func (d *Device) syncMPSSE() error {
	if err := d.flush(); err != nil {
		return err
	}
	if err := d.write([]byte{cmdBadCommand}); err != nil {
		return err
	}
	// Expect 0xFA 0xAA back. Poll briefly (latency timer is 1ms).
	buf := make([]byte, 2)
	deadline := time.Now().Add(200 * time.Millisecond)
	var got []byte
	for time.Now().Before(deadline) {
		n, err := d.read(buf)
		if err != nil {
			return err
		}
		got = append(got, buf[:n]...)
		for i := 0; i+1 < len(got); i++ {
			if got[i] == 0xFA && got[i+1] == cmdBadCommand {
				return nil
			}
		}
		if n == 0 {
			time.Sleep(1 * time.Millisecond)
		}
	}
	return fmt.Errorf("ftdi: MPSSE sync failed (chip not responding to MPSSE; got %x) - is it really an FT232H in MPSSE mode?", got)
}

// clockDivisor returns the 16-bit divisor for the requested SPI clock:
// SCLK = 60MHz / ((1 + divisor) * 2).
func clockDivisor(clockHz int) int {
	if clockHz <= 0 {
		clockHz = 1
	}
	div := mpsseMasterClockHz/(2*clockHz) - 1
	if div < 0 {
		div = 0
	}
	if div > 0xFFFF {
		div = 0xFFFF
	}
	return div
}

// SetLowPin drives a single ADBUS pin high or low while preserving every other
// bit of the low byte - crucially including the fan-gate bit, which shares this
// port with the LCD control lines.
func (d *Device) SetLowPin(bit uint, high bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.setLowPinLocked(bit, high)
}

func (d *Device) setLowPinLocked(bit uint, high bool) error {
	next := d.lowShadow
	if high {
		next |= 1 << bit
	} else {
		next &^= 1 << bit
	}
	if next == d.lowShadow {
		return nil
	}
	if err := d.write([]byte{cmdSetLowByte, next, d.lowDir}); err != nil {
		return err
	}
	d.lowShadow = next
	return nil
}

// LowByte returns the current shadow value of the ADBUS low byte.
func (d *Device) LowByte() byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lowShadow
}

// SPIWrite clocks bytes out on MOSI (ADBUS1) with SCK (ADBUS0), MSB first, on
// the falling edge (SPI mode 0). All other low-byte pins keep their current
// state throughout, so CS/DC/RST/BL/fan-gate are untouched.
func (d *Device) SPIWrite(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.spiWriteLocked(data)
}

func (d *Device) spiWriteLocked(data []byte) error {
	for len(data) > 0 {
		chunk := data
		if len(chunk) > maxSPIChunk {
			chunk = chunk[:maxSPIChunk]
		}
		n := len(chunk) - 1 // MPSSE length is (N-1)
		hdr := []byte{cmdClockOutMSBNeg, byte(n & 0xFF), byte((n >> 8) & 0xFF)}
		if err := d.write(append(hdr, chunk...)); err != nil {
			return err
		}
		data = data[len(chunk):]
	}
	return nil
}

// ReadHighByte samples all ACBUS pins.
func (d *Device) ReadHighByte() (byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.readHighByteLocked()
}

func (d *Device) readHighByteLocked() (byte, error) {
	if err := d.write([]byte{cmdReadHighByte, cmdSendImmediate}); err != nil {
		return 0, err
	}
	buf := make([]byte, 1)
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		n, err := d.read(buf)
		if err != nil {
			return 0, err
		}
		if n == 1 {
			return buf[0], nil
		}
		time.Sleep(200 * time.Microsecond)
	}
	return 0, fmt.Errorf("ftdi: timed out reading ACBUS high byte")
}

// ReadHighPin returns the level of a single ACBUS pin.
func (d *Device) ReadHighPin(bit uint) (bool, error) {
	v, err := d.ReadHighByte()
	if err != nil {
		return false, err
	}
	return v&(1<<bit) != 0, nil
}
