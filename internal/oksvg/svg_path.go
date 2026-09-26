// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley

package oksvg

import (
	"image/color"
	"math"
	"slices"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/fixed"
)

// maxLineWidth bounds stroke widths, in raster pixels, to keep them and
// their miter limits (see maxMiterLimit) in rasterx's fixed-point range.
const maxLineWidth = 1 << 14

// svgPath is a path and the style it is drawn with.
type svgPath struct {
	pathStyle
	path rasterx.Path
}

// draw draws the path, which is scaled by scale, into r, mapping the root's
// user space through t.
func (svgp *svgPath) draw(r *renderer, t rasterx.Matrix2D, scale float64, grads map[string]*rasterx.Gradient) {
	m := t.Mult(svgp.transform)
	pm := m.Scale(1/scale, 1/scale) // from path units
	if svgp.fill != nil {
		if clr, ok := svgp.paint(svgp.fill, svgp.fillOpacity, m, scale, grads); ok {
			rf := r.filler(svgp.evenOdd)
			rf.Clear()
			svgp.path.AddTo(&rasterx.MatrixAdder{Adder: rf, M: pm})
			rf.SetColor(clr)
			rf.Draw()
		}
	}
	if svgp.stroke != nil && svgp.lineWidth > 0 {
		clr, ok := svgp.paint(svgp.stroke, svgp.lineOpacity, m, scale, grads)
		if !ok {
			return
		}
		// rasterx strokes in raster pixels, so the stroke geometry is scaled
		// by the transform's mean scale factor.
		k := math.Sqrt(math.Abs(m.A*m.D - m.B*m.C))
		dash, offset := scaleDash(svgp.dash, svgp.dashOffset, k)
		r.Clear()
		r.SetStroke(fixed.Int26_6(min(svgp.lineWidth*k, maxLineWidth)*64), fixed.Int26_6(svgp.miterLimit*64),
			svgp.lineCap, nil, nil, svgp.lineJoin, dash, offset)
		svgp.path.AddTo(&rasterx.MatrixAdder{Adder: r, M: pm})
		r.SetColor(clr)
		r.Draw()
	}
}

// paint returns the rasterx color of p for the path drawn with its user
// space mapped through m, or false if it paints nothing.
func (svgp *svgPath) paint(p *paint, opacity float64, m rasterx.Matrix2D, scale float64, grads map[string]*rasterx.Gradient) (any, bool) {
	opacity *= svgp.opacity
	if g := grads[p.grad]; p.grad != "" && g != nil {
		return svgp.gradient(g, opacity, m, scale)
	}
	return withOpacity(p.color, opacity)
}

func withOpacity(c color.NRGBA, opacity float64) (color.NRGBA, bool) {
	c.A = uint8(math.Round(float64(c.A) * opacity))
	return c, c.A > 0
}

// gradient returns the color function of g for the path drawn with its
// user space mapped through m.
func (svgp *svgPath) gradient(g *rasterx.Gradient, opacity float64, m rasterx.Matrix2D, scale float64) (any, bool) {
	switch len(g.Stops) {
	case 0:
		return nil, false
	case 1:
		return stopColor(g.Stops[0], opacity)
	}
	if g.Units == rasterx.ObjectBoundingBox {
		b := bounds(svgp.path)
		if !(b.W > 0 && b.H > 0) {
			return nil, false
		}
		m = m.Translate(b.X/scale, b.Y/scale).Scale(b.W/scale, b.H/scale)
	}
	m = m.Mult(g.Matrix)
	p := g.Points
	if m.A*m.D-m.B*m.C == 0 || g.IsRadial && !(p[4] > 0) || !g.IsRadial && p[0] == p[2] && p[1] == p[3] {
		return stopColor(g.Stops[len(g.Stops)-1], opacity)
	}
	// rasterx lays out objectBoundingBox gradients through the inverse of
	// their matrix, which with unit bounds maps raster pixels to gradient
	// space under any affine transform.
	lg := *g
	lg.Stops = slices.Clone(g.Stops) // rasterx sorts them in place
	lg.Units, lg.Matrix = rasterx.ObjectBoundingBox, m
	lg.Bounds.X, lg.Bounds.Y, lg.Bounds.W, lg.Bounds.H = 0, 0, 1, 1
	return lg.GetColorFunction(opacity), true
}

func stopColor(s rasterx.GradStop, opacity float64) (color.NRGBA, bool) {
	return withOpacity(s.StopColor.(color.NRGBA), s.Opacity*opacity)
}

// scaleDash returns a dash array and offset scaled by k. The offset is
// reduced to one period of the pattern, which rasterx would otherwise walk
// one dash at a time, and which also makes negative offsets work.
func scaleDash(dash []float64, offset, k float64) ([]float64, float64) {
	if len(dash) == 0 {
		return nil, 0
	}
	scaled := make([]float64, len(dash))
	var period float64
	for i, d := range dash {
		scaled[i] = d * k
		period += scaled[i]
	}
	if len(dash)%2 == 1 {
		period *= 2
	}
	if !(period > 0) || math.IsInf(period, 0) {
		return nil, 0
	}
	offset = math.Mod(offset*k, period)
	if offset < 0 {
		offset += period
	}
	return scaled, offset
}

// bounds returns the bounding box of a path.
func bounds(p rasterx.Path) (b Box) {
	var a bbox
	p.AddTo(&a)
	if !a.started {
		return b
	}
	b.X, b.Y, b.W, b.H = a.minX, a.minY, a.maxX-a.minX, a.maxY-a.minY
	return b
}

// bbox is a rasterx.Adder that accumulates a bounding box, sampling curves.
type bbox struct {
	minX, minY, maxX, maxY float64
	last                   fixed.Point26_6
	started                bool
}

func (b *bbox) add(x, y float64) {
	if !b.started {
		b.minX, b.minY, b.maxX, b.maxY, b.started = x, y, x, y, true
	}
	b.minX, b.minY = min(b.minX, x), min(b.minY, y)
	b.maxX, b.maxY = max(b.maxX, x), max(b.maxY, y)
}

func (b *bbox) Start(a fixed.Point26_6) { b.last = a; b.add(unfix(a)) }
func (b *bbox) Line(a fixed.Point26_6)  { b.Start(a) }
func (b *bbox) Stop(bool)               {}

// curveSamples is the number of segments that curves are sampled in.
const curveSamples = 16

func (b *bbox) QuadBezier(c, d fixed.Point26_6) {
	x0, y0 := unfix(b.last)
	x1, y1 := unfix(c)
	x2, y2 := unfix(d)
	for i := 1; i < curveSamples; i++ {
		t := float64(i) / curveSamples
		s := 1 - t
		b.add(s*s*x0+2*s*t*x1+t*t*x2, s*s*y0+2*s*t*y1+t*t*y2)
	}
	b.Start(d)
}

func (b *bbox) CubeBezier(c1, c2, d fixed.Point26_6) {
	x0, y0 := unfix(b.last)
	x1, y1 := unfix(c1)
	x2, y2 := unfix(c2)
	x3, y3 := unfix(d)
	for i := 1; i < curveSamples; i++ {
		t := float64(i) / curveSamples
		s := 1 - t
		b.add(s*s*s*x0+3*s*s*t*x1+3*s*t*t*x2+t*t*t*x3, s*s*s*y0+3*s*s*t*y1+3*s*t*t*y2+t*t*t*y3)
	}
	b.Start(d)
}

func unfix(p fixed.Point26_6) (x, y float64) {
	return float64(p.X) / 64, float64(p.Y) / 64
}
