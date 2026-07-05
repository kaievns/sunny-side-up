// Command fanpwm maps this fan's usable speed band: it drives high-frequency PWM
// on the gate (via MPSSE clock-delay commands, executed at the chip's clock -
// steady enough to act like DC voltage control) and, at each duty step, measures
// the real RPM from the tach. It sweeps in small increments so you can see where
// the fan slows and where it stalls.
//
//	fanpwm                 # 100%..40% in 5% steps, ~5s each
//	fanpwm -khz 25 -step 2
package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/kaievns/sunny-side-up/paneld/internal/board"
	"github.com/kaievns/sunny-side-up/paneld/internal/ftdi"
)

func main() {
	clockHz := flag.Int("clock", 15_000_000, "SPI/MPSSE clock in Hz")
	khz := flag.Float64("khz", 20, "PWM frequency in kHz")
	step := flag.Int("step", 5, "duty step (percent)")
	lo := flag.Int("lo", 40, "lowest duty to test (percent)")
	settle := flag.Duration("settle", 2*time.Second, "settle time before measuring each step")
	measure := flag.Duration("measure", 3*time.Second, "measurement window per step")
	flag.Parse()

	dev, err := ftdi.Open(0, 0)
	if err != nil {
		log.Fatalf("fanpwm: %v", err)
	}
	defer dev.Close()
	if err := dev.EnableMPSSE(*clockHz, board.LowIdle, board.LowDirMask); err != nil {
		log.Fatalf("fanpwm: %v", err)
	}
	defer dev.SetLowPin(board.PinFan, false)

	clk := dev.ClockHz()
	freq := *khz * 1000
	fmt.Printf("fan speed sweep @ %.1f kHz PWM (settle %s, measure %s)\n\n", *khz, *settle, *measure)

	// Spin up to full first so the sweep tests whether it MAINTAINS each speed.
	_ = dev.SetLowPin(board.PinFan, true)
	time.Sleep(1500 * time.Millisecond)

	fmt.Printf("  %-6s %-7s %s\n", "duty", "RPM", "note")
	for pct := 100; pct >= *lo; pct -= *step {
		rpm := stepRPM(dev, float64(pct)/100, freq, clk, *settle, *measure)
		note := ""
		if rpm < 200 {
			note = "stalled"
		}
		fmt.Printf("  %-5d%% %-7d %s\n", pct, rpm, note)
	}
	_ = dev.SetLowPin(board.PinFan, false)
	fmt.Println("\ndone (fan off)")
}

// stepRPM holds the fan at duty (high-freq PWM), lets it settle, then measures
// real RPM by embedding a tach read at a powered phase of the waveform.
func stepRPM(dev *ftdi.Device, duty, freqHz float64, clk int, settle, window time.Duration) int {
	base := board.LowIdle &^ (byte(1) << board.PinFan)
	dir := board.LowDirMask
	fanOn := base | (byte(1) << board.PinFan)

	if duty >= 1 {
		_ = dev.SetLowPin(board.PinFan, true)
		time.Sleep(settle)
		return sampleFull(dev, window)
	}

	period := 1.0 / freqHz
	onLen := clocksToLen(period * duty * float64(clk))
	offLen := clocksToLen(period * (1 - duty) * float64(clk))
	one := []byte{
		ftdi.CmdSetLowByte, fanOn, dir,
		ftdi.CmdClockNoData, byte(onLen), byte(onLen >> 8),
		ftdi.CmdSetLowByte, base, dir,
		ftdi.CmdClockNoData, byte(offLen), byte(offLen >> 8),
	}
	// A pure-PWM chunk (~3ms) for settling, and a measure-chunk that ends with a
	// short powered window + tach read.
	n := int(0.003 * freqHz)
	if n < 1 {
		n = 1
	}
	chunk := repeat(one, n)
	settleLen := clocksToLen(0.00002 * float64(clk)) // ~20us powered before the read
	mchunk := append(repeat(one, n),
		ftdi.CmdSetLowByte, fanOn, dir,
		ftdi.CmdClockNoData, byte(settleLen), byte(settleLen>>8),
		ftdi.CmdReadHighByte, ftdi.CmdSendImmediate)

	// Settle at this duty (pure PWM).
	for deadline := time.Now().Add(settle); time.Now().Before(deadline); {
		if dev.WriteRaw(chunk) != nil {
			return 0
		}
	}

	// Measure: stream measure-chunks, one tach byte each, count rising edges.
	_ = dev.Flush()
	edges, prev := 0, false
	for deadline := time.Now().Add(window); time.Now().Before(deadline); {
		if dev.WriteRaw(mchunk) != nil {
			break
		}
		b, err := dev.ReadRaw(1)
		if err != nil || len(b) < 1 {
			continue
		}
		cur := b[0]&(byte(1)<<board.PinTach) != 0
		if cur && !prev {
			edges++
		}
		prev = cur
	}
	return int(float64(edges) / float64(board.TachPulsesPerRev) / window.Seconds() * 60)
}

// sampleFull polls the tach with the fan fully on.
func sampleFull(dev *ftdi.Device, window time.Duration) int {
	edges, prev := 0, false
	for deadline := time.Now().Add(window); time.Now().Before(deadline); {
		cur, err := dev.ReadHighPin(board.PinTach)
		if err != nil {
			break
		}
		if cur && !prev {
			edges++
		}
		prev = cur
	}
	return int(float64(edges) / float64(board.TachPulsesPerRev) / window.Seconds() * 60)
}

func repeat(b []byte, n int) []byte {
	out := make([]byte, 0, len(b)*n)
	for i := 0; i < n; i++ {
		out = append(out, b...)
	}
	return out
}

func clocksToLen(clocks float64) int {
	l := int(clocks/8) - 1
	if l < 0 {
		l = 0
	}
	if l > 0xFFFF {
		l = 0xFFFF
	}
	return l
}
