package ui

import (
	"image"
	"image/draw"
)

// RenderSplash draws the boot screen: the node's identity in the beacon's
// frame with a quiet "starting" note. It is blitted the moment the panel
// link is up - before the first metric sample exists - so the device shows
// life during boot instead of a dead black glass. The bar carries the role's
// identity color (health is unknown yet).
func RenderSplash(dst *image.RGBA, role Role, location string) {
	draw.Draw(dst, dst.Bounds(), image.NewUniform(colBG), image.Point{}, draw.Src)

	drawGlow(dst, role.base())
	fillRect(dst, 0, barTop, barW, barBot-barTop, role.base())

	x := textTracked(dst, padL, 13, role.label(), Bold, 14, 0.56, role.base())
	if location != "" {
		text(dst, x+7, 13, location, Medium, 14, colText)
	}
	text(dst, padL, 78, "starting…", Regular, 14, colDim)
}
