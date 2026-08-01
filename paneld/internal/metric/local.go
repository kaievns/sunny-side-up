package metric

import (
	"context"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kaievns/sunny-side-up/paneld/internal/ui"
)

// LocalProvider reads this node's own metrics from the kernel (/sys, /proc) and
// a few CLI probes (ping, iw, DNS). It is stateful: it holds the previous
// interface counters to compute throughput rates and the sparkline history.
type LocalProvider struct {
	node Node

	histMbps []float64 // raw download rate history
	lastRx   uint64
	lastTx   uint64
	lastAt   time.Time
	haveLast bool
}

// NewLocalProvider returns a collector for the given node.
func NewLocalProvider(n Node) *LocalProvider {
	return &LocalProvider{node: n, histMbps: make([]float64, 64)}
}

func (p *LocalProvider) Read(ctx context.Context) (Sample, error) {
	n := p.node
	s := Sample{
		CPUTempC:  readCPUTemp(),
		WifiTempC: math.NaN(),
		PingMs:    math.NaN(),
		DNSMs:     math.NaN(),
		LossPct:   math.NaN(),
		FanRPM:    -1,
		UptimeSec: readUptime(),
		LinkMbps:  readLinkSpeed(n.Iface),
	}
	if n.HasWifi {
		s.WifiTempC = readWifiTemp()
	}

	s.DownMbps, s.UpMbps = p.throughput()
	s.Clients = p.clients()

	if n.PingTarget != "" {
		rtt, loss, up := pingProbe(ctx, n.PingTarget)
		s.PingMs, s.LossPct = rtt, loss
		switch n.Role {
		case ui.RoleGateway:
			s.WANUp = up
			s.DNSUp, s.DNSMs = dnsProbe(ctx)
		default:
			s.UplinkUp = up
		}
	} else {
		// No probe configured; assume the link is up.
		s.WANUp, s.DNSUp, s.UplinkUp = true, true, true
	}

	if len(n.Hosts) > 0 {
		s.HostsUp, s.HostsTotal = checkHosts(ctx, n.Hosts)
	}

	// Sparkline: keep raw Mbps history and normalise by an adaptive peak.
	p.histMbps = append(p.histMbps[1:], s.DownMbps)
	s.Down01 = normalise(p.histMbps)
	return s, nil
}

// throughput returns download/upload rate in Mbps from interface byte counters.
func (p *LocalProvider) throughput() (down, up float64) {
	rx := readUint("/sys/class/net/" + p.node.Iface + "/statistics/rx_bytes")
	tx := readUint("/sys/class/net/" + p.node.Iface + "/statistics/tx_bytes")
	now := time.Now()
	defer func() { p.lastRx, p.lastTx, p.lastAt, p.haveLast = rx, tx, now, true }()
	if !p.haveLast {
		return 0, 0
	}
	dt := now.Sub(p.lastAt).Seconds()
	if dt <= 0 {
		return 0, 0
	}
	mbps := func(cur, prev uint64) float64 {
		if cur < prev { // counter reset
			return 0
		}
		return float64(cur-prev) * 8 / 1e6 / dt
	}
	return mbps(rx, p.lastRx), mbps(tx, p.lastTx)
}

// clients counts DHCP leases (gateway) or associated wifi stations (nodes).
func (p *LocalProvider) clients() int {
	if p.node.DHCPLeases {
		b, err := os.ReadFile("/tmp/dhcp.leases")
		if err != nil {
			return 0
		}
		return countLines(b) // one active lease per line
	}
	if p.node.WifiIface != "" {
		// One AP node can run several BSS ifaces (bands / SSIDs) - sum the
		// associated stations across the comma-separated list.
		n := 0
		for _, ifc := range strings.Split(p.node.WifiIface, ",") {
			if ifc = strings.TrimSpace(ifc); ifc == "" {
				continue
			}
			out, err := run(context.Background(), "iw", "dev", ifc, "station", "dump")
			if err == nil {
				n += strings.Count(out, "Station ")
			}
		}
		return n
	}
	return 0
}

// --- kernel/sysfs collectors -------------------------------------------------

// readCPUTemp finds the SoC thermal zone and returns its temperature in °C.
func readCPUTemp() float64 {
	zones, _ := filepath.Glob("/sys/class/thermal/thermal_zone*")
	best := math.NaN()
	for _, z := range zones {
		typ := strings.ToLower(strings.TrimSpace(readString(z + "/type")))
		t := readMilliDeg(z + "/temp")
		if math.IsNaN(t) {
			continue
		}
		// Prefer a zone that looks like the SoC/CPU; else take the first valid.
		if strings.Contains(typ, "soc") || strings.Contains(typ, "cpu") || strings.Contains(typ, "tsadc") {
			return t
		}
		if math.IsNaN(best) {
			best = t
		}
	}
	return best
}

// readWifiTemp finds the wifi (mt76) hwmon sensor and returns °C.
func readWifiTemp() float64 {
	hwmons, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, h := range hwmons {
		name := strings.ToLower(strings.TrimSpace(readString(h + "/name")))
		if strings.Contains(name, "mt79") || strings.Contains(name, "mt76") {
			if t := readMilliDeg(h + "/temp1_input"); !math.IsNaN(t) {
				return t
			}
		}
	}
	return math.NaN()
}

func readUptime() int64 {
	f := strings.Fields(readString("/proc/uptime"))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return int64(v)
}

func readLinkSpeed(iface string) int {
	if iface == "" {
		return 0
	}
	v := strings.TrimSpace(readString("/sys/class/net/" + iface + "/speed"))
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// --- probes ------------------------------------------------------------------

var (
	reLoss = regexp.MustCompile(`(\d+)% packet loss`)
	reRTT  = regexp.MustCompile(`= [\d.]+/([\d.]+)/`)
)

// pingProbe pings target a few times and returns avg RTT (ms), loss %, and
// whether it is reachable at all.
func pingProbe(ctx context.Context, target string) (rttMs, lossPct float64, up bool) {
	rttMs, lossPct = math.NaN(), math.NaN()
	out, _ := run(ctx, "ping", "-c", "3", "-W", "1", target)
	if m := reLoss.FindStringSubmatch(out); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			lossPct = v
			up = v < 100
		}
	}
	if m := reRTT.FindStringSubmatch(out); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			rttMs = v
		}
	}
	return rttMs, lossPct, up
}

// dnsProbe times a DNS lookup and reports whether it resolved.
func dnsProbe(ctx context.Context) (ok bool, ms float64) {
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var r net.Resolver
	start := time.Now()
	_, err := r.LookupHost(c, "openwrt.org")
	if err != nil {
		return false, math.NaN()
	}
	return true, float64(time.Since(start).Microseconds()) / 1000
}

// checkHosts pings each host once and returns how many are up.
func checkHosts(ctx context.Context, hosts []string) (up, total int) {
	total = len(hosts)
	for _, h := range hosts {
		if out, _ := run(ctx, "ping", "-c", "1", "-W", "1", h); reLoss.MatchString(out) {
			if m := reLoss.FindStringSubmatch(out); m != nil && m[1] != "100" {
				up++
			}
		}
	}
	return up, total
}

// --- small helpers -----------------------------------------------------------

func run(ctx context.Context, name string, args ...string) (string, error) {
	c, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(c, name, args...).Output()
	return string(out), err
}

func readString(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func readUint(path string) uint64 {
	v, _ := strconv.ParseUint(strings.TrimSpace(readString(path)), 10, 64)
	return v
}

func readMilliDeg(path string) float64 {
	s := strings.TrimSpace(readString(path))
	if s == "" {
		return math.NaN()
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return v / 1000
}

func countLines(b []byte) int {
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// normalise maps a raw-Mbps history to 0..1 by an adaptive peak (so the
// sparkline auto-scales), with a floor so a quiet line isn't amplified to noise.
func normalise(raw []float64) []float64 {
	peak := 10.0
	for _, v := range raw {
		if v > peak {
			peak = v
		}
	}
	out := make([]float64, len(raw))
	for i, v := range raw {
		out[i] = clamp01(v / peak)
	}
	return out
}
