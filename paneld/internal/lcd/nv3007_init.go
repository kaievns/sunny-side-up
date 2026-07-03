package lcd

import "time"

// This file holds the panel geometry and the verified NV3007 initialisation
// data for the BuyDisplay 2.79" 142x428 IPS panel. The register block was
// cross-verified from four independent sources and reconciled:
//   - ruiofang/ESP-IDF-TFT-NV3007-LVGL (lcd_init.c) - drives this exact panel
//   - lvgl NV3007 driver (src/drivers/display/nv3007)
//   - Bodmer/TFT_eSPI issue #3851 (tested 2.79" 142x428 config)
//   - Novatek NV3007 datasheet V1.0 (2024-06-19)
//
// The vendor "magic" registers (power/gamma/GIP) are undocumented in the
// datasheet but present in every working driver; they are required for the
// panel to display correctly and are reproduced verbatim.

// Native panel geometry, as the controller addresses it (portrait). The visible
// area is 142x428, but the controller has 168 columns of RAM, so a 12-pixel
// column offset positions the visible window (value taken from the tested
// ESP-IDF LCD_X_OFFSET=0x0C and TFT_eSPI TFT_COLUMN_OFFSET=12; the
// datasheet-centered value would be (168-142)/2 = 13). Row offset is 0.
//
// We always drive the controller in this native orientation (MADCTL rotation
// bits off) and rotate in software when blitting, so this addressing exactly
// matches the known-good reference drivers.
const (
	nativeW = 142
	nativeH = 428
	colOff  = 12
	rowOff  = 0
)

// Post-command delays. The datasheet requires >=120ms after SLPOUT; the
// reference driver uses 220ms after SLPOUT and 200ms after DISPON.
const (
	slpOutDelay = 220 * time.Millisecond
	dispOnDelay = 200 * time.Millisecond
)

// nv3007VendorInit runs from the extended-command page-enter (0xFF 0xA5)
// through the power/gamma/GIP registers, then page-exit (0xFF 0x00) and pixel
// format (0x3A = 0x05, RGB565). Orientation (0x36), inversion (0x20/0x21),
// sleep-out (0x11) and display-on (0x29) are applied afterwards by Init so they
// stay configurable.
var nv3007VendorInit = []initStep{
	{cmd: 0xFF, data: []byte{0xA5}, delay: time.Millisecond}, // enter extended command page
	{cmd: 0x9A, data: []byte{0x08}},
	{cmd: 0x9B, data: []byte{0x08}},
	{cmd: 0x9C, data: []byte{0xB0}},
	{cmd: 0x9D, data: []byte{0x16}},
	{cmd: 0x9E, data: []byte{0xC4}},
	{cmd: 0x8F, data: []byte{0x55, 0x04}},
	{cmd: 0x84, data: []byte{0x90}},
	{cmd: 0x83, data: []byte{0x7B}},
	{cmd: 0x85, data: []byte{0x33}},
	{cmd: 0x60, data: []byte{0x00}},
	{cmd: 0x70, data: []byte{0x00}},
	{cmd: 0x61, data: []byte{0x02}},
	{cmd: 0x71, data: []byte{0x02}},
	{cmd: 0x62, data: []byte{0x04}},
	{cmd: 0x72, data: []byte{0x04}},
	{cmd: 0x6C, data: []byte{0x29}},
	{cmd: 0x7C, data: []byte{0x29}},
	{cmd: 0x6D, data: []byte{0x31}},
	{cmd: 0x7D, data: []byte{0x31}},
	{cmd: 0x6E, data: []byte{0x0F}},
	{cmd: 0x7E, data: []byte{0x0F}},
	{cmd: 0x66, data: []byte{0x21}},
	{cmd: 0x76, data: []byte{0x21}},
	{cmd: 0x68, data: []byte{0x3A}},
	{cmd: 0x78, data: []byte{0x3A}},
	{cmd: 0x63, data: []byte{0x07}},
	{cmd: 0x73, data: []byte{0x07}},
	{cmd: 0x64, data: []byte{0x05}},
	{cmd: 0x74, data: []byte{0x05}},
	{cmd: 0x65, data: []byte{0x02}},
	{cmd: 0x75, data: []byte{0x02}},
	{cmd: 0x67, data: []byte{0x23}},
	{cmd: 0x77, data: []byte{0x23}},
	{cmd: 0x69, data: []byte{0x08}},
	{cmd: 0x79, data: []byte{0x08}},
	{cmd: 0x6A, data: []byte{0x13}},
	{cmd: 0x7A, data: []byte{0x13}},
	{cmd: 0x6B, data: []byte{0x13}},
	{cmd: 0x7B, data: []byte{0x13}},
	{cmd: 0x6F, data: []byte{0x00}},
	{cmd: 0x7F, data: []byte{0x00}},
	{cmd: 0x50, data: []byte{0x00}},
	{cmd: 0x52, data: []byte{0xD6}},
	{cmd: 0x53, data: []byte{0x08}},
	{cmd: 0x54, data: []byte{0x08}},
	{cmd: 0x55, data: []byte{0x1E}},
	{cmd: 0x56, data: []byte{0x1C}},
	{cmd: 0xA0, data: []byte{0x2B, 0x24, 0x00}}, // goa map_sel
	{cmd: 0xA1, data: []byte{0x87}},
	{cmd: 0xA2, data: []byte{0x86}},
	{cmd: 0xA5, data: []byte{0x00}},
	{cmd: 0xA6, data: []byte{0x00}},
	{cmd: 0xA7, data: []byte{0x00}},
	{cmd: 0xA8, data: []byte{0x36}},
	{cmd: 0xA9, data: []byte{0x7E}},
	{cmd: 0xAA, data: []byte{0x7E}},
	{cmd: 0xB9, data: []byte{0x85}},
	{cmd: 0xBA, data: []byte{0x84}},
	{cmd: 0xBB, data: []byte{0x83}},
	{cmd: 0xBC, data: []byte{0x82}},
	{cmd: 0xBD, data: []byte{0x81}},
	{cmd: 0xBE, data: []byte{0x80}},
	{cmd: 0xBF, data: []byte{0x01}},
	{cmd: 0xC0, data: []byte{0x02}},
	{cmd: 0xC1, data: []byte{0x00}},
	{cmd: 0xC2, data: []byte{0x00}},
	{cmd: 0xC3, data: []byte{0x00}},
	{cmd: 0xC4, data: []byte{0x33}},
	{cmd: 0xC5, data: []byte{0x7E}},
	{cmd: 0xC6, data: []byte{0x7E}},
	{cmd: 0xC8, data: []byte{0x33, 0x33}},
	{cmd: 0xC9, data: []byte{0x68}},
	{cmd: 0xCA, data: []byte{0x69}},
	{cmd: 0xCB, data: []byte{0x6A}},
	{cmd: 0xCC, data: []byte{0x6B}},
	{cmd: 0xCD, data: []byte{0x33, 0x33}},
	{cmd: 0xCE, data: []byte{0x6C}},
	{cmd: 0xCF, data: []byte{0x6D}},
	{cmd: 0xD0, data: []byte{0x6E}},
	{cmd: 0xD1, data: []byte{0x6F}},
	{cmd: 0xAB, data: []byte{0x03, 0x67}},
	{cmd: 0xAC, data: []byte{0x03, 0x6B}},
	{cmd: 0xAD, data: []byte{0x03, 0x68}},
	{cmd: 0xAE, data: []byte{0x03, 0x6C}},
	{cmd: 0xB3, data: []byte{0x00}},
	{cmd: 0xB4, data: []byte{0x00}},
	{cmd: 0xB5, data: []byte{0x00}},
	{cmd: 0xB6, data: []byte{0x32}},
	{cmd: 0xB7, data: []byte{0x7E}},
	{cmd: 0xB8, data: []byte{0x7E}},
	{cmd: 0xE0, data: []byte{0x00}},
	{cmd: 0xE1, data: []byte{0x03, 0x0F}},
	{cmd: 0xE2, data: []byte{0x04}},
	{cmd: 0xE3, data: []byte{0x01}},
	{cmd: 0xE4, data: []byte{0x0E}},
	{cmd: 0xE5, data: []byte{0x01}},
	{cmd: 0xE6, data: []byte{0x19}},
	{cmd: 0xE7, data: []byte{0x10}},
	{cmd: 0xE8, data: []byte{0x10}},
	{cmd: 0xEA, data: []byte{0x12}},
	{cmd: 0xEB, data: []byte{0xD0}},
	{cmd: 0xEC, data: []byte{0x04}},
	{cmd: 0xED, data: []byte{0x07}},
	{cmd: 0xEE, data: []byte{0x07}},
	{cmd: 0xEF, data: []byte{0x09}},
	{cmd: 0xF0, data: []byte{0xD0}},
	{cmd: 0xF1, data: []byte{0x0E}},
	{cmd: 0xF9, data: []byte{0x17}},
	{cmd: 0xF2, data: []byte{0x2C, 0x1B, 0x0B, 0x20}},
	{cmd: 0xE9, data: []byte{0x29}}, // 1 dot inversion
	{cmd: 0xEC, data: []byte{0x04}},
	{cmd: 0x35, data: []byte{0x00}},       // TEON (tear effect line; pin not wired, harmless)
	{cmd: 0x44, data: []byte{0x00, 0x10}}, // set tear scanline
	{cmd: 0x46, data: []byte{0x10}},
	{cmd: 0xFF, data: []byte{0x00}, delay: time.Millisecond}, // exit to standard command page
	{cmd: 0x3A, data: []byte{0x05}},                          // COLMOD = RGB565 (16bpp)
}
