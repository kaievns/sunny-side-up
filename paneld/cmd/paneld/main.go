// Command paneld is the per-node daemon: it reads this router's own metrics,
// renders its Beacon screen, drives the fan from temperature, and keeps the
// panel alive across USB hiccups. One instance runs on each node, configured for
// that node's role.
//
//	paneld -role gateway  -name kitchen     -ip 10.0.0.1  -iface eth1 -ping 1.1.1.1 -dhcp -wifi -fan
//	paneld -role extender -name "living room" -ip 10.0.0.11 -iface eth0 -ping 10.0.0.1 -wifiiface phy0-ap0 -wifi -fan
//	paneld -board v2 -role homelab -name office -ip 10.0.40.1 -iface eth0 -ping 10.0.0.1 -hosts nas,pve -fan
//	paneld -mock -role gateway -name kitchen -ip 10.0.0.1   # dev on a laptop
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kaievns/sunny-side-up/paneld/internal/lcd"
	"github.com/kaievns/sunny-side-up/paneld/internal/metric"
	"github.com/kaievns/sunny-side-up/paneld/internal/panel"
	"github.com/kaievns/sunny-side-up/paneld/internal/tiny"
	"github.com/kaievns/sunny-side-up/paneld/internal/ui"
)

// fanOpts is the board revision plus everything fan/backlight control needs.
type fanOpts struct {
	v2    bool    // v2 board: ATtiny owns fan PWM + backlight over SPI
	bl    byte    // v2 backlight brightness
	onC   float64 // fan on at/above this SoC temp (hysteresis top)
	offC  float64 // fan off below this temp (hysteresis bottom)
	maxC  float64 // v2: temp of full fan duty (curve top)
	floor byte    // v2: never drop below this duty - the fan always runs and
	// ramps floor->full across onC..maxC (0 = allow off)
	floorAuto bool // v2: pick floor by probing what fan is actually fitted
}

// Fan classification. The project's boards get whichever 4010 fan came to hand,
// and a 12V fan on the 5V rail behaves nothing like a 5V one: it tops out around
// 1650 rpm (vs ~4400) and its whole duty range spans barely 300 rpm, so throttling
// it is pointless - it simply has to run continuously to move any air. Peak RPM
// tells the two apart with a wide margin, so the daemon probes at startup instead
// of relying on someone to configure it per box.
const (
	probeDuty    = 255             // full tilt: fastest spin-up, widest separation
	probeSettle  = 3 * time.Second // spin-up + tach averaging window
	strongFanRPM = 2500            // above this at full duty = healthy 5V fan
	spinningRPM  = 500             // below this, nothing is turning
	weakFanFloor = byte(127)       // 50%: keep a weak fan running always
)

// probeFanFloor spins the fan up, reads what it can actually do, and returns the
// duty floor to hold it at: none for a strong fan (it can idle off and ramp on
// demand), a continuous 50% for a weak one. An unreadable or stalled fan gets
// the cautious answer - keep air moving.
func probeFanFloor(tc *tiny.Client) byte {
	if err := tc.SetFanDuty(probeDuty); err != nil {
		log.Printf("fan probe: %v - assuming a weak fan", err)
		return weakFanFloor
	}
	time.Sleep(probeSettle)
	rpm, _, err := tc.Status()
	if err != nil {
		log.Printf("fan probe: %v - assuming a weak fan", err)
		return weakFanFloor
	}
	switch {
	case rpm >= strongFanRPM:
		log.Printf("fan probe: %d rpm at full duty - 5V fan, using the temperature curve", rpm)
		return 0
	case rpm >= spinningRPM:
		log.Printf("fan probe: %d rpm at full duty - weak (12V-on-5V) fan, holding it at 50%%", rpm)
		return weakFanFloor
	default:
		log.Printf("fan probe: %d rpm at full duty - no fan detected or stalled; holding 50%% in case", rpm)
		return weakFanFloor
	}
}

// minDuty is the calibrated keep-spinning floor (also clamped in firmware):
// below this the NF-A4x10 stalls, so the curve starts here, ~3200 rpm.
const minDuty = 26

func main() {
	role := flag.String("role", "gateway", "node role: gateway | extender | homelab")
	name := flag.String("name", "node", "location name shown in the top row")
	ip := flag.String("ip", "", "node IP shown in the top row ('auto' = read it off the LAN interface each refresh)")
	lanIface := flag.String("lan-iface", "br-lan", "interface -ip auto reads the address from")
	iface := flag.String("iface", "", "network interface for throughput (WAN for gateway, uplink for nodes)")
	wifiIface := flag.String("wifiiface", "", "wifi interface(s) for client counts, comma-separated")
	ping := flag.String("ping", "", "latency probe target (public IP for gateway, gateway IP for nodes)")
	dhcp := flag.Bool("dhcp", false, "count DHCP leases as clients (gateway)")
	hosts := flag.String("hosts", "", "comma-separated homelab hosts to health-check")
	hasWifi := flag.String("wifi", "auto", "node has a wifi module - shows its temp and the radio indicators. 'auto' follows whatever radios the box actually has; true/false force it")
	hasFan := flag.Bool("fan", false, "node has the fan board (control + show fan)")
	pingWarn := flag.Float64("ping-warn", 0, "latency (ms) above which the node is degraded (0 = role default)")
	tempWarn := flag.Float64("temp-warn", 80, "temperature (°C) above which the node is degraded")
	fanOnC := flag.Float64("fan-on", 65, "turn the fan on at/above this temp (°C)")
	fanOffC := flag.Float64("fan-off", 55, "turn the fan off below this temp (°C, hysteresis)")
	fanMaxC := flag.Float64("fan-max", 75, "temp (°C) of full fan speed (v2 duty curve top)")
	fanFloor := flag.String("fan-floor", "auto", "duty percent (1-100) the fan never drops below - it then always spins and ramps to full by -fan-max. 'auto' probes the fitted fan at startup and holds a weak one at 50%; '0' always allows the fan to switch off")
	boardRev := flag.String("board", "v1", "panel board revision: v1 (GPIO fan gate) | v2 (ATtiny fan+BL)")
	bl := flag.Int("bl", 200, "backlight brightness 0-255 (v2 boards)")

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
		HasWifi:    strings.EqualFold(*hasWifi, "true") || *hasWifi == "1",
		WifiAuto:   strings.EqualFold(*hasWifi, "auto"),
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
	opts := fanOpts{v2: *boardRev == "v2", bl: byte(*bl), onC: *fanOnC, offC: *fanOffC, maxC: *fanMaxC}
	if strings.EqualFold(*fanFloor, "auto") {
		opts.floorAuto = true
	} else if pct, err := strconv.Atoi(*fanFloor); err != nil {
		log.Fatalf("paneld: bad -fan-floor %q: want 'auto' or 0-100", *fanFloor)
	} else if pct > 0 {
		if pct > 100 {
			pct = 100
		}
		opts.floor = byte(pct * 255 / 100)
	}

	autoLan := ""
	if strings.EqualFold(*ip, "auto") {
		autoLan, node.IP = *lanIface, ""
	}

	if err := run(node, provider, cfg, *interval, opts, autoLan); err != nil {
		log.Fatalf("paneld: %v", err)
	}
}

func run(node metric.Node, provider metric.Provider, cfg panel.Config, interval time.Duration, opts fanOpts, autoLan string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Fan on/off state, kept across reconnects. This 3-wire fan can't be
	// speed-controlled by chopping its supply (it only runs at full), so control
	// is hysteresis on/off and the tach is used to verify it actually spins.
	fanOn := false

	return panel.Supervise(ctx, cfg, func(ctx context.Context, p *panel.Panel) error {
		// v2 boards: the ATtiny owns fan PWM and backlight, reached over the
		// shared SPI bus. Set up per connection; a failed ping is treated like
		// any other link fault so the supervisor retries (the ATtiny may still
		// be booting right after plug-in). Every frame we send also feeds its
		// 60s dead-host failsafe.
		var tc *tiny.Client
		if opts.v2 {
			if err := p.Device().ConfigureHighByte(0x01, 0x01); err != nil { // /CS_TINY idle high
				return err
			}
			tc = tiny.New(p.Device(), cfg.ClockHz)
			if _, _, err := tc.Ping(); err != nil {
				return fmt.Errorf("v2 board: ATtiny not answering: %w", err)
			}
			if node.HasFan && opts.floorAuto {
				opts.floor = probeFanFloor(tc) // doubles as a fan health check
			}
		}

		// Boot splash: light the glass the moment the link is up, so the
		// device shows life while the first sample is still being gathered
		// (and again on every reconnect).
		fb := p.NewFramebuffer()
		ui.RenderSplash(fb.RGBA, node.Role, node.Name)
		if err := p.Blit(fb); err != nil {
			return err
		}
		if opts.v2 {
			if err := tc.SetBL(opts.bl); err != nil {
				return err
			}
		} else if err := p.Backlight(true); err != nil {
			return err
		}

		lit := false
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			sample, err := provider.Read(ctx)
			if err != nil {
				return err
			}

			if autoLan != "" {
				node.IP = detectIP(autoLan)
			}
			scr := metric.Build(node, sample)
			scr.Clock = time.Now().Format("15:04")

			if node.HasFan {
				// The fan answers for the whole enclosure, so it tracks the
				// hottest thing in it: the SoC, or the wifi silicon on nodes
				// that have radios (those idle ~15C above the SoC, so they
				// usually set the pace). If the sensors read nothing at all,
				// fail safe: run the fan.
				hot := hottest(sample.CPUTempC, sample.WifiTempC)
				if math.IsNaN(hot) {
					fanOn = true
				} else if !fanOn && hot >= opts.onC {
					fanOn = true
				} else if fanOn && hot < opts.offC {
					fanOn = false
				}
				switch {
				case opts.v2:
					// Proportional duty on the v2 buck, ramping to full speed at
					// maxC. With a floor configured the fan never stops and the
					// ramp starts there; otherwise it switches off on the
					// hysteresis and starts from the calibrated minimum. Duty 0
					// also clears a latched STALL_FAULT in the firmware.
					var duty byte
					switch {
					case opts.floor > 0:
						duty = fanDuty(hot, opts.onC, opts.maxC, opts.floor)
					case fanOn:
						duty = fanDuty(hot, opts.onC, opts.maxC, minDuty)
					}
					if err := tc.SetFanDuty(duty); err != nil {
						return err
					}
					rpm, flags, err := tc.Status()
					if err != nil {
						return err
					}
					switch {
					case flags&tiny.FlagStallFault != 0:
						scr.Fan = "stall"
					case duty == 0:
						scr.Fan = "off"
					default:
						scr.Fan = fmt.Sprintf("%d", rpm)
					}
				case fanOn || opts.floor > 0: // v1 has no PWM: a floor just means always on
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
				default:
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

// detectIP returns the node's own IPv4 for the top row: the address on the LAN
// interface if it has one, else the first global address on any interface.
// Keeping this live means the panel follows a renumbered network instead of
// showing a stale hand-configured address.
func detectIP(lanIface string) string {
	if ifi, err := net.InterfaceByName(lanIface); err == nil {
		if s := firstIPv4(ifi); s != "" {
			return s
		}
	}
	ifis, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifi := range ifis {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		if s := firstIPv4(&ifi); s != "" {
			return s
		}
	}
	return ""
}

func firstIPv4(ifi *net.Interface) string {
	addrs, err := ifi.Addrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if v4 := n.IP.To4(); v4 != nil && v4.IsGlobalUnicast() {
			return v4.String()
		}
	}
	return ""
}

// hottest returns the highest of the readings, ignoring unavailable sensors
// (NaN). It is NaN only when nothing could be read at all.
func hottest(temps ...float64) float64 {
	out := math.NaN()
	for _, t := range temps {
		if math.IsNaN(t) {
			continue
		}
		if math.IsNaN(out) || t > out {
			out = t
		}
	}
	return out
}

// fanDuty maps a SoC temperature onto the v2 fan curve: floor duty at or below
// onC, ramping linearly to full duty at maxC. An unreadable sensor means full
// speed - same fail-safe stance as v1.
func fanDuty(tempC, onC, maxC float64, floor byte) byte {
	if math.IsNaN(tempC) {
		return 255
	}
	f := (tempC - onC) / (maxC - onC)
	if f < 0 {
		f = 0
	} else if f > 1 {
		f = 1
	}
	return byte(float64(floor) + f*(255-float64(floor)) + 0.5)
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
