package metric

import (
	"math"
	"testing"

	"github.com/kaievns/sunny-side-up/paneld/internal/ui"
)

// healthySample is a node with nothing wrong: probes answer, both sockets have
// cables. Tests below break one thing at a time.
func healthySample() Sample {
	return Sample{
		PingMs: 5, LinkMbps: 1000,
		CPUTempC: 45, WifiTempC: math.NaN(),
		LanPorts: 1, LanPortsUp: 1,
		WanPortKnown: true, WanPortUp: true,
		WANUp: true, DNSUp: true, UplinkUp: true,
	}
}

func ifaceState(t *testing.T, out []ui.Iface, label string) ui.Health {
	t.Helper()
	for _, f := range out {
		if f.Label == label {
			return f.State
		}
	}
	t.Fatalf("no %q indicator in %v", label, out)
	return ui.OK
}

// The bug this guards: the LAN indicator used to be hardcoded OK for gateways
// and derived only from ping elsewhere, so a pulled cable never showed.
func TestUnpluggedLanShowsDown(t *testing.T) {
	for _, role := range []ui.Role{ui.RoleGateway, ui.RoleExtender, ui.RoleHomelab} {
		n := Node{Role: role, TempWarnC: 80, PingWarnMs: 50}
		s := healthySample()
		s.LanPortsUp = 0

		if got := ifaceState(t, ifaces(n, s), "LAN"); got != ui.Down {
			t.Errorf("role %v: LAN = %v, want Down when the cable is out", role, got)
		}
		if got := ifaceState(t, ifaces(n, healthySample()), "LAN"); got != ui.OK {
			t.Errorf("role %v: LAN = %v, want OK when the cable is in", role, got)
		}
	}
}

func TestUnpluggedWanShowsDownAndSaysSo(t *testing.T) {
	n := Node{Role: ui.RoleGateway, TempWarnC: 80, PingWarnMs: 50}
	s := healthySample()
	s.WanPortUp = false

	if got := ifaceState(t, ifaces(n, s), "WAN"); got != ui.Down {
		t.Errorf("WAN = %v, want Down when the cable is out", got)
	}
	// Carrier beats the probe, so the fault must name the cable rather than
	// blame the ISP - even while the (stale) probe flags still say all is well.
	health, fault := assess(n, s)
	if health != ui.Down {
		t.Errorf("health = %v, want Down", health)
	}
	if fault != "WAN cable unplugged" {
		t.Errorf("fault = %q, want the cable called out by name", fault)
	}
}

// A wireless-uplink node has no WAN socket to judge; it must not invent one.
func TestWirelessUplinkNeverReportsACable(t *testing.T) {
	n := Node{Role: ui.RoleExtender, TempWarnC: 80, PingWarnMs: 50}
	s := healthySample()
	s.WanPortKnown, s.WanPortUp = false, false

	if got := ifaceState(t, ifaces(n, s), "WAN"); got != ui.OK {
		t.Errorf("WAN = %v, want OK when there is no cable to miss", got)
	}
	if _, fault := assess(n, s); fault != "" {
		t.Errorf("fault = %q, want none", fault)
	}
}

// Likewise a node whose ports are all uplink/wifi reports no LAN sockets.
func TestNoLanPortsLeavesIndicatorAlone(t *testing.T) {
	n := Node{Role: ui.RoleExtender, TempWarnC: 80, PingWarnMs: 50}
	s := healthySample()
	s.LanPorts, s.LanPortsUp = 0, 0

	if got := ifaceState(t, ifaces(n, s), "LAN"); got != ui.OK {
		t.Errorf("LAN = %v, want OK when the node has no LAN socket", got)
	}
}

// A socket that has never had a cable is not a fault - the provider reports it
// as no port at all, so a wireless extender's spare port stays quiet.
func TestNeverUsedPortIsNotAFault(t *testing.T) {
	p := &LocalProvider{}
	// lanSeen is empty: whatever ports exist, none has been connected.
	if got := len(p.lanSeen); got != 0 {
		t.Fatalf("fresh provider already remembers %d ports", got)
	}
	n := Node{Role: ui.RoleExtender, TempWarnC: 80, PingWarnMs: 50}
	s := healthySample()
	s.LanPorts, s.LanPortsUp = 0, 0 // what lanLink yields for an unused socket

	if got := ifaceState(t, ifaces(n, s), "LAN"); got != ui.OK {
		t.Errorf("LAN = %v, want OK for a socket that was never in use", got)
	}
}

// One cable in a multi-port switch is normal, not a fault.
func TestPartiallyPopulatedSwitchIsFine(t *testing.T) {
	n := Node{Role: ui.RoleGateway, TempWarnC: 80, PingWarnMs: 50}
	s := healthySample()
	s.LanPorts, s.LanPortsUp = 4, 1

	if got := ifaceState(t, ifaces(n, s), "LAN"); got != ui.OK {
		t.Errorf("LAN = %v, want OK with one of four ports connected", got)
	}
}
