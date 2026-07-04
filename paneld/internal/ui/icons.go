package ui

import (
	"image"
	"image/color"
)

// icon is a small pixel-art glyph drawn 1:1 on the panel (no antialiasing, so it
// stays crisp). Rows are equal-length strings; any non-space rune is an on pixel.
type icon struct {
	w, h int
	rows []string
}

func mkIcon(rows ...string) icon {
	w := 0
	for _, r := range rows {
		if len(r) > w {
			w = len(r)
		}
	}
	return icon{w: w, h: len(rows), rows: rows}
}

// draw blits the icon with its top-left at (x, y).
func (ic icon) draw(dst *image.RGBA, x, y int, c color.RGBA) {
	for ry, row := range ic.rows {
		for rx, ch := range row {
			if ch != ' ' {
				setPx(dst, x+rx, y+ry, c)
			}
		}
	}
}

// Vitals icons, ~9px. Kept as simple, solid silhouettes so they read at panel
// size (fine internal detail just turns to mush this small).
var (
	// CPU: a solid die with two pins on each side.
	iconCPU = mkIcon(
		"  #   #  ",
		" ####### ",
		" ####### ",
		"#########",
		" ####### ",
		"#########",
		" ####### ",
		" ####### ",
		"  #   #  ",
	)
	// Wi-Fi: two broadcast arcs over a dot.
	iconWifi = mkIcon(
		"  #####  ",
		" #     # ",
		"   ###   ",
		"  #   #  ",
		"         ",
		"    #    ",
	)
	// Fan: a solid disc with swept blade gaps (a spinning look).
	iconFan = mkIcon(
		"  #####  ",
		" ####### ",
		"###  ####",
		"## ##  ##",
		"## ### ##",
		"##  ## ##",
		"####  ###",
		" ####### ",
		"  #####  ",
	)
)
