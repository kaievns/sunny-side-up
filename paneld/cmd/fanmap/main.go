// Command fanmap maps the fan's real control surface: PWM frequency x duty ->
// measured RPM. It streams MPSSE-timed waveforms (set-gate + clock-delay
// commands executed by the FT232H at its own clock, so the waveform is jitter-
// free) with tach reads EMBEDDED in the waveform at known offsets - so sampling
// is deterministic, doesn't disturb the PWM, and doesn't alias.
//
// Use it to find the frequency regime where duty tracks RPM and the usable duty
// band before stall:
//
//	fanmap                            # default sweep
//	fanmap -freqs 30,120,20000 -duties 100,75,50
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/kaievns/sunny-side-up/paneld/internal/board"
	"github.com/kaievns/sunny-side-up/paneld/internal/ftdi"
)

const (
	sampleEvery = 0.0015 // target tach sample spacing (s)
	guard       = 0.0002 // delay after gate-on before the first in-phase read (s)
	gapThresh   = 0.0026 // max Δt between samples still counted as contiguous (s)
)

func main() {
	clockHz := flag.Int("clock", 15_000_000, "SPI/MPSSE clock in Hz")
	freqsArg := flag.String("freqs", "30,60,120,500,2000,20000", "PWM frequencies (Hz) to test")
	dutiesArg := flag.String("duties", "100,85,75,65,55,45,35", "duty steps (percent) per frequency")
	settle := flag.Duration("settle", 2*time.Second, "settle time per point before measuring")
	measure := flag.Duration("measure", 2*time.Second, "measurement window per point")
	kick := flag.Duration("kick", 2*time.Second, "full-on kickstart before each point (TC642 ~1s, ADI ~2s)")
	flag.Parse()

	freqs := parseList(*freqsArg)
	duties := parseList(*dutiesArg)

	dev, err := ftdi.Open(0, 0)
	if err != nil {
		log.Fatalf("fanmap: %v", err)
	}
	defer dev.Close()
	if err := dev.EnableMPSSE(*clockHz, board.LowIdle, board.LowDirMask); err != nil {
		log.Fatalf("fanmap: %v", err)
	}
	defer dev.SetLowPin(board.PinFan, false)

	clk := dev.ClockHz()
	base := dev.LowByte()
	gateOn := base | 1<<board.PinFan
	gateOff := base &^ (byte(1) << board.PinFan)
	fmt.Printf("fan control map (MPSSE clock %d Hz; settle %s, measure %s, kick %s per point)\n",
		clk, *settle, *measure, *kick)

	// Calibrate the rotor-state probe: what a 350ms full-on RPM read looks like
	// when the fan was already at full speed vs spinning up from a dead stop.
	// SetLowByte (not SetLowPin) everywhere: WriteRaw streams bypass the pin
	// shadow, so pin-level writes could be silently skipped as no-ops.
	_ = dev.SetLowByte(gateOn)
	time.Sleep(2 * time.Second)
	probeFull := probeRPM(dev, gateOn)
	_ = dev.SetLowByte(gateOff)
	time.Sleep(3 * time.Second)
	probeDead := probeRPM(dev, gateOn)
	spinningFloor := (probeFull + probeDead) / 2
	fmt.Printf("probe calibration: already-spinning=%d RPM, from-dead-stop=%d RPM (threshold %d)\n\n",
		probeFull, probeDead, spinningFloor)

	fmt.Printf("%-9s %-6s %-8s %-8s %-8s %s\n", "freq", "duty", "tachRPM", "probe", "samples", "verdict")
	for _, f := range freqs {
		for _, d := range duties {
			// Kickstart: full-on so every point tests MAINTAINING the speed.
			_ = dev.SetLowByte(gateOn)
			time.Sleep(*kick)

			rpm, ns := runPoint(dev, base, d/100, f, clk, *settle, *measure)
			// Objective rotor check: immediately hold full-on and read RPM. A
			// fan that was spinning reads near full; a stalled one reads the
			// low spin-up-from-zero signature.
			probe := probeRPM(dev, gateOn)
			verdict := "SPINNING"
			if probe < spinningFloor {
				verdict = "STALLED"
			} else if rpm < 300 {
				verdict = "SPINNING (tach n/a in-pwm)"
			}
			fmt.Printf("%-8.0fHz %-5.0f%% %-8d %-8d %-8d %s\n", f, d, rpm, probe, ns, verdict)
		}
	}
	_ = dev.SetLowByte(gateOff)
	fmt.Println("done (fan off)")
}

// probeRPM holds the gate solid-on (shadow-safe) and fast-polls the tach for
// 350ms.
func probeRPM(dev *ftdi.Device, gateOn byte) int {
	_ = dev.SetLowByte(gateOn)
	const window = 350 * time.Millisecond
	edges, prev := 0, false
	first := true
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		cur, err := dev.ReadHighPin(board.PinTach)
		if err != nil {
			break
		}
		if !first && cur && !prev {
			edges++
		}
		prev, first = cur, false
	}
	return int(float64(edges) / float64(board.TachPulsesPerRev) / window.Seconds() * 60)
}

// runPoint streams PWM at (duty, freq), discards samples during settle, then
// counts tach edges for the measure window. Returns RPM and sample count.
func runPoint(dev *ftdi.Device, base byte, duty, freqHz float64, clk int, settle, measure time.Duration) (int, int) {
	ch := buildChunk(base, board.LowDirMask, duty, freqHz, clk)

	var cnt counter
	var nSamples int
	dbg := true
	settleSec := settle.Seconds()
	elapsed := 0.0
	total := settleSec + measure.Seconds()

	// Depth-2 pipeline keeps the TX FIFO fed so there are no inter-chunk gaps.
	if err := dev.WriteRaw(ch.cmds); err != nil {
		fmt.Printf("  [dbg: prime write err=%v cmdbytes=%d]\n", err, len(ch.cmds))
		return 0, 0
	}
	for elapsed < total {
		if err := dev.WriteRaw(ch.cmds); err != nil {
			fmt.Printf("  [dbg: write err=%v cmdbytes=%d]\n", err, len(ch.cmds))
			break
		}
		b, err := dev.ReadRaw(len(ch.times))
		if err != nil || len(b) != len(ch.times) {
			// Short read: drop this chunk's samples and re-baseline the edge
			// counter, but do NOT purge the chip (that would corrupt the
			// in-flight waveform and desync every following chunk).
			if dbg {
				fmt.Printf("  [dbg: read err=%v got=%d want=%d cmdbytes=%d]\n",
					err, len(b), len(ch.times), len(ch.cmds))
				dbg = false
			}
			cnt.reset()
		} else if elapsed >= settleSec {
			for i, t := range ch.times {
				level := b[i]&(1<<board.PinTach) != 0
				cnt.feed(elapsed+t, level)
				nSamples++
			}
		}
		elapsed += ch.dur
	}
	// Drain the final in-flight chunk.
	_, _ = dev.ReadRaw(len(ch.times))

	return cnt.rpm(), nSamples
}

// chunk is a prebuilt slice of MPSSE commands producing PWM for dur seconds,
// with tach reads at the given offsets.
type chunk struct {
	cmds  []byte
	times []float64
	dur   float64
}

// buildChunk constructs one streamable chunk of waveform. base is the current
// low-byte state; every non-fan bit is preserved in both gate states.
func buildChunk(base, dir byte, duty, freqHz float64, clk int) chunk {
	gateOn := base | 1<<board.PinFan
	gateOff := base &^ (1 << board.PinFan)
	var c chunk

	// Full-on: no switching, just periodic reads.
	if duty >= 0.995 {
		c.cmds = append(c.cmds, ftdi.CmdSetLowByte, gateOn, dir)
		for c.dur < 0.02 {
			c.cmds, c.dur = appendDelay(c.cmds, c.dur, sampleEvery, clk)
			c.cmds = append(c.cmds, ftdi.CmdReadHighByte)
			c.times = append(c.times, c.dur)
		}
		c.cmds = append(c.cmds, ftdi.CmdSendImmediate)
		return c
	}

	period := 1 / freqHz
	onT := period * duty
	highFreq := period < 0.001
	readEvery := 1
	if highFreq {
		readEvery = int(math.Ceil(sampleEvery / period))
	}
	// Keep chunks small (~1.2KB of commands) so each write comfortably fits the
	// chip's 1KB FIFO + kernel buffering; large chunks proved unreliable.
	target := math.Max(0.005, 2*period)
	nextSample := guard

	for cycle := 0; c.dur < target; cycle++ {
		// ON phase
		c.cmds = append(c.cmds, ftdi.CmdSetLowByte, gateOn, dir)
		phaseEnd := c.dur + onT
		if highFreq {
			if cycle%readEvery == 0 {
				// Let the powered rail settle ~40us before sampling, so we
				// don't read the tach mid switching-transient.
				st := math.Min(0.00004, onT/3)
				c.cmds, c.dur = appendDelay(c.cmds, c.dur, st, clk)
				c.cmds = append(c.cmds, ftdi.CmdReadHighByte)
				c.times = append(c.times, c.dur)
			}
		} else {
			for nextSample < phaseEnd-guard {
				if nextSample < c.dur+guard {
					nextSample = c.dur + guard
				}
				c.cmds, c.dur = appendDelay(c.cmds, c.dur, nextSample-c.dur, clk)
				c.cmds = append(c.cmds, ftdi.CmdReadHighByte)
				c.times = append(c.times, c.dur)
				nextSample += sampleEvery
			}
		}
		if rem := phaseEnd - c.dur; rem > 0 {
			c.cmds, c.dur = appendDelay(c.cmds, c.dur, rem, clk)
		}
		// OFF phase
		c.cmds = append(c.cmds, ftdi.CmdSetLowByte, gateOff, dir)
		c.cmds, c.dur = appendDelay(c.cmds, c.dur, period-onT, clk)
		if !highFreq && nextSample < c.dur {
			nextSample = c.dur + guard
		}
	}
	c.cmds = append(c.cmds, ftdi.CmdSendImmediate)
	return c
}

// appendDelay emits 0x8F clock-delay commands totalling ~sec seconds and
// returns the updated command slice and cursor advanced by the ACTUAL delay.
func appendDelay(cmds []byte, cursor, sec float64, clk int) ([]byte, float64) {
	clocks := sec * float64(clk)
	for clocks >= 8 {
		n := int(clocks / 8) // units of 8 clocks
		if n > 0x10000 {
			n = 0x10000
		}
		l := n - 1
		cmds = append(cmds, ftdi.CmdClockNoData, byte(l), byte(l>>8))
		cursor += float64(n*8) / float64(clk)
		clocks -= float64(n * 8)
	}
	return cmds, cursor
}

// counter accumulates tach rising edges over contiguously-sampled time. Samples
// separated by more than gapThresh (an off-phase gap) don't contribute observed
// time, and the edge across the gap is discarded - keeping the estimate
// unbiased: edges are uniform in time, so edges/observed-time = tach frequency.
type counter struct {
	have  bool
	prevL bool
	prevT float64
	edges int
	obs   float64
}

func (c *counter) feed(t float64, level bool) {
	if c.have {
		if dt := t - c.prevT; dt > 0 && dt <= gapThresh {
			c.obs += dt
			if level && !c.prevL {
				c.edges++
			}
		}
	}
	c.have, c.prevL, c.prevT = true, level, t
}

func (c *counter) reset() { c.have = false }

func (c *counter) rpm() int {
	if c.obs <= 0 {
		return 0
	}
	return int(float64(c.edges) / float64(board.TachPulsesPerRev) / c.obs * 60)
}

func parseList(s string) []float64 {
	var out []float64
	for _, p := range strings.Split(s, ",") {
		if v, err := strconv.ParseFloat(strings.TrimSpace(p), 64); err == nil {
			out = append(out, v)
		}
	}
	return out
}
