// Command paneld is the per-node daemon: it reads this router's own metrics,
// renders its Beacon screen, drives the fan from temperature, and keeps the
// panel alive across USB hiccups. One instance runs on each node, configured for
// that node's role.
//
//	paneld -role gateway  -name kitchen     -ip 10.0.0.1  -iface eth1 -ping 1.1.1.1 -dhcp -wifi -fan
//	paneld -role extender -name "living room" -ip 10.0.0.11 -iface eth0 -ping 10.0.0.1 -wifiiface phy0-ap0 -wifi -fan
//	paneld -mock -role gateway -name kitchen -ip 10.0.0.1   # dev on a laptop
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kaievns/sunny-side-up/paneld/internal/lcd"
	"github.com/kaievns/sunny-side-up/paneld/internal/metric"
	"github.com/kaievns/sunny-side-up/paneld/internal/panel"
	"github.com/kaievns/sunny-side-up/paneld/internal/ui"
)

func main() {
	role := flag.String("role", "gateway", "node role: gateway | extender | homelab")
	name := flag.String("name", "node", "location name shown in the top row")
	ip := flag.String("ip", "", "node IP shown in the top row")
	iface := flag.String("iface", "", "network interface for throughput (WAN for gateway, uplink for nodes)")
	wifiIface := flag.String("wifiiface", "", "wifi interface for client counts")
	ping := flag.String("ping", "", "latency probe target (public IP for gateway, gateway IP for nodes)")
	dhcp := flag.Bool("dhcp", false, "count DHCP leases as clients (gateway)")
	hosts := flag.String("hosts", "", "comma-separated homelab hosts to health-check")
	hasWifi := flag.Bool("wifi", false, "node has a wifi module (show wifi temp)")
	hasFan := flag.Bool("fan", false, "node has the fan board (control + show fan)")
	pingWarn := flag.Float64("ping-warn", 0, "latency (ms) above which the node is degraded (0 = role default)")
	tempWarn := flag.Float64("temp-warn", 80, "temperature (°C) above which the node is degraded")
	fanOnC := flag.Float64("fan-on", 65, "turn the fan on at/above this temp (°C)")
	fanOffC := flag.Float64("fan-off", 55, "turn the fan off below this temp (°C, hysteresis)")

	mock := flag.Bool("mock", false, "use mock data (for a dev machine with no router metrics)")
	interval := flag.Duration("interval", 2*time.Second, "refresh interval")
	clockHz := flag.Int("clock", 15_000_000, "SPI clock in Hz")
	rotate := flag.Int("rotate", 270, "display rotation")
	invert := flag.Bool("invert", false, "display inversion")
	bgr := flag.Bool("bgr", false, "BGR color order")
	flag.Parse()

	node := metric.Node{
		Role:       parseRole(*role),
		Name:       *name,
		IP:         *ip,
		Iface:      *iface,
		WifiIface:  *wifiIface,
		DHCPLeases: *dhcp,
		PingTarget: *ping,
		Hosts:      splitHosts(*hosts),
		HasWifi:    *hasWifi,
		HasFan:     *hasFan,
		PingWarnMs: *pingWarn,
		TempWarnC:  *tempWarn,
	}
	if node.PingWarnMs == 0 {
		node.PingWarnMs = defaultPingWarn(node.Role)
	}

	var provider metric.Provider = metric.NewLocalProvider(node)
	if *mock {
		provider = metric.NewMockProvider(node)
	}

	cfg := panel.Config{ClockHz: *clockHz, LCD: lcd.Options{Rotation: *rotate, Invert: *invert, BGR: *bgr}}

	if err := run(node, provider, cfg, *interval, *fanOnC, *fanOffC); err != nil {
		log.Fatalf("paneld: %v", err)
	}
}

func run(node metric.Node, provider metric.Provider, cfg panel.Config, interval time.Duration, fanOnC, fanOffC float64) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Fan on/off state, kept across reconnects. This 3-wire fan can't be
	// speed-controlled by chopping its supply (it only runs at full), so control
	// is hysteresis on/off and the tach is used to verify it actually spins.
	fanOn := false

	return panel.Supervise(ctx, cfg, func(ctx context.Context, p *panel.Panel) error {
		fb := p.NewFramebuffer()
		lit := false
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			sample, err := provider.Read(ctx)
			if err != nil {
				return err
			}

			scr := metric.Build(node, sample)
			scr.Clock = time.Now().Format("15:04")

			if node.HasFan {
				// The fan triggers on the SoC temperature. (The wifi module
				// idles hotter and would dominate a max() rule; the fan cools
				// the whole board regardless.) If the sensor read ever fails,
				// fail safe: run the fan.
				hot := sample.CPUTempC
				if math.IsNaN(hot) {
					fanOn = true
				} else if !fanOn && hot >= fanOnC {
					fanOn = true
				} else if fanOn && hot < fanOffC {
					fanOn = false
				}
				if fanOn {
					// MeasureTach powers the fan and reads real RPM - closed loop:
					// if it's commanded on but not spinning, we can see it.
					t, terr := p.Fan.MeasureTach(800 * time.Millisecond)
					if terr != nil {
						return terr
					}
					if t.RPM > 200 {
						scr.Fan = fmt.Sprintf("%d", t.RPM)
					} else {
						scr.Fan = "stall"
					}
				} else {
					if err := p.Fan.SetOn(false); err != nil {
						return err
					}
					scr.Fan = "off"
				}
			}

			ui.Render(fb.RGBA, scr)
			if err := p.Blit(fb); err != nil {
				return err
			}
			if !lit {
				if err := p.Backlight(true); err != nil {
					return err
				}
				lit = true
				log.Printf("paneld: %s (%s) live", node.Name, roleName(node.Role))
			}

			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	})
}

func parseRole(s string) ui.Role {
	switch strings.ToLower(s) {
	case "extender":
		return ui.RoleExtender
	case "homelab":
		return ui.RoleHomelab
	default:
		return ui.RoleGateway
	}
}

func roleName(r ui.Role) string {
	switch r {
	case ui.RoleExtender:
		return "extender"
	case ui.RoleHomelab:
		return "homelab"
	default:
		return "gateway"
	}
}

func defaultPingWarn(r ui.Role) float64 {
	if r == ui.RoleGateway {
		return 80 // ISP latency
	}
	return 15 // a wired uplink to the gateway should be well under this
}

func splitHosts(s string) []string {
	var out []string
	for _, h := range strings.Split(s, ",") {
		if h = strings.TrimSpace(h); h != "" {
			out = append(out, h)
		}
	}
	return out
}

