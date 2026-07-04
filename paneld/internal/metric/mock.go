package metric

import (
	"context"
	"math"

	"github.com/kaievns/sunny-side-up/paneld/internal/ui"
)

// MockProvider yields plausible, gently-moving samples so the daemon and the
// renderer can run on a dev machine with no real hardware.
type MockProvider struct {
	node Node
	hist []float64
	t    int
}

// NewMockProvider returns a mock sized to the node's role.
func NewMockProvider(n Node) *MockProvider {
	h := make([]float64, 64)
	for i := range h {
		h[i] = 0.5
	}
	return &MockProvider{node: n, hist: h}
}

func (m *MockProvider) Read(ctx context.Context) (Sample, error) {
	m.t++
	v := clamp01(0.55 + 0.18*math.Sin(float64(m.t)/9) + 0.05*math.Sin(float64(m.t)/2.3))
	m.hist = append(m.hist[1:], v)

	peak := 220.0
	switch m.node.Role {
	case ui.RoleGateway:
		peak = 560
	case ui.RoleHomelab:
		peak = 420
	}
	down := 40 + peak*v

	s := Sample{
		DownMbps:  down,
		UpMbps:    down * 0.15,
		Clients:   clientsFor(m.node.Role),
		PingMs:    0.5 + 0.2*math.Sin(float64(m.t)/5),
		LinkMbps:  1000,
		DNSMs:     4.2,
		LossPct:   0,
		CPUTempC:  50 + 4*v,
		WifiTempC: math.NaN(),
		FanRPM:    -1,
		UptimeSec: 14 * 86400,
		Down01:    append([]float64(nil), m.hist...),
		WANUp:     true,
		DNSUp:     true,
		UplinkUp:  true,
	}
	if m.node.Role == ui.RoleGateway {
		s.PingMs = 6.4
	}
	if m.node.HasWifi {
		s.WifiTempC = 46 + 3*v
	}
	if m.node.HasFan {
		s.FanRPM = 3000 + int(600*v)
	}
	if m.node.Role == ui.RoleHomelab {
		s.HostsTotal, s.HostsUp = 14, 14
	}
	return s, nil
}

func clientsFor(r ui.Role) int {
	switch r {
	case ui.RoleGateway:
		return 46
	case ui.RoleHomelab:
		return 12
	default:
		return 7
	}
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
