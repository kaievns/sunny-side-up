// Package fan drives the 3-wire fan on the lcd-panel board: it switches the
// low-side MOSFET gate (ADBUS7) on/off and measures RPM from the tachometer
// (ACBUS1) by polling.
//
// Wiring reminder (from the schematic): the fan's +5V is permanent and the
// MOSFET interrupts the ground return, so the tach signal is only valid while
// the gate is ON. Always turn the fan on before measuring RPM.
package fan

import (
	"time"

	"github.com/kaievns/sunny-side-up/paneld/internal/board"
	"github.com/kaievns/sunny-side-up/paneld/internal/ftdi"
)

// Fan controls the fan MOSFET and reads the tachometer via the FT232H.
type Fan struct {
	dev *ftdi.Device
}

// New returns a Fan bound to the given device.
func New(dev *ftdi.Device) *Fan {
	return &Fan{dev: dev}
}

// SetOn drives the MOSFET gate. High = fan powered.
func (f *Fan) SetOn(on bool) error {
	return f.dev.SetLowPin(board.PinFan, on)
}

// Tach is the result of one tachometer measurement window.
type Tach struct {
	RPM         int           // estimated revolutions per minute
	RisingEdges int           // low->high transitions counted (= tach pulses)
	Samples     int           // how many times the pin was sampled
	Window      time.Duration // measurement window actually elapsed
	SawToggle   bool          // true if the tach line changed at all
}

// MeasurePWM runs the fan at the given duty (software PWM at the given period)
// for window, sampling the tach only during the powered on-phases, and returns
// the true RPM. Because the fan spins continuously, the pulses caught during the
// on-time (a fraction = duty of the total) still reflect the actual rotation
// rate: RPM = edges/pulsesPerRev / on-seconds * 60. This is how we tie a duty to
// a real measured speed.
func (f *Fan) MeasurePWM(duty float64, window, period time.Duration) Tach {
	if duty <= 0 {
		_ = f.SetOn(false)
		time.Sleep(window)
		return Tach{}
	}
	onDur := time.Duration(float64(period) * duty)
	if duty >= 1 {
		onDur = period
	}

	var edges, samples int
	var onTotal time.Duration
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		// Powered phase: sample the tach and count rising edges.
		_ = f.dev.SetLowPin(board.PinFan, true)
		onStart := time.Now()
		prev, _ := f.dev.ReadHighPin(board.PinTach) // baseline, not an edge
		for time.Since(onStart) < onDur {
			cur, err := f.dev.ReadHighPin(board.PinTach)
			if err != nil {
				break
			}
			samples++
			if cur && !prev {
				edges++
			}
			prev = cur
		}
		onTotal += time.Since(onStart)

		if duty < 1 {
			_ = f.dev.SetLowPin(board.PinFan, false)
			time.Sleep(period - onDur)
		}
	}
	_ = f.dev.SetLowPin(board.PinFan, false)

	rpm := 0
	if onTotal > 0 {
		revs := float64(edges) / float64(board.TachPulsesPerRev)
		rpm = int(revs / onTotal.Seconds() * 60)
	}
	return Tach{RPM: rpm, RisingEdges: edges, Samples: samples, Window: window, SawToggle: edges > 0}
}

// MeasureTach turns the fan on (tach is only valid when powered) and polls the
// tach pin for the given window, counting pulses to estimate RPM.
//
// This is a polling measurement: each sample is a USB round-trip (~1ms with the
// latency timer at 1ms), so it reliably resolves typical fan speeds but will
// under-count very fast fans (>~7000 RPM). For bring-up it answers the real
// question: are we getting a tach signal, and roughly how fast.
func (f *Fan) MeasureTach(window time.Duration) (Tach, error) {
	if err := f.SetOn(true); err != nil {
		return Tach{}, err
	}
	// Let the fan spin up and the tach line settle before counting.
	time.Sleep(150 * time.Millisecond)

	prev, err := f.dev.ReadHighPin(board.PinTach)
	if err != nil {
		return Tach{}, err
	}

	var (
		rising  int
		samples = 1
		toggled bool
	)
	start := time.Now()
	deadline := start.Add(window)
	for time.Now().Before(deadline) {
		cur, err := f.dev.ReadHighPin(board.PinTach)
		if err != nil {
			return Tach{}, err
		}
		samples++
		if cur != prev {
			toggled = true
			if cur && !prev { // low -> high edge = one tach pulse
				rising++
			}
			prev = cur
		}
	}
	elapsed := time.Since(start)

	revs := float64(rising) / float64(board.TachPulsesPerRev)
	rpm := 0
	if elapsed > 0 {
		rpm = int(revs / elapsed.Seconds() * 60.0)
	}

	return Tach{
		RPM:         rpm,
		RisingEdges: rising,
		Samples:     samples,
		Window:      elapsed,
		SawToggle:   toggled,
	}, nil
}
