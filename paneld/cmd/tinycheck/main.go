// Command tinycheck is the bench tool for the v2 board's ATtiny fan/backlight
// controller. It talks the 4-byte SPI protocol over the FT232H (MPSSE), with
// /CS_TINY on ACBUS0 and MISO on ADBUS2.
//
//	go run ./cmd/tinycheck                  # PING + STATUS
//	go run ./cmd/tinycheck -bl 200          # backlight on (0-255)
//	go run ./cmd/tinycheck -fan 128         # fan to 50% (firmware soft-starts)
//	go run ./cmd/tinycheck -sweep           # duty sweep with RPM readback table
//	go run ./cmd/tinycheck -watch           # poll STATUS once a second
//
// macOS note: run under sudo (libusb must capture the device from the Apple
// FTDI serial driver).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kaievns/sunny-side-up/paneld/internal/ftdi"
	"github.com/kaievns/sunny-side-up/paneld/internal/tiny"
)

// v2 ADBUS setup: SCK/MOSI out (bits 0,1), MISO in (bit 2), LCD CS out+idle
// high (bit 3), spare in (bit 4), LCD RST out+idle high (bit 5), LCD DC out
// (bit 6), spare in (bit 7). Mask 0x6B per docs/v2-rework-design.md.
const (
	lowIdle = 0x68 // CS_LCD + RST + DC high; SCK/MOSI low
	lowDir  = 0x6B
	// ACBUS: /CS_TINY on bit 0, output, idle high (deselected).
	highIdle = 0x01
	highDir  = 0x01
)

func main() {
	clockHz := flag.Int("clock", 15_000_000, "LCD SPI clock restored after tiny transactions")
	vid := flag.Int("vid", 0, "USB vendor id (0 = FT232H default)")
	pid := flag.Int("pid", 0, "USB product id (0 = FT232H default)")
	bl := flag.Int("bl", -1, "set backlight 0-255 (-1 = leave alone)")
	fanDuty := flag.Int("fan", -1, "set fan duty 0-255 (-1 = leave alone)")
	kick := flag.Bool("kick", false, "force a fan kick cycle")
	sweep := flag.Bool("sweep", false, "fan duty sweep with RPM readback (calibrates MIN_DUTY)")
	watch := flag.Bool("watch", false, "poll STATUS once a second until Ctrl-C")
	debug := flag.Bool("debug", false, "dump pin states around a raw transaction (bus bring-up)")
	tinyHz := flag.Int("tinyhz", 0, "override the tiny SPI clock (bring-up; 0 = default 1MHz)")
	flag.Parse()

	dev, err := ftdi.Open(*vid, *pid)
	if err != nil {
		log.Fatalf("tinycheck: %v", err)
	}
	defer dev.Close()

	if err := dev.EnableMPSSE(*clockHz, lowIdle, lowDir); err != nil {
		log.Fatalf("tinycheck: %v", err)
	}
	if err := dev.ConfigureHighByte(highIdle, highDir); err != nil {
		log.Fatalf("tinycheck: %v", err)
	}

	t := tiny.New(dev, *clockHz)
	if *tinyHz > 0 {
		t.SetBusHz(*tinyHz)
	}

	if *debug {
		hz := *tinyHz
		if hz <= 0 {
			hz = 1_000_000
		}
		runDebug(dev, hz)
		return
	}

	ver, flags, err := t.Ping()
	if err != nil {
		log.Fatalf("tinycheck: PING failed: %v", err)
	}
	fmt.Printf("PING ok: fw v%d, flags: %s\n", ver, flags)

	if *bl >= 0 {
		if err := t.SetBL(byte(*bl)); err != nil {
			log.Fatalf("tinycheck: BL: %v", err)
		}
		fmt.Printf("BL -> %d\n", *bl)
	}
	if *fanDuty >= 0 {
		if err := t.SetFanDuty(byte(*fanDuty)); err != nil {
			log.Fatalf("tinycheck: FAN_DUTY: %v", err)
		}
		fmt.Printf("FAN -> %d\n", *fanDuty)
	}
	if *kick {
		if err := t.Kick(); err != nil {
			log.Fatalf("tinycheck: KICK: %v", err)
		}
		fmt.Println("KICK sent")
	}

	if *sweep {
		runSweep(t)
		return
	}
	if *watch {
		runWatch(t)
		return
	}

	rpm, flags, err := t.Status()
	if err != nil {
		log.Fatalf("tinycheck: STATUS failed: %v", err)
	}
	fmt.Printf("STATUS: rpm=%d, flags: %s\n", rpm, flags)
}

// runDebug dumps raw pin states around a hand-rolled transaction so a dead
// bus can be localised: is /CS_TINY actually asserting, is MISO driven?
func runDebug(dev *ftdi.Device, busHz int) {
	rdHigh := func(tag string) {
		v, err := dev.ReadHighByte()
		fmt.Printf("  ACBUS=%08b (err=%v)  [bit0=/CS_TINY] %s\n", v, err, tag)
	}
	rdLow := func(tag string) {
		v, err := dev.ReadLowByte()
		fmt.Printf("  ADBUS=%08b (err=%v)  [bit2=MISO] %s\n", v, err, tag)
	}

	fmt.Println("idle state:")
	rdHigh("idle (expect bit0=1)")
	rdLow("idle")

	actual, err := dev.SetClockHz(busHz)
	if err != nil {
		log.Fatalf("debug: %v", err)
	}
	fmt.Printf("bus clock for this run: %d Hz\n", actual)
	fmt.Println("asserting /CS_TINY low:")
	if err := dev.SetHighPin(0, false); err != nil {
		log.Fatalf("debug: %v", err)
	}
	rdHigh("asserted (expect bit0=0)")
	rdLow("selected, before clocks (slave should now drive MISO)")

	fmt.Println("clocking 8 bytes of PING frame + padding, dumping each exchange:")
	// two PING frames with COMPUTED CRC (an earlier revision hardcoded a wrong
	// CRC byte here, making every debug frame invalid at the slave)
	pf := tiny.Frame(0x01, 0, 0)
	out := []byte{pf[0], pf[1], pf[2], pf[3], pf[0], pf[1], pf[2], pf[3]}
	fmt.Printf("(frame: % x)\n", out[:4])
	for i, b := range out {
		in, err := dev.SPIExchangeByte(b)
		fmt.Printf("  [%d] out=%02x in=%02x (err=%v)\n", i, b, in, err)
	}
	rdLow("still selected, after clocks")

	if err := dev.SetHighPin(0, true); err != nil {
		log.Fatalf("debug: %v", err)
	}
	rdHigh("deasserted (expect bit0=1)")
	rdLow("deselected (slave should release MISO)")

	// Edge matrix: against the spiecho firmware, the opcode whose echo comes
	// back byte-exact (offset by one) is the timing the slave actually wants.
	fmt.Println("\nedge matrix (echo probe: clean = in[i] == out[i-1]):")
	for _, op := range []byte{0x31, 0x30, 0x34, 0x35} {
		dev.SetHighPin(0, false)
		fmt.Printf("  opcode 0x%02x: ", op)
		for _, b := range out {
			in, err := dev.SPIExchangeByteOp(op, b)
			if err != nil {
				fmt.Printf("err=%v ", err)
				break
			}
			fmt.Printf("%02x ", in)
		}
		dev.SetHighPin(0, true)
		fmt.Println()
	}
	fmt.Println("  (sent each round:  01 00 00 5e 01 00 00 5e)")
}

func runSweep(t *tiny.Client) {
	fmt.Println("duty sweep (4s settle per step; RPM windows are 1s):")
	fmt.Println("duty  rpm")
	// Descending so the spinning fan carries into the low-duty region (the
	// keep-spinning floor; the from-standstill floor is kickstart territory).
	// Dense below 40: light-load DCM lifts the buck output, so the whole
	// usable control range hides down there.
	for _, duty := range []int{255, 200, 160, 128, 100, 80, 60, 40, 32, 26, 20, 16, 12, 8, 5, 0} {
		if err := t.SetFanDuty(byte(duty)); err != nil {
			log.Fatalf("sweep: %v", err)
		}
		time.Sleep(4 * time.Second)
		rpm, _, err := t.Status()
		if err != nil {
			log.Fatalf("sweep: %v", err)
		}
		fmt.Printf("%4d  %d\n", duty, rpm)
	}
	fmt.Println("sweep done, fan off")
}

func runWatch(t *tiny.Client) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			rpm, flags, err := t.Status()
			if err != nil {
				fmt.Printf("status error: %v\n", err)
				continue
			}
			fmt.Printf("rpm=%-5d flags: %s\n", rpm, flags)
		}
	}
}
