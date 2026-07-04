package ui

import (
	"image"
	"image/color"
)

// drawSpark renders a smooth throughput sparkline in the rect (x,y,w,h): a
// filled area that fades from the line color down to the background, with a
// bright line on top. data holds samples in 0..1 (0 = bottom, 1 = top); it is
// linearly resampled to the pixel width.
func drawSpark(dst *image.RGBA, x, y, w, h int, data []float64, line color.RGBA) {
	if len(data) < 2 || w < 2 || h < 2 {
		return
	}
	bottom := y + h - 1
	span := float64(h - 1)

	for i := 0; i < w; i++ {
		t := float64(i) / float64(w-1)
		v := clamp01(sampleAt(data, t))
		top := bottom - int(v*span+0.5)

		// Area fill: brightest just under the line, fading to the background at
		// the bottom of the band (band-relative gradient for a consistent look).
		for yy := top; yy <= bottom; yy++ {
			frac := float64(bottom-yy) / span // 1 near top of band, 0 at bottom
			a := 0.10 + 0.40*frac
			setPx(dst, x+i, yy, blend(line, colBG, a))
		}
		// The line itself: a solid 2px full-brightness stroke so it reads
		// through the tint.
		setPx(dst, x+i, top, line)
		if top+1 <= bottom {
			setPx(dst, x+i, top+1, line)
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
