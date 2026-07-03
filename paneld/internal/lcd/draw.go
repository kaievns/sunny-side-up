package lcd

import (
	"image"
	"image/color"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// Framebuffer is an in-memory RGBA image the size of the display. Drawing
// happens here with the standard library, then Bytes565 converts it to the
// packed RGB565 the NV3007 expects, and (*NV3007).Blit streams it out.
type Framebuffer struct {
	*image.RGBA
	W, H int
}

// NewFramebuffer allocates a w x h framebuffer, cleared to black.
func NewFramebuffer(w, h int) *Framebuffer {
	return &Framebuffer{RGBA: image.NewRGBA(image.Rect(0, 0, w, h)), W: w, H: h}
}

// Fill paints the whole framebuffer one colour.
func (fb *Framebuffer) Fill(c color.Color) {
	r, g, b, a := c.RGBA()
	px := color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
	for i := 0; i < len(fb.Pix); i += 4 {
		fb.Pix[i+0] = px.R
		fb.Pix[i+1] = px.G
		fb.Pix[i+2] = px.B
		fb.Pix[i+3] = px.A
	}
}

// FillRect paints a rectangle.
func (fb *Framebuffer) FillRect(x, y, w, h int, c color.Color) {
	rect := image.Rect(x, y, x+w, y+h).Intersect(fb.Bounds())
	src := image.NewUniform(c)
	for yy := rect.Min.Y; yy < rect.Max.Y; yy++ {
		for xx := rect.Min.X; xx < rect.Max.X; xx++ {
			fb.Set(xx, yy, src.C)
		}
	}
}

// LineHeight is the vertical advance of the built-in 7x13 font.
const LineHeight = 13

// Text draws a string with the built-in 7x13 bitmap font. (x, yTop) is the
// top-left of the first glyph cell. Returns the y for the next line.
func (fb *Framebuffer) Text(x, yTop int, s string, c color.Color) int {
	face := basicfont.Face7x13
	d := &font.Drawer{
		Dst:  fb.RGBA,
		Src:  image.NewUniform(c),
		Face: face,
		Dot:  fixed.P(x, yTop+face.Ascent),
	}
	d.DrawString(s)
	return yTop + LineHeight
}

// Bytes565 packs the framebuffer into big-endian RGB565, row-major, ready for a
// RAM write to the controller (2 bytes per pixel, high byte first).
func (fb *Framebuffer) Bytes565() []byte {
	out := make([]byte, 0, fb.W*fb.H*2)
	for i := 0; i < len(fb.Pix); i += 4 {
		r := fb.Pix[i+0]
		g := fb.Pix[i+1]
		b := fb.Pix[i+2]
		v := (uint16(r&0xF8) << 8) | (uint16(g&0xFC) << 3) | (uint16(b) >> 3)
		out = append(out, byte(v>>8), byte(v&0xFF))
	}
	return out
}

// Handy colours.
var (
	Black  = color.RGBA{0, 0, 0, 255}
	White  = color.RGBA{255, 255, 255, 255}
	Red    = color.RGBA{255, 0, 0, 255}
	Green  = color.RGBA{0, 255, 0, 255}
	Blue   = color.RGBA{0, 0, 255, 255}
	Yellow = color.RGBA{255, 255, 0, 255}
	Cyan   = color.RGBA{0, 255, 255, 255}
	Orange = color.RGBA{255, 140, 0, 255}
)
