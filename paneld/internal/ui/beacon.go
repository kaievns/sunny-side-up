package ui

import (
	"image"
	"image/draw"
)

// Panel dimensions (landscape).
const (
	W = 428
	H = 142

	barW   = 4        // left health bar - full height, flush to the screen's left edge
	padL   = 12       // content left edge - a clear gap to the right of the bar
	padR   = 6        // content right margin
	rightX = W - padR // right alignment edge
)

// Screen is the data for one node's Beacon screen. The renderer decides colors
// from Role + Health; the caller supplies the text and sparkline.
type Screen struct {
	Role     Role
	Health   Health
	Location string // "kitchen", "living room"
	IP       string // "10.0.0.1" - node IP, shown after the location
	Clock    string // "14:32" - top right

	Hero      string // big number: current download Mb/s
	HeroUnit  string // "Mb/s"
	HeroGreen bool   // draw hero in green when healthy (unused by default)

	Corner [2]string // two small stacked "what's connected" stats: "UP 68", "CLI 46"

	Aux      string // big right value: latency "6.4", or "FAIL"
	AuxUnit  string // small unit after it: "ms" (empty for FAIL/LOST)
	AuxLabel string // dim line under aux: "dns 4.2 · loss 0%"
	AuxGreen bool   // keep aux green even when the node is unhealthy (metric itself is fine)

	Spark []float64 // throughput samples in 0..1

	StatusL string // bottom-left plain-language status: "internet ok"
	Fault   string // shown instead of StatusL when not healthy: "WAN down · no internet"

	// Bottom-right vitals, each drawn with its icon (empty ones are skipped):
	CPU  string // cpu temperature, e.g. "52°"
	Wifi string // wifi module temperature, e.g. "48°"
	Fan  string // fan speed, e.g. "3200"
	Up   string // uptime, e.g. "14d" (shown as text, no icon)
}

// NewImage returns a blank panel-sized image.
func NewImage() *image.RGBA { return image.NewRGBA(image.Rect(0, 0, W, H)) }

// Render draws the screen into dst (which must be W x H).
func Render(dst *image.RGBA, s Screen) {
	draw.Draw(dst, dst.Bounds(), image.NewUniform(colBG), image.Point{}, draw.Src)

	// The left bar is a pure device-health indicator: green nominal, amber
	// degraded, red down - full height, against the very edge.
	fillRect(dst, 0, 0, barW, H, s.Health.color())

	// The role's identity color (blue/green/ember), falling back to the fault
	// color when unhealthy - used for the sparkline and bottom status.
	ident := accent(s.Role, s.Health)

	// A tight type scale: two display sizes (hero, aux) and two body sizes
	// (label, unit). Keeping the set small is what makes it read as one system.
	const (
		szHero  = 48
		szAux   = 30
		szLabel = 14.5
		szUnit  = 14
	)

	// --- top row: identity · location · IP  |  time. Sits high so the text tops
	// hug the top edge, level with the health bar. ---
	const yTop = 13
	x := text(dst, padL, yTop, s.Role.label(), SemiBold, szLabel, s.Role.base())
	if s.Location != "" {
		x = text(dst, x, yTop, " "+s.Location, Medium, szLabel, colText)
	}
	if s.IP != "" {
		text(dst, x, yTop, "  "+s.IP, Regular, szLabel, colDim)
	}
	if s.Clock != "" {
		textRight(dst, rightX, yTop, s.Clock, Medium, szLabel, colDim)
	}

	// --- middle band (the focus): hero on the left, aux block on the right,
	// both bottom-aligned to bandBase so they read as one row and the graph
	// below gets a uniform top edge. ---
	const bandBase = 62

	heroCol := colWhite
	if s.HeroGreen {
		heroCol = colGreen
	}
	if s.Health != OK {
		heroCol = s.Health.color()
	}
	hx := text(dst, padL, bandBase, s.Hero, Bold, szHero, heroCol)
	if s.HeroUnit != "" {
		// Unit inline to the right of the number, not on its own line below.
		hx = text(dst, hx+7, bandBase, s.HeroUnit, Regular, szUnit, colDim)
	}

	// corner stats to the right of the hero, bottom-aligned to the band
	cx := hx + 20
	if s.Corner[0] != "" {
		text(dst, cx, bandBase-19, s.Corner[0], Medium, szLabel, colDim)
	}
	if s.Corner[1] != "" {
		text(dst, cx, bandBase, s.Corner[1], Medium, szLabel, colDim)
	}

	// aux block on the right: big value over its label, bottom-aligned
	auxCol := colGreen
	if s.Health != OK && !s.AuxGreen {
		auxCol = s.Health.color()
	}
	if s.Aux != "" {
		valW := textWidth(s.Aux, SemiBold, szAux)
		unitW := 0
		if s.AuxUnit != "" {
			unitW = textWidth(" "+s.AuxUnit, Regular, szUnit)
		}
		start := rightX - valW - unitW
		text(dst, start, bandBase-16, s.Aux, SemiBold, szAux, auxCol)
		if s.AuxUnit != "" {
			text(dst, start+valW, bandBase-16, " "+s.AuxUnit, Regular, szUnit, colDim)
		}
	}
	if s.AuxLabel != "" {
		textRight(dst, rightX, bandBase, s.AuxLabel, Medium, szLabel, colDim)
	}

	// --- graph: a modest band, secondary to the hero/aux ---
	drawSpark(dst, padL, 76, W-padL, 36, s.Spark, ident)

	// --- bottom status row: sits low so the text bottoms hug the bottom edge ---
	const yBot = 138
	statusL := s.StatusL
	statusLCol := ident
	if s.Health != OK && s.Fault != "" {
		statusL = s.Fault
	}
	if statusL != "" {
		text(dst, padL, yBot, statusL, Medium, szLabel, statusLCol)
	}
	drawVitals(dst, rightX, yBot, szLabel, s)
}

// drawVitals lays out the bottom-right vitals as icon+value segments (cpu/wifi/
// fan) followed by uptime as plain text, right-aligned to rightEdge.
func drawVitals(dst *image.RGBA, rightEdge, baseline int, size float64, s Screen) {
	type seg struct {
		ic  *icon
		txt string
	}
	var segs []seg
	if s.CPU != "" {
		segs = append(segs, seg{&iconCPU, s.CPU})
	}
	if s.Wifi != "" {
		segs = append(segs, seg{&iconWifi, s.Wifi})
	}
	if s.Fan != "" {
		segs = append(segs, seg{&iconFan, s.Fan})
	}
	if s.Up != "" {
		segs = append(segs, seg{nil, s.Up})
	}
	if len(segs) == 0 {
		return
	}

	const iconGap = 4 // between an icon and its value
	const segGap = 13 // between segments

	total := segGap * (len(segs) - 1)
	widths := make([]int, len(segs))
	for i, sg := range segs {
		w := textWidth(sg.txt, Medium, size)
		if sg.ic != nil {
			w += sg.ic.w + iconGap
		}
		widths[i] = w
		total += w
	}

	x := rightEdge - total
	for _, sg := range segs {
		if sg.ic != nil {
			// Center the icon on the digits' cap band (~baseline-5).
			sg.ic.draw(dst, x, baseline-5-sg.ic.h/2, colDim)
			x += sg.ic.w + iconGap
		}
		x = text(dst, x, baseline, sg.txt, Medium, size, colDim)
		x += segGap
	}
}

func fillRect(dst *image.RGBA, x, y, w, h int, c interface{ RGBA() (r, g, b, a uint32) }) {
	rect := image.Rect(x, y, x+w, y+h).Intersect(dst.Bounds())
	draw.Draw(dst, rect, image.NewUniform(c), rect.Min, draw.Src)
}
