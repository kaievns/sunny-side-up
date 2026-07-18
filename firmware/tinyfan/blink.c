/*
 * blink — the minimal "is this chip executing at all" test. NO peripherals:
 * no CLKCTRL write (runs on the reset-default 20 MHz/6 = 3.33 MHz), no TCA0,
 * no SPI — nothing but raw port writes and busy delays. If this shows no
 * output, the problem is not peripheral configuration.
 *
 * Cycle (~13 s):
 *   8 s: backlight gate (PA5) toggles at 2 Hz  -> screen BL blinks
 *   4 s: fan gate (PB0) soft-ramped then held  -> fan spins at full speed
 *   1 s: both off, repeat
 *
 * The fan turn-on is a ~0.2 s software-PWM ramp so the buck's 47 uH never
 * sees a hard 0->100 % step into the discharged 22 uF (saturation spike).
 *
 * Readings:
 *   BL blinks + fan spins  -> chip fine, buck fine, BL path fine: the earlier
 *                             deadness was the TCA0 PWM code. My bug.
 *   BL blinks, fan dead    -> chip fine; fault is in the buck (Q1/Q2/L1/D1/J3).
 *   fan spins, BL dead     -> chip fine; fault is FPC seating / LED path / Q3.
 *   neither                -> chip not executing, or both gate nets broken:
 *                             next step is a meter on PA5/PB0 pins directly.
 *
 * Reflash tinyfan.hex afterwards.
 */

#define F_CPU 3333333UL   /* reset default: OSC20M / 6 */

#include <avr/io.h>
#include <util/delay.h>
#include <stdint.h>

static void fan_soft_on(void)
{
	/* software-PWM ramp 0 -> 100 % over ~0.2 s, then solid high */
	for (uint8_t duty = 1; duty <= 100; duty++) {
		for (uint8_t r = 0; r < 2; r++) {
			PORTB.OUTSET = PIN0_bm;
			for (uint8_t t = 0; t < duty; t++)
				_delay_us(8);
			PORTB.OUTCLR = PIN0_bm;
			for (uint8_t t = duty; t < 100; t++)
				_delay_us(8);
		}
	}
	PORTB.OUTSET = PIN0_bm;
}

int main(void)
{
	PORTA.DIRSET = PIN5_bm;   /* BL gate (Q3) */
	PORTB.DIRSET = PIN0_bm;   /* fan gate (Q1 -> buck) */

	for (;;) {
		/* phase 1: blink the backlight, fan off */
		PORTB.OUTCLR = PIN0_bm;
		for (uint8_t i = 0; i < 16; i++) {
			PORTA.OUTTGL = PIN5_bm;
			_delay_ms(250);
		}

		/* phase 2: backlight held on, fan soft-on to full */
		PORTA.OUTSET = PIN5_bm;
		fan_soft_on();
		_delay_ms(4000);

		/* phase 3: everything off, breathe, repeat */
		PORTB.OUTCLR = PIN0_bm;
		PORTA.OUTCLR = PIN5_bm;
		_delay_ms(1000);
	}
}
