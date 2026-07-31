// Command framedump is a temporary bring-up probe: it performs one cycle of
// exactly what the daemon does on a v2 board - read local metrics, build the
// screen, render, tiny transactions, blit - and saves the rendered frame to a
// PNG so the frame content and the LCD transport can be told apart.
package main

import (
	"context"
	"flag"
	"fmt"
	"image/color"
	"image/png"
	"log"
	"os"
	"time"

	"github.com/kaievns/sunny-side-up/paneld/internal/lcd"
	"github.com/kaievns/sunny-side-up/paneld/internal/metric"
	"github.com/kaievns/sunny-side-up/paneld/internal/panel"
	"github.com/kaievns/sunny-side-up/paneld/internal/tiny"
	"github.com/kaievns/sunny-side-up/paneld/internal/ui"
)

func main() {
	out := flag.String("out", "/tmp/frame.png", "where to save the rendered frame")
	rotate := flag.Int("rotate", 90, "rotation")
	noTiny := flag.Bool("notiny", false, "skip all ATtiny traffic (isolate the LCD path)")
	mock := flag.Bool("mock", false, "mock provider (no syscalls) instead of local metrics")
	delay := flag.Duration("sleep", 0, "extra sleep before the blit")
	fill := flag.String("fill", "", "skip metrics/render: fill the frame with R,G,B (e.g. 255,0,0) and blit")
	pattern := flag.String("pattern", "", "skip metrics/render: blit a test pattern (checker | bars | render)")
	burn := flag.Duration("burn", 0, "spin the CPU (no allocations) before the blit")
	churn := flag.Int("churn", 0, "allocate this many MB of garbage before the blit")
	clockHz := flag.Int("clock", 15_000_000, "SPI clock in Hz")
	flag.Parse()

	node := metric.Node{
		Role: ui.RoleHomelab, Name: "office", IP: "192.168.1.1",
		Iface: "eth1", PingTarget: "172.20.1.254",
		DHCPLeases: true, HasFan: true, PingWarnMs: 15, TempWarnC: 80,
	}
	var provider metric.Provider = metric.NewLocalProvider(node)
	if *mock {
		provider = metric.NewMockProvider(node)
	}

	p, err := panel.Open(panel.Config{ClockHz: *clockHz, LCD: lcd.Options{Rotation: *rotate}})
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer p.Close()

	var tc *tiny.Client
	if !*noTiny {
		if err := p.Device().ConfigureHighByte(0x01, 0x01); err != nil {
			log.Fatalf("high byte: %v", err)
		}
		tc = tiny.New(p.Device(), 15_000_000)
		if v, f, err := tc.Ping(); err != nil {
			log.Fatalf("ping: %v", err)
		} else {
			log.Printf("ping ok: fw v%d flags %v", v, f)
		}
	}

	if *burn > 0 {
		log.Printf("burning CPU for %v", *burn)
		deadline := time.Now().Add(*burn)
		x := 0
		for time.Now().Before(deadline) {
			x++
		}
		_ = x
	}
	if *churn > 0 {
		log.Printf("churning %d MB of allocations", *churn)
		for i := 0; i < *churn*16; i++ {
			s := make([]byte, 64*1024)
			s[0] = byte(i)
			_ = s
		}
	}

	if *pattern == "render" {
		// ui.Render with static data, straight to blit - no metrics, no PNG.
		scr := ui.Screen{
			Role: ui.RoleHomelab, Health: ui.OK, Location: "office", IP: "192.168.1.1",
			Clock: "13:37", Hero: "384", HeroUnit: "Mb/s",
			Corner: [2]string{"↑ 86 Mb/s", "12 clients"},
			Aux:    "0.6", AuxUnit: "ms", AuxLabel: "→ gateway · 1G",
			Spark:  []float64{0.2, 0.5, 0.3, 0.6, 0.4, 0.5, 0.2, 0.4},
			Ifaces: []ui.Iface{{Label: "WAN", State: ui.OK}, {Label: "LAN", State: ui.OK}},
			CPU:    "55°", Fan: "3200", Up: "26m",
		}
		fb := p.NewFramebuffer()
		ui.Render(fb.RGBA, scr)
		if err := p.Blit(fb); err != nil {
			log.Fatalf("blit: %v", err)
		}
		if junk, err := p.Device().DrainRX(4096); err != nil {
			log.Printf("drain err: %v", err)
		} else {
			log.Printf("post-blit RX drain: %d bytes % x", len(junk), junk[:min(len(junk), 64)])
		}
		fmt.Println("rendered static screen + blitted - look at the glass")
		return
	}

	if *pattern != "" {
		fb := p.NewFramebuffer()
		for y := 0; y < fb.H; y++ {
			for x := 0; x < fb.W; x++ {
				var c color.RGBA
				switch *pattern {
				case "checker": // worst-case bit toggling per pixel
					if (x+y)%2 == 0 {
						c = color.RGBA{255, 255, 255, 255}
					} else {
						c = color.RGBA{0, 0, 0, 255}
					}
				default: // bars
					cols := []color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 0, 255},
						{0, 255, 255, 255}, {255, 0, 255, 255}, {255, 255, 255, 255}, {60, 60, 60, 255}}
					c = cols[x*8/fb.W]
				}
				fb.SetRGBA(x, y, c)
			}
		}
		if err := p.Blit(fb); err != nil {
			log.Fatalf("blit: %v", err)
		}
		fmt.Println("blitted pattern", *pattern, "- look at the glass")
		return
	}

	if *fill != "" {
		var r, g, b uint8
		if _, err := fmt.Sscanf(*fill, "%d,%d,%d", &r, &g, &b); err != nil {
			log.Fatalf("bad -fill: %v", err)
		}
		fb := p.NewFramebuffer()
		fb.Fill(color.RGBA{r, g, b, 255})
		if *delay > 0 {
			log.Printf("sleeping %v before blit", *delay)
			time.Sleep(*delay)
		}
		if err := p.Blit(fb); err != nil {
			log.Fatalf("blit: %v", err)
		}
		fmt.Println("filled + blitted", *fill, "- look at the glass")
		return
	}

	sample, err := provider.Read(context.Background())
	if err != nil {
		log.Fatalf("provider read: %v", err)
	}
	log.Printf("sample: cpu=%.1fC down=%.1f up=%.1f ping=%.1fms clients=%d up=%ds",
		sample.CPUTempC, sample.DownMbps, sample.UpMbps, sample.PingMs, sample.Clients, sample.UptimeSec)

	scr := metric.Build(node, sample)
	scr.Clock = "13:37"
	fb := p.NewFramebuffer()
	log.Printf("fb: %dx%d pix=%d", fb.W, fb.H, len(fb.Pix))
	ui.Render(fb.RGBA, scr)

	if *out != "" {
		// How much ink did the render produce? (sum of non-background pixels)
		ink := 0
		for i := 0; i < len(fb.Pix); i += 4 {
			if fb.Pix[i] > 20 || fb.Pix[i+1] > 20 || fb.Pix[i+2] > 20 {
				ink++
			}
		}
		log.Printf("render ink: %d px non-background (of %d)", ink, len(fb.Pix)/4)

		f, err := os.Create(*out)
		if err != nil {
			log.Fatal(err)
		}
		if err := png.Encode(f, fb.RGBA); err != nil {
			log.Fatal(err)
		}
		f.Close()
		fmt.Println("saved", *out)
	}

	if tc != nil {
		if err := tc.SetFanDuty(0); err != nil {
			log.Fatalf("fan duty: %v", err)
		}
		if rpm, fl, err := tc.Status(); err != nil {
			log.Fatalf("status: %v", err)
		} else {
			log.Printf("status: rpm=%d flags=%v", rpm, fl)
		}
	}

	if *delay > 0 {
		log.Printf("sleeping %v before blit", *delay)
		time.Sleep(*delay)
	}
	if err := p.Blit(fb); err != nil {
		log.Fatalf("blit: %v", err)
	}
	if tc != nil {
		if err := tc.SetBL(220); err != nil {
			log.Fatalf("bl: %v", err)
		}
	}
	fmt.Println("blitted - look at the glass")
}
