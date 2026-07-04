// Command screens renders the "Beacon" node screens with mock data. It writes
// PNG previews (so you can eyeball the design without the panel) and, unless
// -no-lcd is set, pushes them to the LCD over the FT232H.
//
//	go run ./cmd/screens                 # PNGs + show 'kitchen' on the panel
//	go run ./cmd/screens -cycle          # rotate through all 8 screens
//	go run ./cmd/screens -screen garage-deg
//	go run ./cmd/screens -no-lcd         # just write PNGs
package main

import (
	"context"
	"flag"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/kaievns/sunny-side-up/paneld/internal/lcd"
	"github.com/kaievns/sunny-side-up/paneld/internal/panel"
	"github.com/kaievns/sunny-side-up/paneld/internal/ui"
)

type named struct {
	key string
	s   ui.Screen
}

// colorPage is the dark backdrop for the PNG contact sheet.
var colorPage = color.RGBA{0x16, 0x18, 0x1a, 0xff}

func main() {
	pick := flag.String("screen", "kitchen", "screen key to show on the panel")
	cycle := flag.Bool("cycle", false, "cycle through all screens on the panel")
	interval := flag.Duration("interval", 3*time.Second, "cycle interval")
	pngDir := flag.String("png", defaultPNGDir(), "directory for PNG previews (empty to skip)")
	noLCD := flag.Bool("no-lcd", false, "only write PNG previews; don't touch the panel")

	clockHz := flag.Int("clock", 15_000_000, "SPI clock in Hz")
	rotate := flag.Int("rotate", 270, "display rotation (90/270 landscape)")
	invert := flag.Bool("invert", false, "display inversion (this panel wants it off)")
	bgr := flag.Bool("bgr", false, "BGR color order")
	vid := flag.Int("vid", 0, "USB vendor id (0 = default)")
	pid := flag.Int("pid", 0, "USB product id (0 = default)")
	flag.Parse()

	screens := mockScreens()

	if *pngDir != "" {
		if err := writePreviews(screens, *pngDir); err != nil {
			log.Fatalf("screens: writing previews: %v", err)
		}
		log.Printf("wrote %d PNG previews + contact.png to %s", len(screens), *pngDir)
	}
	if *noLCD {
		return
	}

	cfg := panel.Config{ClockHz: *clockHz, VID: *vid, PID: *pid, LCD: lcd.Options{Rotation: *rotate, Invert: *invert, BGR: *bgr}}
	if err := drive(screens, *pick, *cycle, *interval, cfg); err != nil {
		log.Fatalf("screens: %v", err)
	}
}

// drive shows either one screen (held, with a live-scrolling sparkline) or all
// of them in a cycle, reconnecting automatically if the USB link drops.
func drive(screens []named, pick string, cycle bool, interval time.Duration, cfg panel.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cycle {
		log.Printf("cycling %d screens every %s (Ctrl-C to stop)", len(screens), interval)
	} else if idx(screens, pick) < 0 {
		log.Printf("unknown screen %q; showing %q. available: %s", pick, screens[0].key, keys(screens))
	} else {
		log.Printf("showing %q (Ctrl-C to stop)", pick)
	}

	return panel.Supervise(ctx, cfg, func(ctx context.Context, p *panel.Panel) error {
		fb := p.NewFramebuffer()
		show := func(s ui.Screen) error {
			ui.Render(fb.RGBA, s)
			return p.Blit(fb)
		}

		if cycle {
			if err := show(screens[0].s); err != nil {
				return err
			}
			if err := p.Backlight(true); err != nil {
				return err
			}
			i := 0
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-t.C:
					i = (i + 1) % len(screens)
					log.Printf("  %s", screens[i].key)
					if err := show(screens[i].s); err != nil {
						return err
					}
				}
			}
		}

		// Single screen, held with a gently scrolling sparkline so it looks alive.
		sel := screens[0].s
		if j := idx(screens, pick); j >= 0 {
			sel = screens[j].s
		}
		if err := show(sel); err != nil {
			return err
		}
		if err := p.Backlight(true); err != nil {
			return err
		}
		spark := append([]float64(nil), sel.Spark...)
		t := time.NewTicker(700 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
				spark = append(spark[1:], nextSample(spark))
				sel.Spark = spark
				if err := show(sel); err != nil {
					return err
				}
			}
		}
	})
}

// idx returns the index of the screen with the given key, or -1.
func idx(screens []named, key string) int {
	for i, n := range screens {
		if n.key == key {
			return i
		}
	}
	return -1
}

// ---- mock data ---------------------------------------------------------------

func mockScreens() []named {
	return []named{
		{"kitchen", ui.Screen{
			Role: ui.RoleGateway, Health: ui.OK,
			Location: "kitchen", IP: "10.0.0.1", Clock: "14:32",
			Hero: "521", HeroUnit: "Mb/s", Corner: [2]string{"↑ 68 Mb/s", "46 clients"},
			Aux: "6.4", AuxUnit: "ms", AuxLabel: "dns 4.2 · loss 0%",
			StatusL: "internet ok", CPU: "52°", Wifi: "48°", Fan: "3200", Up: "14d",
			Spark: wave(64, 0.62, 0.22, 1),
		}},
		{"gw-down", ui.Screen{
			Role: ui.RoleGateway, Health: ui.Down,
			Location: "kitchen", IP: "10.0.0.1", Clock: "14:32",
			Hero: "0", HeroUnit: "Mb/s", Corner: [2]string{"↑ 0 Mb/s", "46 clients"},
			Aux: "FAIL", AuxLabel: "isp · dns down",
			Fault: "WAN down · no internet", CPU: "51°", Wifi: "47°", Fan: "3200", Up: "14d",
			Spark: dropout(64, 0.55, 0.2, 2),
		}},
		{"living", ui.Screen{
			Role: ui.RoleExtender, Health: ui.OK,
			Location: "living room", IP: "10.0.0.11", Clock: "14:32",
			Hero: "149", HeroUnit: "Mb/s", Corner: [2]string{"↑ 42 Mb/s", "7 clients"},
			Aux: "0.5", AuxUnit: "ms", AuxLabel: "→ gateway · 1G",
			StatusL: "uplink ok", CPU: "47°", Wifi: "44°", Fan: "2600", Up: "9d",
			Spark: wave(64, 0.5, 0.16, 3),
		}},
		{"ext-lost", ui.Screen{
			Role: ui.RoleExtender, Health: ui.Down,
			Location: "living room", IP: "10.0.0.11", Clock: "14:32",
			Hero: "0", HeroUnit: "Mb/s", Corner: [2]string{"↑ 0 Mb/s", "7 clients"},
			Aux: "LOST", AuxLabel: "no route to gateway",
			Fault: "uplink lost · retrying", CPU: "46°", Wifi: "43°", Fan: "2600", Up: "9d",
			Spark: dropout(64, 0.48, 0.16, 4),
		}},
		{"garage", ui.Screen{
			Role: ui.RoleExtender, Health: ui.OK,
			Location: "garage", IP: "10.0.0.12", Clock: "14:32",
			Hero: "168", HeroUnit: "Mb/s", Corner: [2]string{"↑ 51 Mb/s", "7 clients"},
			Aux: "0.8", AuxUnit: "ms", AuxLabel: "→ gateway · 1G",
			StatusL: "uplink ok", CPU: "49°", Wifi: "45°", Fan: "2800", Up: "9d",
			Spark: wave(64, 0.52, 0.18, 5),
		}},
		{"garage-deg", ui.Screen{
			Role: ui.RoleExtender, Health: ui.Degraded,
			Location: "garage", IP: "10.0.0.12", Clock: "14:32",
			Hero: "88", HeroUnit: "Mb/s", Corner: [2]string{"↑ 6 Mb/s", "5 clients"},
			Aux: "12", AuxUnit: "ms", AuxLabel: "→ gateway · 100M",
			Fault: "uplink degraded · 100M", CPU: "58°", Wifi: "61°", Fan: "4200", Up: "9d",
			Spark: noisyLow(64, 6),
		}},
		{"office", ui.Screen{
			Role: ui.RoleHomelab, Health: ui.OK,
			Location: "office", IP: "10.0.40.1", Clock: "14:32",
			Hero: "384", HeroUnit: "Mb/s", Corner: [2]string{"↑ 86 Mb/s", "12 clients"},
			Aux: "0.6", AuxUnit: "ms", AuxLabel: "→ gateway · 1G",
			StatusL: "14/14 hosts up", CPU: "55°", Wifi: "46°", Fan: "3400", Up: "21d",
			Spark: wave(64, 0.58, 0.2, 7),
		}},
		{"office-alert", ui.Screen{
			Role: ui.RoleHomelab, Health: ui.Degraded,
			Location: "office", IP: "10.0.40.1", Clock: "14:32",
			Hero: "372", HeroUnit: "Mb/s", Corner: [2]string{"↑ 84 Mb/s", "12 clients"},
			Aux: "0.6", AuxUnit: "ms", AuxLabel: "→ gateway · 1G", AuxGreen: true,
			Fault: "2 hosts unreachable", CPU: "56°", Wifi: "47°", Fan: "3600", Up: "21d",
			Spark: wave(64, 0.56, 0.18, 8),
		}},
	}
}

func wave(n int, base, amp float64, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	p1, p2 := r.Float64()*6.28, r.Float64()*6.28
	out := make([]float64, n)
	for i := range out {
		t := float64(i) / float64(n-1)
		v := base + amp*(0.6*math.Sin(t*6.28*1.6+p1)+0.4*math.Sin(t*6.28*3.3+p2))
		v += (r.Float64() - 0.5) * 0.05
		out[i] = clamp01(v)
	}
	return smooth(out)
}

func dropout(n int, base, amp float64, seed int64) []float64 {
	w := wave(n, base, amp, seed)
	k := int(float64(n) * 0.6)
	for i := k; i < n; i++ {
		f := float64(i-k) / float64(n-k)
		w[i] = clamp01(w[i] * (1 - f))
	}
	for i := int(float64(n) * 0.82); i < n; i++ {
		w[i] = 0.015
	}
	return w
}

func noisyLow(n int, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	for i := range out {
		out[i] = clamp01(0.18 + (r.Float64()-0.5)*0.28)
	}
	return smooth(out)
}

func smooth(v []float64) []float64 {
	out := make([]float64, len(v))
	for i := range v {
		sum, cnt := 0.0, 0.0
		for j := i - 1; j <= i+1; j++ {
			if j >= 0 && j < len(v) {
				sum += v[j]
				cnt++
			}
		}
		out[i] = sum / cnt
	}
	return out
}

func nextSample(prev []float64) float64 {
	last := prev[len(prev)-1]
	return clamp01(last + (rand.Float64()-0.5)*0.18)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ---- PNG previews ------------------------------------------------------------

func writePreviews(screens []named, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	imgs := make([]*image.RGBA, len(screens))
	for i, n := range screens {
		img := ui.NewImage()
		ui.Render(img, n.s)
		imgs[i] = img
		if err := writePNG(filepath.Join(dir, n.key+".png"), img); err != nil {
			return err
		}
	}
	return writePNG(filepath.Join(dir, "contact.png"), contactSheet(imgs))
}

// contactSheet lays the screens out 2 columns x N rows at 2x, on a dark page.
func contactSheet(imgs []*image.RGBA) *image.RGBA {
	const scale, gap, cols = 2, 16, 2
	cw, ch := ui.W*scale, ui.H*scale
	rows := (len(imgs) + cols - 1) / cols
	pageW := gap + cols*(cw+gap)
	pageH := gap + rows*(ch+gap)
	page := image.NewRGBA(image.Rect(0, 0, pageW, pageH))
	for y := 0; y < pageH; y++ {
		for x := 0; x < pageW; x++ {
			page.SetRGBA(x, y, colorPage)
		}
	}
	for i, img := range imgs {
		col, row := i%cols, i/cols
		x0 := gap + col*(cw+gap)
		y0 := gap + row*(ch+gap)
		dst := image.Rect(x0, y0, x0+cw, y0+ch)
		xdraw.NearestNeighbor.Scale(page, dst, img, img.Bounds(), xdraw.Over, nil)
	}
	return page
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func keys(screens []named) string {
	s := ""
	for i, n := range screens {
		if i > 0 {
			s += " "
		}
		s += n.key
	}
	return s
}

func defaultPNGDir() string {
	return "/private/tmp/claude-501/-Users-kai-projects-sunny-side-up/6658d08c-502b-4e65-9123-85e4a411381d/scratchpad/screens"
}
