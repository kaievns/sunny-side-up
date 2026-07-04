package ui

import "image/color"

// Palette from the "Okibi" (dark) theme - https://kaievns.github.io/okibi-akiri-theme/
// Background is pure black per the panel design; neutrals are the theme's spine,
// accents are its named colors.
var (
	// Behind the tinted acrylic, muted tones vanish - so everything runs at full
	// brightness. All neutral text is pure white (hierarchy comes from size, not
	// brightness), and the accents are vivid, high-luminance versions of the
	// Okibi hues.
	colBG     = color.RGBA{0x00, 0x00, 0x00, 0xff} // black
	colWhite  = color.RGBA{0xff, 0xff, 0xff, 0xff} // all neutral text
	colText   = color.RGBA{0xff, 0xff, 0xff, 0xff} // (kept as a name; = white)
	colDim    = color.RGBA{0xff, 0xff, 0xff, 0xff} // (kept as a name; = white)
	colDimmer = color.RGBA{0xff, 0xff, 0xff, 0xff} // (kept as a name; = white)

	colBlue  = color.RGBA{0x54, 0xb6, 0xff, 0xff} // gateway identity - bright sky
	colGreen = color.RGBA{0x3d, 0xf5, 0x7e, 0xff} // nominal, latency - bright green
	colAmber = color.RGBA{0xff, 0xc2, 0x2e, 0xff} // degraded - bright amber
	colRed   = color.RGBA{0xff, 0x53, 0x4e, 0xff} // down - bright red
	colEmber = color.RGBA{0xff, 0x84, 0x40, 0xff} // homelab identity - bright ember
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

// base is the role's identity color (used for the role word and, when healthy,
// the accent line and status text).
func (r Role) base() color.RGBA {
	switch r {
	case RoleGateway:
		return colBlue
	case RoleHomelab:
		return colEmber
	default: // extender
		return colGreen
	}
}

// Health is the node's condition and recolors the accent, hero, and status.
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

// accent is the color of the left status line and the sparkline: the role's
// identity when healthy, else the fault color.
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
