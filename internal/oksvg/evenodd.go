// Copyright 2026 The ebitsvg Authors. All rights reserved.

package oksvg

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/fixed"
)

// evenOddScanner is a rasterx.Scanner that fills with the even-odd rule,
// which rasterx.ScannerGV ignores. Like golang.org/x/image/vector, it
// accumulates the signed area that edges cover in each pixel, left to right,
// and then folds the accumulated winding into coverage in [0, 1].
type evenOddScanner struct {
	dst        *image.RGBA // with its origin at (0, 0)
	src        image.Image
	w, h       int
	cover      []float32 // per pixel, the change in winding from the pixel before
	mask       *image.Alpha
	minY, maxY int // the rows with cover, if minY < maxY
	start, pen [2]float32
	started    bool
	uniform    image.Uniform
	funcImage  funcImage
}

func newEvenOddScanner(dst *image.RGBA) *evenOddScanner {
	s := &evenOddScanner{dst: dst}
	s.SetBounds(dst.Rect.Dx(), dst.Rect.Dy())
	return s
}

func (s *evenOddScanner) SetBounds(w, h int) {
	s.w, s.h = w, h
	// The extra element takes cover beyond the end of the last row.
	s.cover = make([]float32, w*h+1)
	s.mask = image.NewAlpha(image.Rect(0, 0, w, h))
	s.minY, s.maxY = h, 0
}

func (s *evenOddScanner) SetColor(c any) {
	switch c := c.(type) {
	case color.Color:
		s.uniform.C = c
		s.src = &s.uniform
	case rasterx.ColorFunc:
		s.funcImage.f = c
		s.src = &s.funcImage
	}
}

func (s *evenOddScanner) SetWinding(bool)                    {}
func (s *evenOddScanner) SetClip(image.Rectangle)            {}
func (s *evenOddScanner) GetPathExtent() fixed.Rectangle26_6 { return fixed.Rectangle26_6{} }

func (s *evenOddScanner) Clear() {
	if s.minY < s.maxY {
		clear(s.cover[s.minY*s.w : s.maxY*s.w+1])
	}
	s.minY, s.maxY = s.h, 0
	s.started = false
}

func (s *evenOddScanner) Start(a fixed.Point26_6) {
	s.close()
	s.start = [2]float32{float32(a.X) / 64, float32(a.Y) / 64}
	s.pen = s.start
	s.started = true
}

func (s *evenOddScanner) Line(b fixed.Point26_6) {
	p := [2]float32{float32(b.X) / 64, float32(b.Y) / 64}
	s.line(s.pen, p)
	s.pen = p
}

// close closes the current subpath, as filling does implicitly.
func (s *evenOddScanner) close() {
	if s.started {
		s.line(s.pen, s.start)
		s.pen = s.start
	}
}

// line adds the cover of the edge from a to b.
func (s *evenOddScanner) line(a, b [2]float32) {
	dir := float32(1)
	if a[1] > b[1] {
		dir, a, b = -1, b, a
	}
	if a[1] == b[1] {
		return
	}
	dxdy := (b[0] - a[0]) / (b[1] - a[1])
	y0 := max(int(math.Floor(float64(a[1]))), 0)
	y1 := min(int(math.Ceil(float64(b[1]))), s.h)
	for y := y0; y < y1; y++ {
		top, bottom := max(float32(y), a[1]), min(float32(y+1), b[1])
		xt, xb := a[0]+(top-a[1])*dxdy, a[0]+(bottom-a[1])*dxdy
		s.addPiece(s.cover[y*s.w:y*s.w+s.w+1], min(xt, xb), max(xt, xb), (bottom-top)*dir)
	}
	s.minY, s.maxY = min(s.minY, y0), max(s.maxY, y1)
}

// addPiece adds the cover of an edge that spans x0 to x1 within a row, and
// whose height, signed by its direction, is d. Each pixel gets the area of
// the piece's height to its right: for a part of the piece within a pixel,
// the height of the part times the distance of its mean x from the pixel's
// right edge; the next pixel gets the rest.
func (s *evenOddScanner) addPiece(row []float32, x0, x1, d float32) {
	w := float32(s.w)
	if x1-x0 < 1e-6 {
		x := min(max(x0, 0), w)
		i := min(int(x), s.w-1)
		m := x - float32(i)
		row[i] += d * (1 - m)
		row[i+1] += d * m
		return
	}
	k := d / (x1 - x0) // height per unit of x
	if x0 < 0 {
		row[0] += k * (min(x1, 0) - x0)
		x0 = 0
	}
	if x1 > w {
		row[s.w] += k * (x1 - max(x0, w))
		x1 = w
	}
	for x := x0; x < x1; {
		i := int(x)
		next := min(float32(i+1), x1)
		m := (x+next)/2 - float32(i)
		h := k * (next - x)
		row[i] += h * (1 - m)
		row[i+1] += h * m
		x = next
	}
}

func (s *evenOddScanner) Draw() {
	s.close()
	s.started = false
	if s.minY >= s.maxY {
		return
	}
	var acc float32
	for i := s.minY * s.w; i < s.maxY*s.w; i++ {
		acc += s.cover[i]
		a := float32(math.Mod(math.Abs(float64(acc)), 2))
		if a > 1 {
			a = 2 - a
		}
		s.mask.Pix[i] = uint8(a*0xff + 0.5)
	}
	r := image.Rect(0, s.minY, s.w, s.maxY)
	draw.DrawMask(s.dst, r, s.src, r.Min, s.mask, r.Min, draw.Over)
}

// funcImage is an image of unbounded size whose colors are given by a
// function.
type funcImage struct{ f rasterx.ColorFunc }

func (f *funcImage) ColorModel() color.Model { return color.RGBAModel }
func (f *funcImage) Bounds() image.Rectangle { return image.Rect(-1e9, -1e9, 1e9, 1e9) }
func (f *funcImage) At(x, y int) color.Color { return f.f(x, y) }
