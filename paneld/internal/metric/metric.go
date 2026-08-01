// Package metric is the data-sourcing layer for the panel. It defines the raw
// per-node metrics (Sample), the node's configuration (Node), a Provider
// interface that yields samples, and Build, which turns a Sample into the
// ui.Screen the renderer draws.
//
// The panel is per-node: each router runs its own daemon that reads only its own
// machine (via LocalProvider) and shows its own screen. MockProvider yields fake
// samples so the daemon can run on a dev machine.
//
//	sources ─▶ Sample ─▶ Build ─▶ ui.Screen ─▶ panel
package metric

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/kaievns/sunny-side-up/paneld/internal/ui"
)

// Node is the static configuration of the router this daemon runs on: what it
// is, and where to read its metrics from.
type Node struct {
	Role     ui.Role
	Name     string // "kitchen", "living room"
	IP       string // shown in the top row

	Iface      string   // network interface to measure throughput on (WAN for gateway, uplink for nodes)
	WifiIface  string   // wifi interface for associated-client counts ("" if none)
	DHCPLeases bool     // count DHCP leases as "clients" (gateway) instead of wifi stations
	PingTarget string   // latency probe target (a public IP for the gateway, the gateway IP for nodes)
	PingLabel  string   // how the aux label frames the target: "dns 4.2 · loss 0%" vs "→ gateway · 1G"
	Hosts      []string // homelab hosts to health-check (empty for non-homelab)

	HasWifi bool // has a wifi module (shows wifi temp)
	HasFan  bool // has the fan board (shows fan rpm)

	// Health thresholds.
	PingWarnMs float64 // latency above this = degraded (e.g. 80 for ISP, 15 for a wired uplink)
	TempWarnC  float64 // cpu/wifi temp above this = degraded (e.g. 80)
}

// Sample is one reading of a node's raw metrics. Unknown values are NaN (floats)
// or -1 (ints). Down01 is the throughput history for the sparkline, already
// normalised to 0..1.
type Sample struct {
	DownMbps float64
	UpMbps   float64
	Clients  int

	PingMs   float64 // NaN if the target is unreachable
	LinkMbps int     // negotiated uplink link speed (1000, 100, ...), 0 if unknown
	DNSMs    float64 // gateway: DNS resolution time, NaN if n/a
	LossPct  float64 // gateway: packet loss %, NaN if n/a

	CPUTempC  float64 // NaN if unknown
	WifiTempC float64 // NaN if none
	FanRPM    int     // -1 if no fan / not measured

	UptimeSec int64

	HostsUp    int
	HostsTotal int

	Down01 []float64

	// Coarse reachability flags, set by the collector.
	WANUp    bool // gateway: the internet is reachable
	DNSUp    bool // gateway: DNS resolves
	UplinkUp bool // node: the gateway is reachable
}

// Provider yields samples for this node. Implementations are stateful (they hold
// the previous counters needed to compute rates, plus the sparkline history).
type Provider interface {
	Read(ctx context.Context) (Sample, error)
}

// --- formatting helpers, shared by Build and the mock ---

func formatUptime(sec int64) string {
	d := time.Duration(sec) * time.Second
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
}

// formatMbps renders a throughput value without a decimal point.
func formatMbps(v float64) string {
	if v < 0 || math.IsNaN(v) {
		return "0"
	}
	return fmt.Sprintf("%.0f", v)
}

// formatPing renders latency: one decimal below 10ms, whole numbers above.
func formatPing(ms float64) string {
	if math.IsNaN(ms) || ms < 0 {
		return "--"
	}
	if ms < 10 {
		return fmt.Sprintf("%.1f", ms)
	}
	return fmt.Sprintf("%.0f", ms)
}

func formatTemp(c float64) string {
	if math.IsNaN(c) {
		return ""
	}
	return fmt.Sprintf("%.0f°", c)
}

func linkSpeedLabel(mbps int) string {
	switch {
	case mbps >= 1000:
		return fmt.Sprintf("%gG", float64(mbps)/1000) // 1G, 2.5G, 10G
	case mbps > 0:
		return fmt.Sprintf("%dM", mbps)
	default:
		return "?"
	}
}
