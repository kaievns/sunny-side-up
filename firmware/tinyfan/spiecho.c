/*
 * spiecho — SPI echo probe. NOT the product firmware.
 *
 * Every byte received on SPI is immediately loaded back into DATA, so the
 * host's next exchange reads back what the slave HEARD one byte earlier.
 * Run `tinycheck -debug` against it: sent vs heard, corruption made visible.
 * (spidbg only proved bytes arrive; this shows their values.)
 *
 * Tight polling loop — no delays — so the reload always lands within the
 * host's ~250 us inter-byte gap. Backlight goes solid on the first received
 * byte (liveness cue). Fan held off. Reflash tinyfan.hex afterwards.
 */

#define F_CPU 3333333UL

#include <avr/io.h>
#include <stdint.h>

int main(void)
{
	PORTA.DIRSET = PIN5_bm | PIN2_bm;   /* BL + MISO */
	PORTB.DIRSET = PIN0_bm;             /* fan gate low */
	PORTB.OUTCLR = PIN0_bm;

	SPI0.CTRLB = SPI_MODE_0_gc;
	SPI0.CTRLA = SPI_ENABLE_bm;
	SPI0.DATA = 0x5A;                   /* first-ever readback marker */

	for (;;) {
		if (SPI0.INTFLAGS & SPI_IF_bm) {
			uint8_t r = SPI0.DATA;      /* what we heard */
			SPI0.DATA = r;              /* say it back */
			PORTA.OUTSET = PIN5_bm;     /* alive */
		}
	}
}
