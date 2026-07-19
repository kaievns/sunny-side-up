package ui

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
)

// Panel dimensions (landscape).
const (
	W = 428
	H = 142

	// Frame per the 2026-07-19 design spec: a 5px status bar inset 2px from
	// the top/bottom edges, content from x15, right-flush runs at x428.
	barW   = 5
	barTop = 2
	barBot = H - 2
	padL   = 15
	rightX = W
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

	Corner [2]string // two small stacked lines: "↑ 68 Mb/s", "46 clients"

	Aux      string // big right value: latency "6.4", or "FAIL"
	AuxUnit  string // small unit after it: "ms" (empty for FAIL/LOST)
	AuxLabel string // dim line under aux: "dns 4.2 · loss 0%"
	AuxGreen bool   // keep aux green even when the node is unhealthy (metric itself is fine)

	Spark []float64 // throughput samples in 0..1

	// Ifaces are the bottom-left interface indicators - small state-colored
	// labels, typically WAN · LAN · 5GHz · 2.4GHz.
	Ifaces []Iface

	// Fault is the plain-language failure line ("WAN down · no internet"). When
	// the node is unhealthy it takes over the aux block's label slot - that
	// block is the "how is the link doing" corner - replacing AuxLabel.
	Fault string

	// Bottom-right vitals, each drawn with its icon (empty ones are skipped):
	CPU  string // cpu temperature, e.g. "52°"
	Wifi string // wifi module temperature, e.g. "48°"
	Fan  string // fan speed, e.g. "3200"
	Up   string // uptime, e.g. "14d"
}

// Iface is one bottom-left interface indicator.
type Iface struct {
	Label string // "WAN", "LAN", "5GHz", "2.4GHz"
	State Health // OK / Degraded / Down - drives the label color
}

// NewImage returns a blank panel-sized image.
func NewImage() *image.RGBA { return image.NewRGBA(image.Rect(0, 0, W, H)) }

// Render draws the screen into dst (which must be W x H). Geometry and type
// scale follow the 2026-07-19 design spec, baselines verified against the 2x
// mock crops in docs/design/.
func Render(dst *image.RGBA, s Screen) {
	draw.Draw(dst, dst.Bounds(), image.NewUniform(colBG), image.Point{}, draw.Src)

	// Status bar: pure device health, with its soft glow behind it.
	drawGlow(dst, s.Health.color())
	fillRect(dst, 0, barTop, barW, barBot-barTop, s.Health.color())

	// The role's identity color, falling back to the fault color when
	// unhealthy - drives the sparkline.
	ident := accent(s.Role, s.Health)

	// Type scale (design spec, 1:1 px).
	const (
		szTop    = 14 // role (Bold, tracked) + location (Medium)
		szTopNum = 13 // IP + clock
		szHero   = 54 // big number (Bold, tightened)
		szUnit   = 14 // "Mb/s" beside the hero
		szMid    = 14 // up/clients stack
		szAux    = 26 // numeric ping (SemiBold)
		szAuxUn  = 13 // " ms" after it
		szAuxBig = 28 // FAIL / LOST (Bold)
		szSub    = 13 // line under the ping
		szInd    = 11 // interface indicators (SemiBold, tracked)
		szVit    = 12 // vitals values
	)

	// --- header row, flush to the top edge: baseline 13, gaps 7 ---
	x := textTracked(dst, padL, 13, s.Role.label(), Bold, szTop, 0.56, s.Role.base())
	if s.Location != "" {
		x = text(dst, x+7, 13, s.Location, Medium, szTop, colText)
	}
	if s.IP != "" {
		text(dst, x+7, 13, s.IP, Regular, szTopNum, colDimmer)
	}
	if s.Clock != "" {
		textRight(dst, rightX, 13, s.Clock, Regular, szTopNum, colDim)
	}

	// --- hero row: number baseline 61, unit on the same baseline 6 after ---
	heroCol := colWhite
	if s.HeroGreen {
		heroCol = colGreen
	}
	if s.Health != OK {
		heroCol = s.Health.color()
	}
	heroEnd := textTracked(dst, padL, 61, s.Hero, Bold, szHero, -1.08, heroCol)
	unitEnd := heroEnd
	if s.HeroUnit != "" {
		unitEnd = text(dst, heroEnd+6, 61, s.HeroUnit, Regular, szUnit, colDim)
	}

	// up/clients stack, 14 past the unit: two left-aligned mixed-weight lines
	// (values SemiBold white, arrows/labels regular gray), baselines 41 + 59.
	drawRuns(dst, unitEnd+14, 41, s.Corner[0], szMid)
	drawRuns(dst, unitEnd+14, 59, s.Corner[1], szMid)

	// --- ping block, right-flush. Numeric mode: SemiBold 26 at baseline 40
	// with " ms" trailing; state words (FAIL/LOST) go Bold 28 at baseline 43.
	// The sub line runs 4px under either (baseline 60 / 62). ---
	auxCol := colGreen
	if s.Health != OK && !s.AuxGreen {
		auxCol = s.Health.color()
	}
	subY := 60
	if s.Aux != "" {
		if s.AuxUnit != "" {
			valW := textWidth(s.Aux, SemiBold, szAux)
			unitW := textWidth(" "+s.AuxUnit, Regular, szAuxUn)
			start := rightX - valW - unitW
			text(dst, start, 40, s.Aux, SemiBold, szAux, auxCol)
			text(dst, start+valW, 40, " "+s.AuxUnit, Regular, szAuxUn, colDim)
		} else {
			textRight(dst, rightX, 43, s.Aux, Bold, szAuxBig, auxCol)
			subY = 62
		}
	}
	subLine, subCol := s.AuxLabel, colDim
	if s.Health != OK && s.Fault != "" {
		subLine, subCol = s.Fault, s.Health.color()
	}
	if subLine != "" {
		textRight(dst, rightX, subY, subLine, Regular, szSub, subCol)
	}

	// --- graph band y67..124, full-bleed from the bar to the right edge:
	// gridlines at 1/3 and 2/3 of the band behind the spark. ---
	for _, gy := range [...]int{86, 105} {
		for gx := barW; gx < W; gx++ {
			setPx(dst, gx, gy, colGrid)
		}
	}
	drawSpark(dst, barW, W-barW, 67, 124, s.Spark, ident)

	// --- bottom strip, flush to the bottom edge: baseline 140 ---
	drawIfaces(dst, padL, 140, szInd, s.Ifaces)
	drawVitals(dst, rightX, 140, szVit, s)
}

// drawGlow paints the status bar's halo (the design's `box-shadow 0 0 9px`):
// the bar rect gaussian-blurred (sigma = blur/2) in the same color, drawn
// before the solid bar so the bar core stays pure.
func drawGlow(dst *image.RGBA, c color.RGBA) {
	const (
		sigma    = 4.5
		strength = 1.0 // box-shadow blurs the full-opacity color
	)
	cov := func(a, b, p float64) float64 {
		k := sigma * math.Sqrt2
		return 0.5 * (math.Erf((b-p)/k) - math.Erf((a-p)/k))
	}
	for y := 0; y < H; y++ {
		cy := cov(barTop, barBot, float64(y)+0.5)
		for x := 0; x < barW+18; x++ {
			a := strength * cy * cov(0, barW, float64(x)+0.5)
			if a < 0.004 {
				continue
			}
			setPx(dst, x, y, blend(c, colBG, a))
		}
	}
}

// drawRuns draws a line of space-separated tokens in mixed weight: tokens
// beginning with a digit render semibold-white (the values), everything else
// in label gray.
func drawRuns(dst *image.RGBA, x, baseline int, line string, size float64) {
	sp := textWidth(" ", Regular, size)
	for i, tok := range strings.Fields(line) {
		if i > 0 {
			x += sp
		}
		if tok[0] >= '0' && tok[0] <= '9' {
			x = text(dst, x, baseline, tok, SemiBold, size, colWhite)
		} else {
			x = text(dst, x, baseline, tok, Regular, size, colDim)
		}
	}
}

// drawIfaces lays out the bottom-left interface indicators as small tracked
// semibold labels colored by state - green up, amber degraded, red down.
func drawIfaces(dst *image.RGBA, x, baseline int, size float64, ifs []Iface) {
	// The spec gap is 10, but our full-hinted faces round each label's advance
	// up ~1px vs the design's fractional metrics - 9 keeps the measured ink
	// gaps (12/12/11) and the row's total width on the design's pixels.
	const gap = 9
	for _, f := range ifs {
		x = textTracked(dst, x, baseline, f.Label, SemiBold, size, 0.33, f.State.color())
		x += gap
	}
}

// drawVitals lays out the bottom-right vitals as icon+value segments (cpu/
// wifi/fan/uptime), right-aligned to rightEdge. Temps and rpm read brighter
// than the uptime, per the design.
func drawVitals(dst *image.RGBA, rightEdge, baseline int, size float64, s Screen) {
	type seg struct {
		ic  *icon
		txt string
		col color.RGBA
	}
	var segs []seg
	if s.CPU != "" {
		segs = append(segs, seg{&iconCPU, s.CPU, colText})
	}
	if s.Wifi != "" {
		segs = append(segs, seg{&iconWifi, s.Wifi, colText})
	}
	if s.Fan != "" {
		segs = append(segs, seg{&iconFan, s.Fan, colText})
	}
	if s.Up != "" {
		segs = append(segs, seg{&iconClock, s.Up, colDim})
	}
	if len(segs) == 0 {
		return
	}

	const iconGap = 4 // between an icon and its value
	const segGap = 10 // between segments
	const iconY = 129 // icon boxes sit centered in the strip

	total := segGap * (len(segs) - 1)
	widths := make([]int, len(segs))
	for i, sg := range segs {
		w := textWidth(sg.txt, Regular, size)
		if sg.ic != nil {
			w += sg.ic.w + iconGap
		}
		widths[i] = w
		total += w
	}

	x := rightEdge - total
	for _, sg := range segs {
		if sg.ic != nil {
			sg.ic.draw(dst, x, iconY, colDimmer)
			x += sg.ic.w + iconGap
		}
		x = text(dst, x, baseline, sg.txt, Regular, size, sg.col)
		x += segGap
	}
}

func fillRect(dst *image.RGBA, x, y, w, h int, c interface{ RGBA() (r, g, b, a uint32) }) {
	rect := image.Rect(x, y, x+w, y+h).Intersect(dst.Bounds())
	draw.Draw(dst, rect, image.NewUniform(c), rect.Min, draw.Src)
}
