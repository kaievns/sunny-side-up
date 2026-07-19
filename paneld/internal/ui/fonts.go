// Package ui renders the panel screens: the "Beacon" node-status layout the
// project uses to show each router's health on the 428x142 LCD. It draws into a
// standard *image.RGBA (which lcd.Blit then pushes to the panel), so screens can
// also be rendered to PNG on a dev machine for previewing.
package ui

import (
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// IBM Plex Sans (SIL OFL 1.1) - a clean sans that stays crisp at the panel's
// sizes (no slab terminals). See assets/fonts/OFL.txt.
//
//go:embed assets/fonts/IBMPlexSans-Regular.ttf
var ttfRegular []byte

//go:embed assets/fonts/IBMPlexSans-Medium.ttf
var ttfMedium []byte

//go:embed assets/fonts/IBMPlexSans-SemiBold.ttf
var ttfSemiBold []byte

//go:embed assets/fonts/IBMPlexSans-Bold.ttf
var ttfBold []byte

// Weight selects a font weight.
type Weight int

const (
	Regular Weight = iota
	Medium
	SemiBold
	Bold
)

var parsedFonts map[Weight]*sfnt.Font

func init() {
	parsedFonts = map[Weight]*sfnt.Font{}
	for w, b := range map[Weight][]byte{Regular: ttfRegular, Medium: ttfMedium, SemiBold: ttfSemiBold, Bold: ttfBold} {
		f, err := opentype.Parse(b)
		if err != nil {
			panic("ui: parse embedded font: " + err.Error())
		}
		parsedFonts[w] = f
	}
}

type faceKey struct {
	w  Weight
	px float64
}

var (
	faceMu    sync.Mutex
	faceCache = map[faceKey]font.Face{}
)

// face returns a cached font.Face for the weight at px pixels (DPI 72 => size in
// pixels), full-hinted for crispness at the panel's small sizes.
func face(w Weight, px float64) font.Face {
	faceMu.Lock()
	defer faceMu.Unlock()
	k := faceKey{w, px}
	if f, ok := faceCache[k]; ok {
		return f
	}
	f, err := opentype.NewFace(parsedFonts[w], &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic("ui: new face: " + err.Error())
	}
	faceCache[k] = f
	return f
}

// text draws s left-aligned with its baseline at (x, y) and returns the x
// coordinate just past the drawn string.
func text(dst draw.Image, x, y int, s string, w Weight, px float64, col color.Color) int {
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(col), Face: face(w, px), Dot: fixed.P(x, y)}
	d.DrawString(s)
	return d.Dot.X.Round()
}

// textRight draws s so its right edge is at x=right, baseline y. Returns the
// left x where drawing started.
func textRight(dst draw.Image, right, y int, s string, w Weight, px float64, col color.Color) int {
	left := right - textWidth(s, w, px)
	text(dst, left, y, s, w, px, col)
	return left
}

// textWidth measures the advance width of s.
func textWidth(s string, w Weight, px float64) int {
	return font.MeasureString(face(w, px), s).Round()
}

// textTracked draws s like text but with extra letter-spacing (track pixels
// added between glyphs). The design tracks the role word (0.04em) and the
// interface indicators (0.03em); the hero number is tracked negative.
func textTracked(dst draw.Image, x, y int, s string, w Weight, px, track float64, col color.Color) int {
	f := face(w, px)
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(col), Face: f, Dot: fixed.P(x, y)}
	t := fixed.Int26_6(track * 64)
	first := true
	for _, r := range s {
		if !first {
			d.Dot.X += t
		}
		first = false
		d.DrawString(string(r))
	}
	return d.Dot.X.Round()
}

