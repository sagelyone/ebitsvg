package ebitsvg

import (
	"fmt"
	"image"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
)

const (
	// maxRasterSize is the largest raster side. Larger images fail on some
	// GPUs, and beyond the GPU's limit Ebitengine panics only after seconds
	// of rasterizing.
	maxRasterSize = 4096

	// settleTicks is the number of game ticks a target must persist for an
	// Image to draw it from an exact raster. Draw runs every frame, not every
	// tick, so ticks rather than draws tell a still target from a moving one.
	// Two ticks keep content that moves every tick or every other tick from
	// rasterizing on each move.
	settleTicks = 2

	// graceTicks, a second at the default TPS, is how long an Image keeps
	// the raster for a changing target after settling, and how long a
	// target that changes meanwhile must persist to settle. Content that
	// moves in steps a few ticks apart thus reuses one raster rather than
	// rasterizing twice per step.
	graceTicks = 60

	// rasterMargin is the transparent border, in texels, around a raster made
	// for a changing target, so that linear filtering forms the edges of
	// full-bleed content from the SVG's own antialiasing rather than
	// snapping them to pixel centers. Four texels keep it at least a texel
	// wide at every mipmap level a raster is drawn at.
	rasterMargin = 4

	// subpixels is the precision, per pixel, of exact positions. Rounding to
	// it lets content moved by whole pixels keep its exact raster despite
	// floating-point error.
	subpixels = 256
)

// Fit is how an SVG fills the box it is drawn into.
type Fit uint8

const (
	// Contain scales the SVG uniformly to fit entirely inside the box.
	Contain Fit = iota

	// Cover scales the SVG uniformly to cover the whole box, clipping the
	// overflow.
	Cover

	// Stretch scales each axis independently to fill the box exactly.
	Stretch
)

// DrawOptions configures [Image.Draw]. The zero value draws a centered
// Contain fit, untransformed.
type DrawOptions struct {
	Fit Fit

	// AlignX and AlignY place a Contain or Cover fit within the box, from -1
	// (left or top edge) through 0 (centered) to 1 (right or bottom edge).
	// Values outside that range extrapolate beyond the edges.
	AlignX, AlignY float64

	// GeoM transforms the placed box, as in [ebiten.DrawImageOptions]. Its
	// scale counts towards the displayed size, so zooming in rasterizes at
	// the zoomed size.
	GeoM ebiten.GeoM

	// ColorScale and Blend are passed to [ebiten.Image.DrawImage].
	ColorScale ebiten.ColorScale
	Blend      ebiten.Blend
}

// Image draws an SVG through cached rasters.
//
// An Image first draws a raster made for exactly its target, pixel for
// pixel. While the target changes, it draws from a raster made at twice the
// displayed size, which it reuses until the displayed size halves or
// doubles. Once the same target has persisted for two game ticks (see
// [ebiten.Tick]), it draws an exact raster again. It keeps the other raster
// until the target has persisted for 60 ticks, and a target that changes
// meanwhile waits those 60 ticks for an exact raster, so content moving in
// steps reuses its rasters and a still Image ends up holding one.
//
// An Image caches for one target, so use one Image for each independently
// drawn use of an SVG. An Image is not safe for concurrent use.
type Image struct {
	svg *SVG

	// raster was made at twice the display scale (anchorX, anchorY), with
	// rasterMargin texels around the SVG.
	raster           *ebiten.Image
	anchorX, anchorY float64

	// exact, if not nil, is made for target, which Draw has had since tick
	// since. restless is whether the target changed within graceTicks of
	// settling.
	exact    *ebiten.Image
	target   exactTarget
	since    int64
	restless bool
}

// tick is [ebiten.Tick], replaced by tests, which run within one tick.
var tick = ebiten.Tick

// exactTarget is the rasterize arguments of a raster that maps pixel for
// pixel onto the destination.
type exactTarget struct {
	w, h           int
	sx, sy, dx, dy float64
}

// NewImage returns an Image that draws svg. It panics if svg is nil.
func NewImage(svg *SVG) *Image {
	if svg == nil {
		panic("ebitsvg: NewImage called with nil SVG")
	}
	return &Image{svg: svg}
}

// Draw draws the SVG into dst, fitted to the w×h box at (x, y) and then
// transformed by opts.GeoM. A nil opts is the zero [DrawOptions]. Like
// [ebiten.Image.DrawImage], x and y are in dst's coordinates, which for a
// sub-image are its parent's. A box without a positive, finite size draws
// nothing. Draw panics if opts.Fit is not a valid [Fit].
//
// Sharpness is measured in pixels of dst, so dst must reach the screen
// unscaled; for high-DPI displays, have LayoutF return device pixels
// (see the package example).
//
// Rasters are at most 4096 pixels on a side, so an SVG displayed larger
// than that is magnified and slightly soft.
func (img *Image) Draw(dst *ebiten.Image, x, y, w, h float64, opts *DrawOptions) {
	if opts == nil {
		opts = &DrawOptions{}
	}
	p, ok := img.place(x, y, w, h, opts)
	if !ok {
		return
	}
	op := &ebiten.DrawImageOptions{
		ColorScale: opts.ColorScale,
		Blend:      opts.Blend,
		Filter:     ebiten.FilterLinear,
	}
	if t, px, py, ok := p.exact(); ok {
		fresh := img.raster == nil && img.exact == nil
		now := tick()
		if t != img.target {
			img.setTarget(t, now)
		}
		wait := int64(settleTicks)
		if img.restless {
			wait = graceTicks
		}
		if fresh || img.exact != nil || now-img.since >= wait {
			img.settle(now)
			op.GeoM.Translate(px, py)
			dst.DrawImage(img.exact, op)
			return
		}
	} else {
		img.setTarget(exactTarget{}, 0)
	}

	img.makeRaster(p)
	src, b := img.raster, img.raster.Bounds()
	const m = rasterMargin
	rx, ry := p.sx*p.sw/float64(b.Dx()-2*m), p.sy*p.sh/float64(b.Dy()-2*m)
	if p.fit == Cover {
		// Clipping the source, unlike a sub-image of dst, follows any GeoM.
		b = image.Rect(
			rasterEdge((p.x-p.ox)/rx+m, b.Dx()), rasterEdge((p.y-p.oy)/ry+m, b.Dy()),
			rasterEdge((p.x+p.w-p.ox)/rx+m, b.Dx()), rasterEdge((p.y+p.h-p.oy)/ry+m, b.Dy()),
		)
	}
	if !keepsDst(opts.Blend) {
		b = b.Intersect(src.Bounds().Inset(m))
	}
	if b != src.Bounds() {
		if b.Empty() {
			return
		}
		src = src.SubImage(b).(*ebiten.Image)
	}
	op.GeoM.Scale(rx, ry)
	op.GeoM.Translate(p.ox+float64(b.Min.X-m)*rx, p.oy+float64(b.Min.Y-m)*ry)
	op.GeoM.Concat(opts.GeoM)
	dst.DrawImage(src, op)
}

// keepsDst reports whether b leaves dst unchanged under transparent source
// pixels, so that drawing a raster's margin changes nothing.
func keepsDst(b ebiten.Blend) bool {
	keeps := func(f ebiten.BlendFactor, op ebiten.BlendOperation) bool {
		switch f {
		case ebiten.BlendFactorDefault, ebiten.BlendFactorOne,
			ebiten.BlendFactorOneMinusSourceColor, ebiten.BlendFactorOneMinusSourceAlpha:
			return op == ebiten.BlendOperationAdd || op == ebiten.BlendOperationReverseSubtract ||
				op == ebiten.BlendOperationMax
		}
		return false
	}
	return keeps(b.BlendFactorDestinationRGB, b.BlendOperationRGB) &&
		keeps(b.BlendFactorDestinationAlpha, b.BlendOperationAlpha)
}

// Prepare rasterizes now what [Image.Draw] settles on for the same
// arguments, so that drawing them needs no rasterization. Use it to render
// ahead of time, such as behind a loading screen.
func (img *Image) Prepare(x, y, w, h float64, opts *DrawOptions) {
	if opts == nil {
		opts = &DrawOptions{}
	}
	p, ok := img.place(x, y, w, h, opts)
	if !ok {
		return
	}
	t, _, _, ok := p.exact()
	if !ok {
		img.makeRaster(p)
		return
	}
	now := tick()
	if t != img.target {
		img.setTarget(t, now)
	}
	img.settle(now)
}

// setTarget sets the exact target, first seen at tick since, releasing an
// exact raster made for another target.
func (img *Image) setTarget(t exactTarget, since int64) {
	if t != img.target && img.exact != nil {
		img.exact.Deallocate()
		img.exact = nil
		img.restless = img.raster != nil
	}
	img.target, img.since = t, since
}

// settle ensures that img.exact is made for img.target, and releases the
// raster once the target has persisted for graceTicks at tick now.
func (img *Image) settle(now int64) {
	if img.exact == nil {
		t := img.target
		img.exact = ebiten.NewImageFromImage(img.svg.rasterize(t.w, t.h, t.sx, t.sy, t.dx, t.dy))
	}
	if img.raster != nil && now-img.since >= graceTicks {
		img.raster.Deallocate()
		img.raster = nil
		img.restless = false
	}
}

// makeRaster ensures that img.raster suits the display scale of p.
func (img *Image) makeRaster(p placement) {
	if img.raster != nil {
		fx, fy := p.scaleX/img.anchorX, p.scaleY/img.anchorY
		// Ebitengine chooses the mipmap level by the less shrunk axis, so
		// the axes must not drift apart either.
		if reusable(fx) && reusable(fy) && max(fx, fy)/min(fx, fy) < 2 {
			return
		}
	}
	w, h := p.scaleX*p.sw, p.scaleY*p.sh
	// Clamping both axes by the same factor keeps mipmapping even.
	s := min(2, (maxRasterSize-2*rasterMargin)/max(w, h))
	rw, rh := rasterSide(w*s)+2*rasterMargin, rasterSide(h*s)+2*rasterMargin
	img.anchorX, img.anchorY = p.scaleX, p.scaleY
	if img.raster != nil {
		if img.raster.Bounds().Size() == image.Pt(rw, rh) {
			return
		}
		img.raster.Deallocate()
	}
	sx, sy := float64(rw-2*rasterMargin)/p.sw, float64(rh-2*rasterMargin)/p.sh
	img.raster = ebiten.NewImageFromImage(img.svg.rasterize(rw, rh, sx, sy, rasterMargin, rasterMargin))
}

// reusable reports whether a raster made at twice one display scale suits
// f times that scale, at which it is drawn between a quarter of its size
// and its full size, never magnified.
func reusable(f float64) bool {
	return 0.5 <= f && f <= 2
}

// rasterSide rounds v, less floating-point error, up to a multiple of 4
// that leaves room for the margins within maxRasterSize. Ebitengine halves
// sizes, rounding down, for each mipmap level, so other sizes fade at their
// right and bottom edges when drawn shrunk.
func rasterSide(v float64) int {
	return min(maxRasterSize-2*rasterMargin, max(4, 4*int(math.Ceil(snap(v)/4))))
}

// rasterEdge rounds v to the nearest pixel edge in [0, n].
func rasterEdge(v float64, n int) int {
	return int(min(max(math.Round(v), 0), float64(n)))
}

// placement is a Draw resolved into the box's coordinates.
type placement struct {
	x, y, w, h float64
	fit        Fit
	geoM       ebiten.GeoM
	sw, sh     float64 // SVG size
	sx, sy     float64 // box pixels per SVG unit
	ox, oy     float64 // SVG origin

	// scaleX and scaleY are dst pixels per SVG unit along each SVG axis.
	scaleX, scaleY float64
}

// place resolves a Draw, reporting whether it draws anything.
func (img *Image) place(x, y, w, h float64, opts *DrawOptions) (placement, bool) {
	sw, sh := img.svg.Size()
	sx, sy := w/sw, h/sh
	switch opts.Fit {
	case Contain:
		sx = min(sx, sy)
		sy = sx
	case Cover:
		sx = max(sx, sy)
		sy = sx
	case Stretch:
	default:
		panic(fmt.Sprintf("ebitsvg: invalid Fit %d", opts.Fit))
	}
	g := opts.GeoM
	p := placement{
		x: x, y: y, w: w, h: h,
		fit:  opts.Fit,
		geoM: g,
		sw:   sw, sh: sh,
		sx: sx, sy: sy,
		ox:     x + (w-sw*sx)*(opts.AlignX+1)/2,
		oy:     y + (h-sh*sy)*(opts.AlignY+1)/2,
		scaleX: sx * math.Hypot(g.Element(0, 0), g.Element(1, 0)),
		scaleY: sy * math.Hypot(g.Element(0, 1), g.Element(1, 1)),
	}
	ok := w > 0 && h > 0 && p.scaleX > 0 && p.scaleY > 0 &&
		finite(x, y, w, h, p.ox, p.oy, p.scaleX*sw, p.scaleY*sh, g.Element(0, 2), g.Element(1, 2))
	return p, ok
}

func finite(vs ...float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

// exact returns the raster that p maps onto pixel for pixel, and the dst
// position to draw it at. There is none if p's transform is not
// axis-aligned with positive scale, or if the raster would be too large.
func (p placement) exact() (t exactTarget, px, py float64, ok bool) {
	a, b := p.geoM.Element(0, 0), p.geoM.Element(0, 1)
	c, d := p.geoM.Element(1, 0), p.geoM.Element(1, 1)
	if b != 0 || c != 0 || a <= 0 || d <= 0 {
		return t, 0, 0, false
	}
	tx, ty := p.geoM.Element(0, 2), p.geoM.Element(1, 2)
	// Snapping the size like positions keeps sizes derived from moving
	// coordinates, such as a camera's, from differing by rounding error.
	dw, dh := snap(p.scaleX*p.sw), snap(p.scaleY*p.sh)
	px, w, dx := p.exactSpan(a*p.ox+tx, dw, a*p.x+tx, a*(p.x+p.w)+tx)
	py, h, dy := p.exactSpan(d*p.oy+ty, dh, d*p.y+ty, d*(p.y+p.h)+ty)
	if !(w >= 1 && w <= maxRasterSize && h >= 1 && h <= maxRasterSize) {
		return t, 0, 0, false
	}
	return exactTarget{int(w), int(h), dw / p.sw, dh / p.sh, dx, dy}, px, py, true
}

// exactSpan returns, along one axis, the first pixel and the number of
// pixels that show an SVG starting at o and n pixels long, and o relative to
// the first pixel. Cover clips to [lo, hi), keeping the pixels whose centers
// are inside, as DrawImage does.
func (p placement) exactSpan(o, n, lo, hi float64) (first, count, offset float64) {
	o = snap(o)
	first, last := math.Floor(o), math.Ceil(snap(o+n))
	if p.fit == Cover {
		first = max(first, math.Floor(snap(lo)+0.5))
		last = min(last, math.Floor(snap(hi)+0.5))
	}
	return first, last - first, o - first
}

func snap(v float64) float64 {
	return math.Round(v*subpixels) / subpixels
}
