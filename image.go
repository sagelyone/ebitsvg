package ebitsvg

import (
	"cmp"
	"fmt"
	"image"
	"math"
	"runtime"
	"sync/atomic"

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

// Image draws an SVG through cached rasters, which it makes in the
// background, so that drawing never waits for rasterizing.
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
// Until the raster it needs is ready, an Image draws the closest one it
// has, scaled, and a new Image draws nothing; [Image.Prepare] makes rasters
// ahead of time. An Image makes one raster at a time, and Images make at
// most GOMAXPROCS-1 at once, leaving the game a CPU.
//
// An Image caches for one target, so use one Image for each independently
// drawn use of an SVG. An Image is not safe for concurrent use.
type Image struct {
	svg *SVG

	// raster was made at twice the display scale (anchorX, anchorY), with
	// rasterMargin texels around the SVG.
	raster           *raster
	anchorX, anchorY float64

	// exact is made for target, which Draw has had since tick since, if
	// its args are target, and otherwise for an earlier target. restless
	// is whether the target changed within graceTicks of settling.
	exact    *raster
	target   rasterArgs
	since    int64
	restless bool

	// job, if not nil, makes a raster in the background.
	job *job
}

// tick is [ebiten.Tick], replaced by tests, which run within one tick.
var tick = ebiten.Tick

// rasterArgs are the arguments of [SVG.rasterize]: a w×h raster with the
// SVG's point (u, v) at texel (u*sx + dx, v*sy + dy).
type rasterArgs struct {
	w, h           int
	sx, sy, dx, dy float64
}

// A raster is an SVG rasterized with args, with margin transparent texels
// around the SVG's bounds.
type raster struct {
	img    *ebiten.Image
	args   rasterArgs
	margin int
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
// Draw does not rasterize: it starts making the raster it needs in the
// background and meanwhile draws the closest raster it has, if any.
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
	img.collect()
	t, px, py, exact := p.exact()
	var now int64
	if exact {
		now = tick()
		if t != img.target {
			img.setTarget(t, now)
		}
		wait := int64(settleTicks)
		if img.restless {
			wait = graceTicks
		}
		if img.exact == nil && img.raster == nil || img.exactNow() || now-img.since >= wait {
			img.settle()
		} else {
			img.wantRaster(p)
		}
	} else {
		img.setTarget(rasterArgs{}, 0)
		img.wantRaster(p)
	}
	img.collect() // in case start ran the job at once, as tests make it
	if exact {
		img.retire(now)
	}

	op := &ebiten.DrawImageOptions{
		ColorScale: opts.ColorScale,
		Blend:      opts.Blend,
		Filter:     ebiten.FilterLinear,
	}
	if exact && img.exactNow() {
		op.GeoM.Translate(px, py)
		dst.DrawImage(img.exact.img, op)
		return
	}
	if r := cmp.Or(img.raster, img.exact); r != nil {
		r.draw(dst, p, op)
	}
}

// draw draws r, scaled to where p places the SVG.
func (r *raster) draw(dst *ebiten.Image, p placement, op *ebiten.DrawImageOptions) {
	src, b := r.img, r.img.Bounds()
	a := r.args
	rx, ry := p.sx/a.sx, p.sy/a.sy
	if p.fit == Cover {
		// Clipping the source, unlike a sub-image of dst, follows any GeoM.
		b = image.Rect(
			rasterEdge((p.x-p.ox)/rx+a.dx, b.Dx()), rasterEdge((p.y-p.oy)/ry+a.dy, b.Dy()),
			rasterEdge((p.x+p.w-p.ox)/rx+a.dx, b.Dx()), rasterEdge((p.y+p.h-p.oy)/ry+a.dy, b.Dy()),
		)
	}
	if !keepsDst(op.Blend) {
		b = b.Intersect(src.Bounds().Inset(r.margin))
	}
	if b != src.Bounds() {
		if b.Empty() {
			return
		}
		src = src.SubImage(b).(*ebiten.Image)
	}
	op.GeoM.Scale(rx, ry)
	op.GeoM.Translate(p.ox+(float64(b.Min.X)-a.dx)*rx, p.oy+(float64(b.Min.Y)-a.dy)*ry)
	op.GeoM.Concat(p.geoM)
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
// arguments, so that drawing them needs no rasterization and draws them at
// once. Use it to render ahead of time, such as behind a loading screen.
func (img *Image) Prepare(x, y, w, h float64, opts *DrawOptions) {
	if opts == nil {
		opts = &DrawOptions{}
	}
	p, ok := img.place(x, y, w, h, opts)
	if !ok {
		return
	}
	t, _, _, exact := p.exact()
	now := tick()
	for {
		if exact {
			if t != img.target {
				img.setTarget(t, now)
			}
			img.settle()
		} else {
			img.wantRaster(p)
		}
		// The job may have been making another raster.
		j := img.job
		if j == nil {
			break
		}
		<-j.done
		img.collect()
	}
	if exact {
		img.retire(now)
	}
}

// exactNow reports whether img.exact is made for img.target.
func (img *Image) exactNow() bool {
	return img.exact != nil && img.exact.args == img.target
}

// setTarget sets the exact target, first seen at tick since.
func (img *Image) setTarget(t rasterArgs, since int64) {
	if t != img.target {
		if img.exactNow() {
			img.restless = img.raster != nil
		}
		if j := img.job; j != nil && j.exact {
			j.cancel()
		}
	}
	img.target, img.since = t, since
}

// settle starts making the exact raster for img.target, unless img has it.
func (img *Image) settle() {
	if !img.exactNow() {
		img.request(&job{args: img.target, exact: true})
	}
}

// retire releases the raster for a changing target once img has the exact
// raster and the target has persisted for graceTicks at tick now.
func (img *Image) retire(now int64) {
	if img.exactNow() && img.raster != nil && now-img.since >= graceTicks {
		img.raster.img.Deallocate()
		img.raster = nil
		img.restless = false
	}
}

// wantRaster starts making a raster that suits the display scale of p,
// unless img has or is making one.
func (img *Image) wantRaster(p placement) {
	if img.raster != nil && suits(p, img.anchorX, img.anchorY) {
		return
	}
	if j := img.job; j != nil && !j.exact && suits(p, j.anchorX, j.anchorY) {
		return
	}
	w, h := p.scaleX*p.sw, p.scaleY*p.sh
	// Clamping both axes by the same factor keeps mipmapping even.
	s := min(2, (maxRasterSize-2*rasterMargin)/max(w, h))
	rw, rh := rasterSide(w*s)+2*rasterMargin, rasterSide(h*s)+2*rasterMargin
	if img.raster != nil && img.raster.img.Bounds().Size() == image.Pt(rw, rh) {
		img.anchorX, img.anchorY = p.scaleX, p.scaleY
		return
	}
	sx, sy := float64(rw-2*rasterMargin)/p.sw, float64(rh-2*rasterMargin)/p.sh
	img.request(&job{
		args:    rasterArgs{rw, rh, sx, sy, rasterMargin, rasterMargin},
		anchorX: p.scaleX,
		anchorY: p.scaleY,
	})
}

// suits reports whether a raster made at twice the display scale (ax, ay)
// suits the display scale of p.
func suits(p placement, ax, ay float64) bool {
	fx, fy := p.scaleX/ax, p.scaleY/ay
	// Ebitengine chooses the mipmap level by the less shrunk axis, so the
	// axes must not drift apart either.
	return reusable(fx) && reusable(fy) && max(fx, fy)/min(fx, fy) < 2
}

// request makes j img's job, unless img's job makes the same raster or
// has started.
func (img *Image) request(j *job) {
	if old := img.job; old != nil {
		if old.exact == j.exact && old.args == j.args || !old.cancel() {
			return
		}
	}
	j.done = make(chan struct{})
	img.job = j
	start(img.svg, j)
}

// collect installs the raster that img's job made, once it has finished.
// An exact raster for an earlier target is kept only while img has no
// other raster to fall back to.
func (img *Image) collect() {
	j := img.job
	if j == nil || !j.finished() {
		return
	}
	img.job = nil
	if j.pix == nil || j.exact && j.args != img.target && img.raster != nil {
		return
	}
	r := &raster{img: ebiten.NewImageFromImage(j.pix), args: j.args}
	if j.exact {
		if img.exact != nil {
			img.exact.img.Deallocate()
		}
		img.exact = r
		return
	}
	r.margin = rasterMargin
	if img.raster != nil {
		img.raster.img.Deallocate()
	}
	img.raster = r
	img.anchorX, img.anchorY = j.anchorX, j.anchorY
	if img.exact != nil && !img.exactNow() {
		img.exact.img.Deallocate()
		img.exact = nil
	}
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

// A job makes a raster in the background.
type job struct {
	args             rasterArgs
	exact            bool    // whether the raster is exact or at twice the display scale
	anchorX, anchorY float64 // the display scale of a raster that is not exact

	state atomic.Int32  // queued, running or cancelled
	done  chan struct{} // closed once the job has finished
	pix   *image.RGBA   // the raster, unless the job was cancelled
}

const (
	queued = iota
	running
	cancelled
)

// workers limits the jobs that rasterize at once, leaving the game a CPU.
var workers = make(chan struct{}, max(1, runtime.GOMAXPROCS(0)-1))

// start runs j in the background. Tests replace it to run jobs at once.
var start = func(s *SVG, j *job) { go j.run(s) }

func (j *job) run(s *SVG) {
	defer close(j.done)
	workers <- struct{}{}
	defer func() { <-workers }()
	if j.state.CompareAndSwap(queued, running) {
		a := j.args
		j.pix = s.rasterize(a.w, a.h, a.sx, a.sy, a.dx, a.dy)
	}
}

// cancel cancels j unless it has started, reporting whether j makes no
// raster.
func (j *job) cancel() bool {
	j.state.CompareAndSwap(queued, cancelled)
	return j.state.Load() == cancelled
}

// finished reports whether j has finished.
func (j *job) finished() bool {
	select {
	case <-j.done:
		return true
	default:
		return false
	}
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
func (p placement) exact() (t rasterArgs, px, py float64, ok bool) {
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
	return rasterArgs{int(w), int(h), dw / p.sw, dh / p.sh, dx, dy}, px, py, true
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
