// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley

package oksvg

import (
	"image"

	"github.com/srwiley/rasterx"
)

// Icon is a parsed SVG document.
type Icon struct {
	ViewBox Box

	grads     map[string]*rasterx.Gradient
	paths     []svgPath
	pathScale float64 // path units per user unit
}

// Box is a rectangle with its origin at (X, Y).
type Box struct{ X, Y, W, H float64 }

// Draw draws the icon into img, whose origin must be at (0, 0), mapping its
// user space through t. It does not modify the icon, so an icon can be drawn
// concurrently.
func (icon *Icon) Draw(img *image.RGBA, t rasterx.Matrix2D) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	r := &renderer{Dasher: rasterx.NewDasher(w, h, rasterx.NewScannerGV(w, h, img, img.Rect)), img: img}
	for i := range icon.paths {
		icon.paths[i].draw(r, t, icon.pathScale, icon.grads)
	}
}

// renderer draws paths into an image.
type renderer struct {
	*rasterx.Dasher
	img     *image.RGBA
	evenOdd *rasterx.Filler // made when needed
}

// filler returns a filler with the given fill rule.
func (r *renderer) filler(evenOdd bool) *rasterx.Filler {
	if !evenOdd {
		return &r.Filler
	}
	if r.evenOdd == nil {
		w, h := r.img.Rect.Dx(), r.img.Rect.Dy()
		r.evenOdd = rasterx.NewFiller(w, h, newEvenOddScanner(r.img))
	}
	return r.evenOdd
}
