// Package board captures the physical wiring of the lcd-panel PCB: how the
// FT232H's MPSSE pins map to the LCD, the fan MOSFET, and the fan tachometer.
//
// This is derived directly from the KiCad netlist (lcd-panel/production/
// netlist.ipc) decoded against the FT232H LQFP-48 pinout. Everything the
// software does about "which pin is what" lives here, in one place.
//
//	FT232H pin  MPSSE bit          Net         Function
//	----------  -----------------  ----------  ------------------------------
//	ADBUS0      low  bit0  (SCK)   LCD_SCK     SPI clock          (output)
//	ADBUS1      low  bit1  (DO)    LCD_MOSI    SPI data out       (output)
//	ADBUS2      low  bit2  (DI)    -           unused, LCD is write-only (input)
//	ADBUS3      low  bit3  (CS)    LCD_CS      LCD chip select, active low (output)
//	ADBUS4      low  bit4          LCD_BL      backlight enable, active high (output)
//	ADBUS5      low  bit5          LCD_RST     LCD reset, active low (output)
//	ADBUS6      low  bit6          LCD_DC      data(1)/command(0) (output)
//	ADBUS7      low  bit7          FAN_GATE    MOSFET gate, active high (output)
//	ACBUS1      high bit1          FAN_TACH    fan tachometer, 10k pull-up to 3V3 (input)
//
// Note the coupling: the fan gate shares the low byte with every LCD control
// line, so any write to the low byte must preserve the fan-gate bit. The
// ftdi.Device shadow register handles that; callers just use these constants.
package board

// Low-byte (ADBUS) pin bit positions.
const (
	PinSCK  uint = 0 // ADBUS0 - SPI clock, driven by MPSSE
	PinMOSI uint = 1 // ADBUS1 - SPI data out, driven by MPSSE
	PinMISO uint = 2 // ADBUS2 - SPI data in, unused (LCD is write-only)
	PinCS   uint = 3 // ADBUS3 - LCD chip select (active low)
	PinBL   uint = 4 // ADBUS4 - LCD backlight (active high)
	PinRST  uint = 5 // ADBUS5 - LCD reset (active low)
	PinDC   uint = 6 // ADBUS6 - LCD data/command (data=1, command=0)
	PinFan  uint = 7 // ADBUS7 - fan MOSFET gate (active high)
)

// High-byte (ACBUS) pin bit positions.
const (
	PinTach uint = 1 // ACBUS1 - fan tachometer input
)

// LowDirMask is the MPSSE low-byte direction mask: 1 = output, 0 = input.
// All low-byte pins are outputs except MISO (ADBUS2), which is an input.
//
//	0b1111_1011 = 0xFB
const LowDirMask byte = 0xFB

// LowIdle is the power-on-safe idle state of the low byte, applied right after
// entering MPSSE mode and before the LCD is initialised:
//   - SCK idle low (SPI mode 0)
//   - CS high (LCD deselected, active low)
//   - RST low (hold the LCD in reset until the driver releases it)
//   - BL low (backlight off until we're ready to show something)
//   - DC low, MOSI low
//   - FAN gate low (fan off; matches the R3 gate pulldown default)
const LowIdle byte = (1 << PinCS)

// HighDirMask keeps the tach pin (ACBUS1) as an input; the rest are unused
// inputs too, so the whole high byte is input.
const HighDirMask byte = 0x00

// TachPulsesPerRev is the number of tach pulses a standard brushless PC fan
// emits per revolution. Two is near-universal for 3-wire fans.
const TachPulsesPerRev = 2
