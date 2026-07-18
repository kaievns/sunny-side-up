/*
 * tinyfan — ATtiny414 fan + backlight controller for the lcd-panel v2 board.
 *
 * Milestone 1 (per docs/v2-software-design.md): 10 MHz core, TCA0 split-mode
 * PWM (fan on PB0/WO0, backlight on PA5/WO5), an SPI slave speaking the 4-byte
 * CRC-8 framed protocol (PING / FAN_DUTY / BL / STATUS / FAILSAFE_CFG), tach
 * counting, a from-reset failsafe, and an inrush-safe soft-start on the fan.
 *
 * The ATtiny is a dumb executor: paneld owns the curve/policy. Firmware owns
 * only what must survive a dead host (PWM, tach, failsafe).
 *
 * Clocks: OSC20M (fuse OSCCFG=0x02, factory default) with the main prescaler
 * at /2 -> 10 MHz (in-spec at 3V3). No fuse writes needed.
 * TCA0 split, prescaler /2 -> 5 MHz timer clock:
 *   fan  WO0 = LCMP0, LPER=159 -> 31.25 kHz
 *   BL   WO5 = HCMP2, HPER=124 -> 40 kHz
 */

#define F_CPU 10000000UL

#include <avr/io.h>
#include <avr/interrupt.h>
#include <avr/pgmspace.h>
#include <util/delay.h>
#include <stdint.h>

/* ---------- protocol (mirrors docs/v2-software-design.md) ---------- */
#define FW_VERSION     0x01

#define CMD_PING       0x01
#define CMD_FAN_DUTY   0x10
#define CMD_BL         0x11
#define CMD_FAN_KICK   0x12
#define CMD_STATUS     0x20
#define CMD_FAILSAFE   0x30
#define RESP_FLAG      0x80   /* set in the echoed cmd byte of a response */
#define RESP_ERR       0xFF   /* bad CRC / unknown cmd */

/* status flags (byte returned in most responses; see build_resp usage) */
#define F_KICKING          0x01
#define F_STALL_FAULT      0x02
#define F_FAILSAFE_ACTIVE  0x04
#define F_HOST_STALE       0x08

/* ---------- PWM periods ---------- */
#define FAN_PER   159   /* 5 MHz / (159+1) = 31.25 kHz */
#define BL_PER    124   /* 5 MHz / (124+1) = 40.00 kHz */

/* ---------- failsafe (armed from reset; see design doc) ---------- */
#define FAILSAFE_TIMEOUT_S   60
#define FAILSAFE_FAN_DUTY    178   /* ~70% of 255 */
#define BL_FAILSAFE_CAP      76    /* ~30% of 255 — never raise BL from off */

/* Keep-spinning floor, bench-calibrated 2026-07-18 (NF-A4x10 5V, board #2):
 * descending sweep held 3000 rpm at duty 20 and stalled hard at 16; DCM
 * light-load lift compresses the range (duty 20..255 -> 3000..4440 rpm).
 * 26 keeps one step of margin above the cliff. Nonzero commands below this
 * are raised to it so paneld can't accidentally park the fan in the stall
 * zone with a nonzero duty. */
#define MIN_DUTY             26

/* Boot bring-up "hello": pulse the backlight twice so a flashed board shows
 * life on the bench even before any host SPI exists. Set to 0 for production
 * (design doc mandates cold-boot BL off). Only visible if the LCD FPC is on. */
#define BRINGUP_HELLO  1

/* ===================== shared state ===================== */
/* uint8_t targets: single-byte => atomic on AVR, written only in the SPI ISR,
 * read in main. 16-bit vars below are touched only by non-nesting ISRs. */
static volatile uint8_t g_flags = 0;
static volatile uint8_t fan_target = 0;   /* commanded duty 0..255 */
static volatile uint8_t bl_target  = 0;   /* commanded brightness 0..255 */
static volatile uint8_t failsafe_duty    = FAILSAFE_FAN_DUTY;
static volatile uint8_t failsafe_timeout = FAILSAFE_TIMEOUT_S;

static volatile uint16_t silence_s  = 0;  /* seconds since last valid frame */
static volatile uint16_t tach_edges = 0;  /* rising edges this 1 s window */
static volatile uint16_t rpm        = 0;  /* last computed RPM */

static uint8_t fan_current = 0;           /* soft-start ramps this to target */

/* ===================== CRC-8 (Dallas/Maxim, poly 0x31 reflected = 0x8C) =====
 * TABLE-DRIVEN, table in flash. This is load-bearing for the SPI protocol:
 * process_frame() runs INSIDE the SPI ISR, and with the old bit-banged loop
 * (~100-150us for two passes) the response's DATA reload landed after the
 * next byte had started - the write collided (WRCOL) and was discarded, so
 * every response byte after a frame boundary went stale. Found on hardware
 * 2026-07-18: streams read clean exactly through byte 3 and broke from
 * byte 4 on. Three lookups (~15us worst case) keep the reload inside any
 * inter-byte gap. Sanity anchors: crc(FF,FF,FF)=0x66, crc(01,00,00)=0xAB. */
static const uint8_t crc8_tab[256] PROGMEM = {
#include "crc8_tab.inc"
};

static uint8_t crc8(const volatile uint8_t *d, uint8_t n)
{
	uint8_t crc = 0;
	while (n--)
		crc = pgm_read_byte(&crc8_tab[crc ^ *d++]);
	return crc;
}

/* ===================== PWM ===================== */
/* Map 0..255 to compare counts, capped at PER: a compare value of PER+1
 * (the naive 100% result) never matches and parks the pin LOW — found on
 * hardware: duty 255 turned the fan OFF. CMP=PER gives (PER)/(PER+1) ≈
 * 99.4% — DC after the gate RC, indistinguishable from full-on. */
static inline void fan_pwm(uint8_t duty255)
{
	uint16_t c = ((uint16_t)duty255 * (FAN_PER + 1)) / 255;
	if (c > FAN_PER)
		c = FAN_PER;
	TCA0.SPLIT.LCMP0 = (uint8_t)c;
}

static inline void bl_pwm(uint8_t b255)
{
	uint16_t c = ((uint16_t)b255 * (BL_PER + 1)) / 255;
	if (c > BL_PER)
		c = BL_PER;
	TCA0.SPLIT.HCMP2 = (uint8_t)c;
}

/* ===================== SPI framing ===================== */
static volatile uint8_t rxf[4];
static volatile uint8_t txf[4] = { RESP_ERR, RESP_ERR, RESP_ERR, 0 };
static volatile uint8_t sidx = 0;

static void build_resp(uint8_t b0, uint8_t d0, uint8_t d1)
{
	txf[0] = b0;
	txf[1] = d0;
	txf[2] = d1;
	txf[3] = crc8(txf, 3);
}

/* Called from the SPI ISR once a full 4-byte master frame has arrived; it
 * builds the response to *this* frame, which is shifted out during the next
 * one (classic offset-by-one slave). */
static void process_frame(void)
{
	if (crc8(rxf, 3) != rxf[3]) {
		build_resp(RESP_ERR, RESP_ERR, RESP_ERR);
		return;
	}
	/* a valid frame means the host is alive */
	silence_s = 0;
	g_flags &= (uint8_t)~(F_HOST_STALE | F_FAILSAFE_ACTIVE);

	uint8_t cmd = rxf[0];
	switch (cmd) {
	case CMD_PING:
		build_resp(CMD_PING | RESP_FLAG, FW_VERSION, g_flags);
		break;
	case CMD_FAN_DUTY: {
		uint8_t d = rxf[1];
		if (d > 0 && d < MIN_DUTY)
			d = MIN_DUTY;   /* clamp out of the stall zone */
		fan_target = d;
		build_resp(cmd | RESP_FLAG, g_flags, 0);
		break;
	}
	case CMD_BL:
		bl_target = rxf[1];
		bl_pwm(bl_target);
		build_resp(cmd | RESP_FLAG, g_flags, 0);
		break;
	case CMD_FAN_KICK:
		/* full kickstart cycle is Milestone 2; ack for now */
		build_resp(cmd | RESP_FLAG, g_flags, 0);
		break;
	case CMD_STATUS:
		build_resp(CMD_STATUS | RESP_FLAG, (uint8_t)(rpm >> 8),
			   (uint8_t)(rpm & 0xFF));
		break;
	case CMD_FAILSAFE:
		failsafe_duty = rxf[1];
		failsafe_timeout = rxf[2];
		build_resp(cmd | RESP_FLAG, g_flags, 0);
		break;
	default:
		build_resp(RESP_ERR, RESP_ERR, RESP_ERR);
		break;
	}
}

/* SPI_PROBE=1 builds a diagnostic image: the backlight brightness becomes an
 * ISR trace (bright = SPI ISR ran, dim = /SS resync ISR ran) so a silent bus
 * can be localised on a board with no debug port. Production builds: 0. */
#ifndef SPI_PROBE
#define SPI_PROBE 0
#endif

ISR(SPI0_INT_vect)
{
#if SPI_PROBE
	TCA0.SPLIT.HCMP2 = 90;        /* visible: the SPI ISR is running */
#endif
	(void)SPI0.INTFLAGS;          /* read INTFLAGS first: arms WRCOL clearing */
	uint8_t in = SPI0.DATA;       /* read clears the interrupt flag */
	rxf[sidx] = in;
	if (++sidx >= 4) {
		process_frame();      /* builds txf = response to this frame */
		sidx = 0;
		SPI0.DATA = txf[0];   /* preload first byte for the next frame */
	} else {
		SPI0.DATA = txf[sidx];
	}
}

/* /SS rising edge = end of transaction. Resync the byte index and flush the
 * SPI shift path (toggling ENABLE is the only way to clear it on tinyAVR) so a
 * short/aborted transfer can't offset every later response by one byte. */
ISR(PORTA_PORT_vect)
{
	if (PORTA.INTFLAGS & PIN4_bm) {
		PORTA.INTFLAGS = PIN4_bm;
#if SPI_PROBE
		if (TCA0.SPLIT.HCMP2 < 12)
			TCA0.SPLIT.HCMP2 = 12;   /* dim: resync ISR ran */
#endif
		/* Glitch filter (found on hardware, 2026-07-18): SCK/MOSI bursts
		 * couple ns-class spikes onto the /SS trace that set ISC_RISING
		 * between EVERY byte — without ever registering at the SPI's own,
		 * slower /SS synchronizer (spidbg received clean bytes while this
		 * flag fired continuously). Resetting the SPI on each spike is what
		 * killed the link (alternating driven/Hi-Z response bytes). Only
		 * resync when /SS is REALLY high: a spike is long gone by ISR
		 * entry; a genuine end-of-frame deassert still reads high. */
		if (!(PORTA.IN & PIN4_bm))
			return;
		sidx = 0;
		SPI0.CTRLA &= (uint8_t)~SPI_ENABLE_bm;
		SPI0.CTRLA |= SPI_ENABLE_bm;
		SPI0.DATA = txf[0];
	}
}

/* tach: rising edges on PB1 */
ISR(PORTB_PORT_vect)
{
	if (PORTB.INTFLAGS & PIN1_bm) {
		PORTB.INTFLAGS = PIN1_bm;
		tach_edges++;
	}
}

/* 1 s tick: fold tach edges into RPM and run the failsafe countdown */
ISR(RTC_PIT_vect)
{
	RTC.PITINTFLAGS = RTC_PI_bm;
	/* 2 pulses/rev over a 1 s window: rpm = edges/2 * 60 = edges * 30 */
	rpm = (uint16_t)(tach_edges * 30);
	tach_edges = 0;
	if (silence_s < 0xFFFF)
		silence_s++;
	if (silence_s >= failsafe_timeout)
		g_flags |= (F_HOST_STALE | F_FAILSAFE_ACTIVE);
}

/* ===================== init ===================== */
static void clock_init(void)
{
	/* OSC20M is the reset-default source; enable the main prescaler at /2
	 * (PDIV=0) -> 10 MHz. CLKCTRL is CCP-protected. */
	_PROTECTED_WRITE(CLKCTRL.MCLKCTRLB, CLKCTRL_PEN_bm);
}

static void pwm_init(void)
{
	PORTB.DIRSET = PIN0_bm;   /* PB0 = fan (WO0) */
	PORTA.DIRSET = PIN5_bm;   /* PA5 = BL  (WO5) */

	TCA0.SPLIT.CTRLD = TCA_SPLIT_SPLITM_bm;
	TCA0.SPLIT.LPER  = FAN_PER;
	TCA0.SPLIT.HPER  = BL_PER;
	TCA0.SPLIT.LCMP0 = 0;      /* fan off */
	TCA0.SPLIT.HCMP2 = 0;      /* BL off  */
	TCA0.SPLIT.CTRLB = TCA_SPLIT_LCMP0EN_bm | TCA_SPLIT_HCMP2EN_bm;
	TCA0.SPLIT.CTRLA = TCA_SPLIT_CLKSEL_DIV2_gc | TCA_SPLIT_ENABLE_bm;
}

static void spi_slave_init(void)
{
	/* Slave: MISO (PA2) is driven by the peripheral only while selected and
	 * tri-stated when /SS is high; MOSI/SCK/SS are inputs. R21 (external 10k)
	 * holds /SS high when the FT232H tri-states it. */
	PORTA.DIRSET = PIN2_bm;               /* MISO */
	PORTA.PIN4CTRL = PORT_ISC_RISING_gc;  /* /SS rising -> resync ISR */

	SPI0.CTRLB = SPI_MODE_0_gc;           /* SSD=0 (use /SS), BUFEN=0 */
	SPI0.CTRLA = SPI_ENABLE_bm;           /* slave, MSB first */
	SPI0.INTCTRL = SPI_IE_bm;
	SPI0.DATA = txf[0];                   /* preload first response byte */
}

static void tach_init(void)
{
	/* PB1 input (default); R5 external 10k pull-up. Count rising edges. */
	PORTB.PIN1CTRL = PORT_ISC_RISING_gc;
}

static void rtc_pit_init(void)
{
	while (RTC.STATUS & RTC_CTRLABUSY_bm)
		;
	RTC.CLKSEL = RTC_CLKSEL_INT32K_gc;    /* 32.768 kHz internal ULP osc */
	RTC.PITINTCTRL = RTC_PI_bm;
	while (RTC.PITSTATUS & RTC_CTRLBUSY_bm)
		;
	RTC.PITCTRLA = RTC_PERIOD_CYC32768_gc | RTC_PITEN_bm;  /* 1 s */
}

int main(void)
{
	clock_init();
	pwm_init();
	spi_slave_init();
	tach_init();
	rtc_pit_init();

	txf[3] = crc8(txf, 3);  /* valid CRC on the idle response */
	sei();

#if BRINGUP_HELLO
	/* two ~0.5 s backlight swells: visible "I'm running your code" on a
	 * board with the LCD FPC attached. Harmless otherwise. */
	for (uint8_t k = 0; k < 2; k++) {
		for (uint8_t b = 0; b < 125; b++) { bl_pwm((uint8_t)(b * 2)); _delay_ms(4); }
		for (uint8_t b = 125; b > 0; b--) { bl_pwm((uint8_t)(b * 2)); _delay_ms(4); }
	}
	bl_pwm(0);
#endif

	for (;;) {
		uint8_t ftgt = fan_target;   /* atomic byte reads */
		uint8_t btgt = bl_target;

		if (g_flags & F_FAILSAFE_ACTIVE) {
			ftgt = failsafe_duty;
			if (btgt > BL_FAILSAFE_CAP)
				btgt = BL_FAILSAFE_CAP;
#if !SPI_PROBE   /* probe builds: BL belongs to the ISR trace, don't stomp it */
			bl_pwm(btgt);
#endif
		}

		/* soft-start: ramp the fan PWM ~1 step/ms so a 0->full command can't
		 * saturate L1 / yank a multi-amp inrush through VBUS. */
		if (fan_current < ftgt)
			fan_current++;
		else if (fan_current > ftgt)
			fan_current--;
		fan_pwm(fan_current);

		_delay_ms(1);
	}
}
