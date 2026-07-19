package ui

import (
	"image"
	"image/color"
)

// drawSpark renders the throughput sparkline per the design spec: samples in
// 0..1 map into the graph band (1 -> top, 0 -> zeroY), the ~1.5px line is
// drawn in the accent color, and the area fill is a vertical gradient of the
// line color from 34% opacity under the line to 4% at the band's bottom edge.
func drawSpark(dst *image.RGBA, x, w, top, zeroY int, data []float64, line color.RGBA) {
	if len(data) < 2 || w < 2 || zeroY <= top {
		return
	}
	span := float64(zeroY - top)

	for i := 0; i < w; i++ {
		t := float64(i) / float64(w-1)
		v := clamp01(sampleAt(data, t))
		ly := zeroY - int(v*span+0.5)

		for yy := ly; yy <= zeroY; yy++ {
			frac := 0.0
			if zeroY > ly {
				frac = float64(yy-ly) / float64(zeroY-ly)
			}
			a := 0.34 - 0.30*frac
			setPx(dst, x+i, yy, blend(line, colBG, a))
		}
		// The 2px line on top, with soft 1px edges so it reads like the
		// design's antialiased 1.5px stroke rather than a hard bitmap line.
		setPx(dst, x+i, ly-1, blend(line, colBG, 0.35))
		setPx(dst, x+i, ly, line)
		if ly+1 <= zeroY+1 {
			setPx(dst, x+i, ly+1, line)
		}
		if ly+2 <= zeroY+1 {
			setPx(dst, x+i, ly+2, blend(line, colBG, 0.45))
		}
	}
}

// sampleAt linearly interpolates data (indexed 0..len-1) at fractional position
// t in [0,1].
func sampleAt(data []float64, t float64) float64 {
	if t <= 0 {
		return data[0]
	}
	if t >= 1 {
		return data[len(data)-1]
	}
	p := t * float64(len(data)-1)
	i := int(p)
	frac := p - float64(i)
	return data[i]*(1-frac) + data[i+1]*frac
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

func setPx(dst *image.RGBA, x, y int, c color.RGBA) {
	if !(image.Point{x, y}).In(dst.Bounds()) {
		return
	}
	dst.SetRGBA(x, y, c)
}
