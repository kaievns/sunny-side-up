package metric

import (
	"fmt"
	"math"

	"github.com/kaievns/sunny-side-up/paneld/internal/ui"
)

// Build turns a raw Sample into the ui.Screen the renderer draws: it formats the
// numbers and derives the health/fault state. All the display and threshold
// logic lives here, in one place.
func Build(n Node, s Sample) ui.Screen {
	health, statusL, fault := assess(n, s)

	scr := ui.Screen{
		Role:     n.Role,
		Health:   health,
		Location: n.Name,
		IP:       n.IP,
		Clock:    "", // set by the daemon (local clock)

		Hero:     formatMbps(s.DownMbps),
		HeroUnit: "Mb/s",
		Corner: [2]string{
			"↑ " + formatMbps(s.UpMbps) + " Mb/s",
			fmt.Sprintf("%d clients", s.Clients),
		},

		Aux:      formatPing(s.PingMs),
		AuxUnit:  "ms",
		AuxLabel: auxLabel(n, s),

		Spark: s.Down01,

		StatusL: statusL,
		Fault:   fault,

		CPU: formatTemp(s.CPUTempC),
		Up:  formatUptime(s.UptimeSec),
	}
	if n.HasWifi {
		scr.Wifi = formatTemp(s.WifiTempC)
	}
	if n.HasFan && s.FanRPM >= 0 {
		scr.Fan = fmt.Sprintf("%d", s.FanRPM)
	}

	// The aux (latency) stays green when the node is unhealthy for a reason
	// unrelated to the link - e.g. a homelab host is down but the mesh is fine.
	if health != ui.OK && linkHealthy(n, s) {
		scr.AuxGreen = true
	}
	return scr
}

// assess derives the health state, the healthy status phrase, and the fault
// message, per role.
func assess(n Node, s Sample) (ui.Health, string, string) {
	switch n.Role {
	case ui.RoleGateway:
		if !s.WANUp {
			return ui.Down, "", "WAN down · no internet"
		}
		if !s.DNSUp {
			return ui.Degraded, "", "dns not resolving"
		}
		if overTemp(n, s) {
			return ui.Degraded, "", "running hot"
		}
		if !math.IsNaN(s.PingMs) && s.PingMs > n.PingWarnMs {
			return ui.Degraded, "", "high latency · " + formatPing(s.PingMs) + "ms"
		}
		return ui.OK, "internet ok", ""

	case ui.RoleHomelab:
		// Homelab is dual-duty: a homelab host being down is the headline fault,
		// but the uplink/traffic may be perfectly fine.
		if !s.UplinkUp {
			return ui.Down, "", "uplink lost · retrying"
		}
		if s.HostsTotal > 0 && s.HostsUp < s.HostsTotal {
			down := s.HostsTotal - s.HostsUp
			return ui.Degraded, "", fmt.Sprintf("%d host%s unreachable", down, plural(down))
		}
		if degradedLink(n, s) {
			return ui.Degraded, "", "uplink degraded · " + linkSpeedLabel(s.LinkMbps)
		}
		if overTemp(n, s) {
			return ui.Degraded, "", "running hot"
		}
		return ui.OK, fmt.Sprintf("%d/%d hosts up", s.HostsUp, s.HostsTotal), ""

	default: // RoleExtender
		if !s.UplinkUp {
			return ui.Down, "", "uplink lost · retrying"
		}
		if degradedLink(n, s) {
			return ui.Degraded, "", "uplink degraded · " + linkSpeedLabel(s.LinkMbps)
		}
		if overTemp(n, s) {
			return ui.Degraded, "", "running hot"
		}
		return ui.OK, "uplink ok", ""
	}
}

func auxLabel(n Node, s Sample) string {
	if n.Role == ui.RoleGateway {
		parts := ""
		if !math.IsNaN(s.DNSMs) {
			parts = "dns " + formatPing(s.DNSMs)
		}
		if !math.IsNaN(s.LossPct) {
			if parts != "" {
				parts += " · "
			}
			parts += fmt.Sprintf("loss %.0f%%", s.LossPct)
		}
		if parts == "" {
			return n.PingLabel
		}
		return parts
	}
	// Extender / homelab: the uplink to the gateway and its link speed.
	if s.LinkMbps > 0 {
		return "→ gateway · " + linkSpeedLabel(s.LinkMbps)
	}
	return "→ gateway"
}

// linkHealthy reports whether the node's connection to the world/gateway is fine
// (used to keep the latency figure green even when another metric is degraded).
func linkHealthy(n Node, s Sample) bool {
	if n.Role == ui.RoleGateway {
		return s.WANUp && s.DNSUp && (math.IsNaN(s.PingMs) || s.PingMs <= n.PingWarnMs)
	}
	return s.UplinkUp && !degradedLink(n, s)
}

func degradedLink(n Node, s Sample) bool {
	if s.LinkMbps > 0 && s.LinkMbps < 1000 {
		return true
	}
	return !math.IsNaN(s.PingMs) && s.PingMs > n.PingWarnMs
}

func overTemp(n Node, s Sample) bool {
	if n.TempWarnC <= 0 {
		return false
	}
	if !math.IsNaN(s.CPUTempC) && s.CPUTempC > n.TempWarnC {
		return true
	}
	if n.HasWifi && !math.IsNaN(s.WifiTempC) && s.WifiTempC > n.TempWarnC {
		return true
	}
	return false
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
