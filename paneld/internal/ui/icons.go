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

// Vitals icons per the 2026-07-19 design spec: SoC chip, wifi and clock are
// 12x12 outlined forms, the fan is a 13x13 rounded housing with filled clover
// blades (matched against the docs/design/ mock crops).
var (
	// CPU: an outlined die with two pins per side and a hollow center ring.
	iconCPU = mkIcon(
		"    #  #    ",
		" ########## ",
		" #        # ",
		" #        # ",
		"##  ####  ##",
		" #  #  #  # ",
		" #  #  #  # ",
		"##  ####  ##",
		" #        # ",
		" #        # ",
		" ########## ",
		"    #  #    ",
	)
	// Wi-Fi: two broadcast arcs over a dot (bottom-weighted like the design -
	// the dot sits on the strip text's baseline rows).
	iconWifi = mkIcon(
		"            ",
		"            ",
		"            ",
		"   ######   ",
		" ##      ## ",
		"            ",
		"    ####    ",
		"  ##    ##  ",
		"            ",
		"            ",
		"     ##     ",
		"     ##     ",
	)
	// Fan: four filled blades around a hub (an X of dark gaps between them),
	// inside a rounded housing.
	iconFan = mkIcon(
		"  #########  ",
		" #         # ",
		"#           #",
		"#  ### ###  #",
		"#  ### ###  #",
		"#  ### ###  #",
		"#     #     #",
		"#  ### ###  #",
		"#  ### ###  #",
		"#  ### ###  #",
		"#           #",
		" #         # ",
		"  #########  ",
	)
	// Clock: a dial with hands at twelve and three (uptime). One row shorter
	// than its box so it rides the strip's optical center like the design.
	iconClock = mkIcon(
		"            ",
		"    ####    ",
		"  ##    ##  ",
		" #        # ",
		" #   #    # ",
		"#    #     #",
		"#    #     #",
		"#    ####  #",
		"#          #",
		" #        # ",
		"  ##    ##  ",
		"    ####    ",
	)
)
