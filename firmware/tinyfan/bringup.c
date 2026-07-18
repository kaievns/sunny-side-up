/*
 * bringup — ATtiny414 dual-output diagnostic: drives the backlight AND the fan
 * so a black screen / dead fan can be split into "chip not executing" vs
 * "buck broken". NOT the product firmware.
 *
 * Loop (~7 s):
 *   Phase A: backlight breathes up/down twice, fan off   -> is the chip alive?
 *   Phase B: backlight held dim, fan soft-starts to 100 % -> does the buck work?
 *
 * Backlight breathes  => chip executes + PA5/Q3/LED path good.
 * Fan also kicks       => buck good.
 * Nothing at all       => chip is not running (reset/power/halt), not the buck.
 *
 * Reflash tinyfan.hex to restore the real firmware afterwards.
 */

#define F_CPU 10000000UL

#include <avr/io.h>
#include <util/delay.h>
#include <stdint.h>

#define FAN_PER 159   /* WO0 / PB0 @ 31.25 kHz */
#define BL_PER  124   /* WO5 / PA5 @ 40 kHz    */

static inline void fan(uint8_t d)
{
	TCA0.SPLIT.LCMP0 = (uint8_t)(((uint16_t)d * (FAN_PER + 1)) / 255);
}

static inline void bl(uint8_t b)
{
	TCA0.SPLIT.HCMP2 = (uint8_t)(((uint16_t)b * (BL_PER + 1)) / 255);
}

int main(void)
{
	_PROTECTED_WRITE(CLKCTRL.MCLKCTRLB, CLKCTRL_PEN_bm);   /* 10 MHz */

	PORTB.DIRSET = PIN0_bm;   /* PB0 = fan (WO0) */
	PORTA.DIRSET = PIN5_bm;   /* PA5 = BL  (WO5) */
	TCA0.SPLIT.CTRLD = TCA_SPLIT_SPLITM_bm;
	TCA0.SPLIT.LPER  = FAN_PER;
	TCA0.SPLIT.HPER  = BL_PER;
	TCA0.SPLIT.LCMP0 = 0;
	TCA0.SPLIT.HCMP2 = 0;
	TCA0.SPLIT.CTRLB = TCA_SPLIT_LCMP0EN_bm | TCA_SPLIT_HCMP2EN_bm;
	TCA0.SPLIT.CTRLA = TCA_SPLIT_CLKSEL_DIV2_gc | TCA_SPLIT_ENABLE_bm;

	for (;;) {
		/* Phase A: breathe the backlight twice, fan off */
		fan(0);
		for (uint8_t n = 0; n < 2; n++) {
			for (uint8_t b = 0; b < 250; b++) { bl(b); _delay_ms(3); }
			for (uint8_t b = 250; b > 0; b--) { bl(b); _delay_ms(3); }
		}

		/* Phase B: hold backlight dim so the screen stays visibly lit while the
		 * fan soft-starts to 100 % and holds (soft-start protects L1). */
		bl(60);
		for (uint16_t d = 0; d <= 255; d++) { fan((uint8_t)d); _delay_us(60); }
		_delay_ms(2000);
		for (uint8_t d = 255; d > 0; d--) { fan(d); _delay_ms(4); }
		fan(0);
		_delay_ms(800);
	}
}
