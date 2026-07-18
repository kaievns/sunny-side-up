package ftdi

import (
	"fmt"
	"time"
)

// This file adds what the v2 board's ATtiny SPI link needs on top of the LCD
// stack: driving the ACBUS high byte (/CS_TINY lives on ACBUS0), switching the
// SPI clock at runtime (the LCD runs at 15 MHz, the tinyAVR slave tops out at
// CLK_PER/4 so tiny transactions run at ~1 MHz), and a full-duplex byte
// exchange that reads MISO (ADBUS2, the MPSSE DI pin) while clocking out.

// cmdClockInOutMSBNeg clocks bytes out on MOSI (data changes on the -ve edge)
// while sampling MISO on the +ve edge - full-duplex SPI mode 0, MSB first.
const cmdClockInOutMSBNeg = 0x31

// cmdReadLowByte reads the ADBUS pins -> 1 byte (input bits read the pin).
const cmdReadLowByte = 0x81

// ReadLowByte samples the ADBUS pins (inputs read the wire: bit 2 = MISO).
func (d *Device) ReadLowByte() (byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.write([]byte{cmdReadLowByte, cmdSendImmediate}); err != nil {
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
	return 0, fmt.Errorf("ftdi: timed out reading ADBUS low byte")
}

// ConfigureHighByte sets the ACBUS pins' idle value and directions and seeds
// the shadow. Call once after EnableMPSSE (e.g. idle 0x01/dir 0x01 to hold
// /CS_TINY on ACBUS0 deselected-high as an output).
func (d *Device) ConfigureHighByte(idle, dir byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.write([]byte{cmdSetHighByte, idle, dir}); err != nil {
		return err
	}
	d.highShadow = idle
	d.highDir = dir
	return nil
}

// SetHighPin drives a single ACBUS pin high or low, preserving the rest of the
// high byte via the shadow (same contract as SetLowPin for ADBUS).
func (d *Device) SetHighPin(bit uint, high bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	next := d.highShadow
	if high {
		next |= 1 << bit
	} else {
		next &^= 1 << bit
	}
	if next == d.highShadow {
		return nil
	}
	if err := d.write([]byte{cmdSetHighByte, next, d.highDir}); err != nil {
		return err
	}
	d.highShadow = next
	return nil
}

// SetClockHz reprograms the MPSSE clock divisor on the fly and returns the
// actual clock achieved. Used to drop to ~1 MHz for ATtiny transactions and
// restore 15 MHz for the LCD afterwards.
func (d *Device) SetClockHz(hz int) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	divisor := clockDivisor(hz)
	if err := d.write([]byte{cmdSetClkDivisor, byte(divisor & 0xFF), byte((divisor >> 8) & 0xFF)}); err != nil {
		return 0, err
	}
	d.clockHz = mpsseMasterClockHz / (2 * (divisor + 1))
	return d.clockHz, nil
}

// SPIExchangeByte clocks one byte out on MOSI while sampling one byte from
// MISO. Each call is its own USB round-trip on purpose: the inter-byte gap it
// creates (>=125us) is what gives the ATtiny's unbuffered slave ISR time to
// stage the next response byte - do NOT "optimise" this into one burst without
// switching the firmware to buffered SPI.
func (d *Device) SPIExchangeByte(out byte) (byte, error) {
	return d.SPIExchangeByteOp(cmdClockInOutMSBNeg, out)
}

// SPIExchangeByteOp is SPIExchangeByte with an explicit MPSSE byte-exchange
// opcode, selecting the out/in clock edges (0x31 out-neg/in-pos = classic
// mode 0 ... 0x34 out-pos/in-neg = mode-1-style timing). Exists because the
// ATtiny slave's effective sample edge was found empirically on hardware.
func (d *Device) SPIExchangeByteOp(op, out byte) (byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// length field is (N-1) over two bytes; 0,0 = one byte
	if err := d.write([]byte{op, 0, 0, out, cmdSendImmediate}); err != nil {
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
	return 0, fmt.Errorf("ftdi: timed out reading SPI exchange byte")
}
