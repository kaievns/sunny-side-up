// Command fancal characterises the actual fan: it sweeps PWM duty and measures
// real RPM via the tach, and tests whether the fan needs a kickstart to spin up
// from a stop. Use the numbers it prints to set a fan curve that actually spins.
//
//	fancal            # sweep + cold-start tests
//	fancal -hz 25     # try a different PWM frequency
package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/kaievns/sunny-side-up/paneld/internal/board"
	"github.com/kaievns/sunny-side-up/paneld/internal/fan"
	"github.com/kaievns/sunny-side-up/paneld/internal/ftdi"
)

func main() {
	clockHz := flag.Int("clock", 15_000_000, "SPI clock in Hz")
	hz := flag.Float64("hz", 50, "PWM frequency to test")
	win := flag.Duration("win", 2*time.Second, "measurement window per step")
	flag.Parse()

	dev, err := ftdi.Open(0, 0)
	if err != nil {
		log.Fatalf("fancal: %v", err)
	}
	defer dev.Close()
	if err := dev.EnableMPSSE(*clockHz, board.LowIdle, board.LowDirMask); err != nil {
		log.Fatalf("fancal: %v", err)
	}
	f := fan.New(dev)
	defer f.SetOn(false)
	period := time.Duration(float64(time.Second) / *hz)

	fmt.Printf("fan calibration @ %.0f Hz PWM, %s/step\n\n", *hz, *win)

	fmt.Println("spin up (100%% for 2s), then sweep down while already spinning:")
	f.MeasurePWM(1.0, 2*time.Second, period) // kickstart / spin up
	fmt.Printf("  %-6s %-7s %s\n", "duty", "RPM", "edges")
	for _, d := range []int{100, 90, 80, 70, 60, 55, 50, 45, 40, 35, 30, 25} {
		t := f.MeasurePWM(float64(d)/100, *win, period)
		fmt.Printf("  %-5d%% %-7d %d\n", d, t.RPM, t.RisingEdges)
	}

	fmt.Println("\ncold-start WITHOUT kickstart (stop 3s, then jump to 50%):")
	f.SetOn(false)
	time.Sleep(3 * time.Second)
	t := f.MeasurePWM(0.5, *win, period)
	fmt.Printf("  50%% cold -> %d RPM  %s\n", t.RPM, verdict(t.RPM))

	fmt.Println("\ncold-start WITH kickstart (stop 3s, 100% for 1s, then 50%):")
	f.SetOn(false)
	time.Sleep(3 * time.Second)
	f.SetOn(true)
	time.Sleep(1 * time.Second)
	t = f.MeasurePWM(0.5, *win, period)
	fmt.Printf("  50%% after kick -> %d RPM  %s\n", t.RPM, verdict(t.RPM))

	f.SetOn(false)
	fmt.Println("\ndone (fan off)")
}

func verdict(rpm int) string {
	if rpm > 300 {
		return "SPINNING"
	}
	return "stalled"
}
