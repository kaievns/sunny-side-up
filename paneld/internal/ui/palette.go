package ui

import "image/color"

// Palette straight from the 2026-07-19 design spec (the docs/design/ mocks are
// P3 screenshots and read brighter - these are the true sRGB values): a near-
// black ground, a four-step foreground ladder, one identity color per role,
// and a good/warn/bad state triple.
var (
	colBG     = color.RGBA{0x0a, 0x0b, 0x0c, 0xff} // background
	colWhite  = color.RGBA{0xf2, 0xf4, 0xf5, 0xff} // fg: values / bold figures
	colText   = color.RGBA{0xc9, 0xcd, 0xd1, 0xff} // fg2: location, vitals values
	colDim    = color.RGBA{0x9a, 0xa0, 0xa6, 0xff} // fg3: units, sub line, clock, uptime
	colDimmer = color.RGBA{0x7c, 0x82, 0x8a, 0xff} // fg4: IP, icons

	colBlue      = color.RGBA{0x5c, 0xaa, 0xff, 0xff} // gateway identity
	colRoleGreen = color.RGBA{0x41, 0xd9, 0x7e, 0xff} // extender identity
	colEmber     = color.RGBA{0xff, 0x8f, 0x40, 0xff} // homelab identity

	colGreen = color.RGBA{0x3f, 0xe0, 0x8a, 0xff} // ok state
	colAmber = color.RGBA{0xff, 0xb2, 0x24, 0xff} // degraded
	colRed   = color.RGBA{0xff, 0x52, 0x52, 0xff} // down

	// Graph gridlines: 5% white over the background.
	colGrid = blend(color.RGBA{0xff, 0xff, 0xff, 0xff}, colBG, 0.05)
)

// Role is the kind of node and drives its identity color and label.
type Role int

const (
	RoleGateway Role = iota
	RoleExtender
	RoleHomelab
)

func (r Role) label() string {
	switch r {
	case RoleGateway:
		return "GATEWAY"
	case RoleExtender:
		return "EXTENDER"
	case RoleHomelab:
		return "HOMELAB"
	}
	return "NODE"
}

// base is the role's identity color (the role word and, when healthy, the
// sparkline).
func (r Role) base() color.RGBA {
	switch r {
	case RoleGateway:
		return colBlue
	case RoleHomelab:
		return colEmber
	default: // extender
		return colRoleGreen
	}
}

// Health is the node's condition and recolors the status bar, hero, and spark.
type Health int

const (
	OK Health = iota
	Degraded
	Down
)

func (h Health) color() color.RGBA {
	switch h {
	case Degraded:
		return colAmber
	case Down:
		return colRed
	default:
		return colGreen
	}
}

// accent is the color of the sparkline: the role's identity when healthy, else
// the fault color.
func accent(r Role, h Health) color.RGBA {
	if h != OK {
		return h.color()
	}
	return r.base()
}

// blend returns fg over bg with opacity a (0..1), assuming an opaque bg.
func blend(fg, bg color.RGBA, a float64) color.RGBA {
	if a < 0 {
		a = 0
	} else if a > 1 {
		a = 1
	}
	return color.RGBA{
		R: uint8(float64(fg.R)*a + float64(bg.R)*(1-a)),
		G: uint8(float64(fg.G)*a + float64(bg.G)*(1-a)),
		B: uint8(float64(fg.B)*a + float64(bg.B)*(1-a)),
		A: 0xff,
	}
}
