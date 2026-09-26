package ebitsvg

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
)

const white = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 7 3">
	<rect width="7" height="3" fill="#fff"/>
</svg>`

const disc = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10">
	<circle cx="5" cy="5" r="4.3" fill="#08f" stroke="#000" stroke-width="0.7"/>
</svg>`

var empty = color.RGBA{}

type probe struct {
	x, y int
	want color.RGBA
}

func checkProbes(t *testing.T, dst *ebiten.Image, probes []probe) {
	t.Helper()
	for _, p := range probes {
		if got := dst.At(p.x, p.y).(color.RGBA); !near(got, p.want, 2) {
			t.Errorf("pixel (%d, %d) = %v; want %v", p.x, p.y, got, p.want)
		}
	}
}

func near(a, b color.RGBA, tol int) bool {
	d := func(x, y uint8) bool { return max(int(x)-int(y), int(y)-int(x)) <= tol }
	return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B) && d(a.A, b.A)
}

// draw draws n times into a cleared dst, as over n frames.
func draw(img *Image, dst *ebiten.Image, n int, x, y, w, h float64, opts *DrawOptions) {
	for range n {
		dst.Clear()
		img.Draw(dst, x, y, w, h, opts)
	}
}

// newImage returns an Image of s. If moving, it is prepared for another
// target than the given one, so that drawing that one uses the moving raster.
func newImage(s *SVG, moving bool, x, y, w, h float64, opts *DrawOptions) *Image {
	img := NewImage(s)
	if moving {
		img.Prepare(x+0.5, y, w, h, opts)
	}
	return img
}

// fakeTicks makes tick return the value it points to for the rest of t.
func fakeTicks(t *testing.T) *int64 {
	var now int64
	tick = func() int64 { return now }
	t.Cleanup(func() { tick = ebiten.Tick })
	return &now
}

// held names the rasters img holds.
func held(img *Image) string {
	switch {
	case img.exact != nil && img.raster != nil:
		return "exact+2x"
	case img.exact != nil:
		return "exact"
	case img.raster != nil:
		return "2x"
	}
	return "none"
}

// rasters records the distinct rasters an Image has held.
type rasters map[*ebiten.Image]bool

func (r rasters) add(img *Image) {
	for _, i := range []*ebiten.Image{img.exact, img.raster} {
		if i != nil {
			r[i] = true
		}
	}
}

func rotated(tx float64) ebiten.GeoM {
	var g ebiten.GeoM
	g.Rotate(math.Pi / 2)
	g.Translate(tx, 0)
	return g
}

func TestDraw(t *testing.T) {
	tests := []struct {
		name       string
		x, y, w, h float64
		opts       DrawOptions
		probes     []probe
	}{
		{"contain", 10, 10, 40, 40, DrawOptions{},
			[]probe{{15, 30, red}, {45, 30, blue}, {30, 15, empty}, {30, 45, empty}}},
		{"contain top", 10, 10, 40, 40, DrawOptions{AlignY: -1},
			[]probe{{15, 15, red}, {45, 25, blue}, {30, 35, empty}}},
		{"contain bottom", 10, 10, 40, 40, DrawOptions{AlignY: 1},
			[]probe{{15, 45, red}, {30, 25, empty}}},
		{"contain extrapolated", 10, 10, 40, 40, DrawOptions{AlignY: 3},
			[]probe{{15, 55, red}, {30, 30, empty}}},
		{"cover", 10, 10, 40, 40, DrawOptions{Fit: Cover},
			[]probe{{15, 30, red}, {45, 30, blue}, {5, 30, empty}, {55, 30, empty}, {30, 5, empty}, {30, 55, empty}}},
		{"cover left", 10, 10, 40, 40, DrawOptions{Fit: Cover, AlignX: -1},
			[]probe{{15, 30, red}, {45, 30, red}, {55, 30, empty}}},
		{"cover right", 10, 10, 40, 40, DrawOptions{Fit: Cover, AlignX: 1},
			[]probe{{15, 30, blue}, {45, 30, blue}, {5, 30, empty}}},
		{"cover fractional", 5.3, 5.3, 10, 10, DrawOptions{Fit: Cover},
			[]probe{{4, 8, empty}, {5, 8, red}, {14, 8, blue}, {15, 8, empty}, {8, 4, empty}, {8, 6, red}, {8, 14, red}, {8, 15, empty}}},
		{"cover rotated", 5, 5, 10, 10, DrawOptions{Fit: Cover, GeoM: rotated(40)},
			[]probe{{30, 7, red}, {30, 13, blue}, {30, 3, empty}, {30, 17, empty}, {23, 10, empty}, {37, 10, empty}}},
		{"stretch", 10, 10, 40, 40, DrawOptions{Fit: Stretch},
			[]probe{{15, 15, red}, {15, 45, red}, {45, 30, blue}, {30, 5, empty}, {55, 30, empty}}},
	}
	s := mustParse(t, halves)
	dst := ebiten.NewImage(64, 64)
	for _, tt := range tests {
		for _, moving := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/moving=%v", tt.name, moving), func(t *testing.T) {
				img := newImage(s, moving, tt.x, tt.y, tt.w, tt.h, &tt.opts)
				draw(img, dst, 1, tt.x, tt.y, tt.w, tt.h, &tt.opts)
				checkProbes(t, dst, tt.probes)
			})
		}
	}
}

func TestRasterSize(t *testing.T) {
	var zoom, squash, spin ebiten.GeoM
	zoom.Scale(3, 3)
	squash.Scale(1, 3)
	spin.Rotate(math.Pi / 2)
	spin.Scale(2, 2)
	tests := []struct {
		name string
		w, h float64
		geoM ebiten.GeoM
		want image.Point
	}{
		{"multiple of 4", 37, 13, ebiten.GeoM{}, image.Pt(76, 28)},
		{"tiny", 0.01, 0.01, ebiten.GeoM{}, image.Pt(4, 4)},
		{"zoom", 20, 10, zoom, image.Pt(120, 60)},
		{"non-uniform", 20, 10, squash, image.Pt(40, 60)},
		{"rotated", 20, 10, spin, image.Pt(80, 40)},
		{"clamped", 1e5, 5e4, ebiten.GeoM{}, image.Pt(4088, 2044)},
		{"clamped huge", 1e300, 1e300, ebiten.GeoM{}, image.Pt(4088, 4088)},
	}
	s := mustParse(t, halves)
	dst := ebiten.NewImage(16, 16)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &DrawOptions{Fit: Stretch, GeoM: tt.geoM}
			img := newImage(s, true, 0, 0, tt.w, tt.h, opts)
			img.Draw(dst, 0, 0, tt.w, tt.h, opts)
			if got := held(img); got != "2x" {
				t.Fatalf("held %s; want 2x", got)
			}
			want := tt.want.Add(image.Pt(2*rasterMargin, 2*rasterMargin))
			if got := img.raster.Bounds().Size(); got != want {
				t.Errorf("raster size = %v; want %v", got, want)
			}
		})
	}
}

func TestNoFadedEdges(t *testing.T) {
	s := mustParse(t, white)
	dst := ebiten.NewImage(64, 64)
	opts := &DrawOptions{Fit: Stretch}
	for _, size := range []image.Point{{9, 3}, {21, 7}, {37, 13}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			// Anchoring at 1.5 times the size would make odd raster sides,
			// which lose texels at the second mipmap level.
			w, h := 1.5*float64(size.X), 1.5*float64(size.Y)
			img := newImage(s, true, 0, 0, w, h, opts)
			draw(img, dst, 1, 0, 0, w, h, opts)
			draw(img, dst, 1, 0, 0, float64(size.X), float64(size.Y), opts)
			edge := func(x, y int) {
				if a := dst.At(x, y).(color.RGBA).A; a != 255 {
					t.Errorf("edge pixel (%d, %d) alpha = %d; want 255", x, y, a)
				}
			}
			for x := range size.X {
				edge(x, size.Y-1)
			}
			for y := range size.Y {
				edge(size.X-1, y)
			}
		})
	}
}

func TestDrawFiltersLinearly(t *testing.T) {
	s := mustParse(t, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 4 1">
		<rect width="4" height="1" fill="#fff"/>
		<path d="M0 0h.5v1H0zM1 0h.5v1H1zM2 0h.5v1H2zM3 0h.5v1H3z"/>
	</svg>`)
	dst := ebiten.NewImage(4, 4)
	draw(NewImage(s), dst, 1, 0, 0, 4, 4, &DrawOptions{Fit: Stretch})
	if got := dst.At(1, 2).(color.RGBA); !near(got, color.RGBA{128, 128, 128, 255}, 8) {
		t.Errorf("half-covered pixel = %v; want gray", got)
	}
}

func TestRasterReuse(t *testing.T) {
	tests := []struct {
		fx, fy float64
		reuse  bool
	}{
		{1, 1, true},
		{2, 2, true},
		{0.5, 0.5, true},
		{1.5, 0.8, true},
		{1.9, 1, true},
		{2.01, 2.01, false},
		{0.49, 0.49, false},
		{2, 1, false},
		{1, 0.5, false},
		{0.5, 1.5, false},
	}
	s := mustParse(t, halves)
	dst := ebiten.NewImage(16, 16)
	opts := &DrawOptions{Fit: Stretch}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("scale x(%v, %v)", tt.fx, tt.fy), func(t *testing.T) {
			img := newImage(s, true, 0, 0, 20, 10, opts)
			img.Draw(dst, 0, 0, 20, 10, opts)
			first := img.raster
			img.Draw(dst, 0, 0, 20*tt.fx, 10*tt.fy, opts)
			if got := img.raster == first; got != tt.reuse {
				t.Errorf("reused = %v; want %v", got, tt.reuse)
			}
		})
	}

	t.Run("beyond the size limit", func(t *testing.T) {
		img := NewImage(s)
		img.Draw(dst, 0, 0, 1e4, 5e3, opts)
		first := img.raster
		img.Draw(dst, 0, 0, 3e4, 1.5e4, opts)
		if first == nil || img.raster != first {
			t.Error("reused = false; want true")
		}
	})
}

func TestMovingEdges(t *testing.T) {
	s := mustParse(t, white)
	dst := ebiten.NewImage(32, 16)
	opts := &DrawOptions{Fit: Stretch}
	// Ebitengine snaps vertices to 0, 5/16 or 11/16 of a pixel, so the
	// SVG's left edge covers c of pixel column 4 only for these c. At twice
	// the displayed size, linear filtering ramps the edge over half a pixel.
	for _, c := range []float64{5.0 / 16, 11.0 / 16} {
		t.Run(fmt.Sprintf("covering %v", c), func(t *testing.T) {
			x := 5 - c
			img := newImage(s, true, x, 2, 14, 6, opts)
			draw(img, dst, 1, x, 2, 14, 6, opts)
			a := uint8(255 * (2*c - 0.5))
			want := color.RGBA{a, a, a, a}
			if got := dst.At(4, 4).(color.RGBA); !near(got, want, 8) {
				t.Errorf("pixel = %v; want %v", got, want)
			}
		})
	}
}

// checkExact checks that dst holds s rasterized at w×h at (x, y), pixel for
// pixel.
func checkExact(t *testing.T, dst *ebiten.Image, s *SVG, x, y, w, h int) {
	t.Helper()
	want := s.Rasterize(w, h)
	for py := range h {
		for px := range w {
			if got, want := dst.At(x+px, y+py).(color.RGBA), want.RGBAAt(px, py); !near(got, want, 1) {
				t.Fatalf("pixel (%d, %d) = %v; want %v", px, py, got, want)
			}
		}
	}
}

func TestSettle(t *testing.T) {
	now := fakeTicks(t)
	s := mustParse(t, disc)
	dst := ebiten.NewImage(64, 64)
	img := NewImage(s)
	draw(img, dst, 1, 3, 5, 40, 40, nil)
	if got := held(img); got != "exact" {
		t.Fatalf("first draw: held %s; want exact", got)
	}
	checkExact(t, dst, s, 3, 5, 40, 40)

	exact := img.exact
	draw(img, dst, 1, 13, 4, 40, 40, nil)
	if img.exact != exact {
		t.Error("moving by whole pixels: exact raster replaced; want kept")
	}
	checkExact(t, dst, s, 13, 4, 40, 40)

	for i := range settleTicks {
		// Ebitengine may draw several frames per tick.
		draw(img, dst, 3, 13.5, 4, 40, 40, nil)
		if got := held(img); got != "2x" {
			t.Fatalf("after %d ticks: held %s; want 2x", i, got)
		}
		*now++
	}
	draw(img, dst, 1, 13.5, 4, 40, 40, nil)
	if want := (exactTarget{41, 40, 4, 4, 0.5, 0}); img.exact == nil || img.target != want {
		t.Errorf("target = %+v; want %+v", img.target, want)
	}
	if got := held(img); got != "exact+2x" {
		t.Errorf("settled: held %s; want exact+2x", got)
	}

	*now += graceTicks
	draw(img, dst, 1, 13.5, 4, 40, 40, nil)
	if got := held(img); got != "exact" {
		t.Errorf("still for %d ticks: held %s; want exact", graceTicks, got)
	}
}

// TestSteps checks that content moving in steps a few ticks apart reuses
// its rasters: the first exact one, the 2x one, and the exact one it
// settles on after the first step.
func TestSteps(t *testing.T) {
	now := fakeTicks(t)
	s := mustParse(t, disc)
	dst := ebiten.NewImage(64, 64)
	for _, every := range []int{3, 4} {
		t.Run(fmt.Sprintf("every %d ticks", every), func(t *testing.T) {
			img := NewImage(s)
			seen := rasters{}
			var x float64
			for i := range 8 {
				x = 3 + 0.3*float64(i)
				for range every {
					draw(img, dst, 1, x, 5, 40, 40, nil)
					seen.add(img)
					*now++
				}
			}
			if len(seen) != 3 {
				t.Errorf("rasterized %d times; want 3", len(seen))
			}

			*now += graceTicks
			draw(img, dst, 1, x, 5, 40, 40, nil)
			if got := held(img); got != "exact" {
				t.Errorf("still for %d ticks: held %s; want exact", graceTicks, got)
			}
		})
	}
}

// TestCameraScroll checks that scrolling a zoomed camera by whole pixels
// keeps the exact raster, although the box's width, computed from world
// coordinates, varies by rounding error.
func TestCameraScroll(t *testing.T) {
	now := fakeTicks(t)
	const zoom = 1.1
	img := NewImage(mustParse(t, disc))
	dst := ebiten.NewImage(64, 64)
	opts := &DrawOptions{Fit: Stretch}
	seen := rasters{}
	for i := range 20 {
		cam := float64(i) / zoom
		x := (5 - cam) * zoom
		draw(img, dst, 1, x, 5, (41-cam)*zoom-x, 40, opts)
		seen.add(img)
		*now++
	}
	if len(seen) != 1 {
		t.Errorf("rasterized %d times; want 1", len(seen))
	}
}

func TestPrepare(t *testing.T) {
	now := fakeTicks(t)
	s := mustParse(t, disc)
	dst := ebiten.NewImage(64, 64)
	rotate := &DrawOptions{GeoM: rotated(64)}

	t.Run("exact", func(t *testing.T) {
		img := NewImage(s)
		img.Prepare(3, 5, 40, 40, nil)
		exact := img.exact
		draw(img, dst, 1, 3, 5, 40, 40, nil)
		if exact == nil || img.exact != exact || img.raster != nil {
			t.Errorf("held %s, rasterized again; want exact, prepared", held(img))
		}
		checkExact(t, dst, s, 3, 5, 40, 40)
	})

	t.Run("rotated", func(t *testing.T) {
		img := NewImage(s)
		img.Prepare(3, 5, 40, 40, rotate)
		raster := img.raster
		draw(img, dst, 1, 3, 5, 40, 40, rotate)
		if raster == nil || img.raster != raster {
			t.Errorf("held %s, rasterized again; want 2x, prepared", held(img))
		}
	})

	t.Run("rotated, then drawn exactly", func(t *testing.T) {
		img := NewImage(s)
		draw(img, dst, 1, 3, 5, 40, 40, nil)
		img.Prepare(3, 5, 40, 40, rotate)
		*now += graceTicks
		draw(img, dst, 1, 3, 5, 40, 40, nil)
		if got := held(img); got != "exact" {
			t.Errorf("held %s; want exact", got)
		}
	})
}

func TestDrawPassesOptions(t *testing.T) {
	s := mustParse(t, white)
	dst := ebiten.NewImage(16, 16)
	half := &DrawOptions{Fit: Stretch}
	half.ColorScale.ScaleAlpha(0.5)
	erase := &DrawOptions{Fit: Stretch, Blend: ebiten.BlendClear}
	for _, moving := range []bool{false, true} {
		t.Run(fmt.Sprintf("moving=%v", moving), func(t *testing.T) {
			draw(newImage(s, moving, 0, 0, 16, 16, half), dst, 1, 0, 0, 16, 16, half)
			checkProbes(t, dst, []probe{{8, 8, color.RGBA{128, 128, 128, 128}}})

			dst.Fill(blue)
			newImage(s, moving, 4, 4, 8, 8, erase).Draw(dst, 4, 4, 8, 8, erase)
			checkProbes(t, dst, []probe{{8, 8, empty}, {2, 2, blue}})
		})
	}
}

func TestDrawNothing(t *testing.T) {
	var zero, nan ebiten.GeoM
	zero.Scale(0, 1)
	nan.Translate(math.NaN(), 0)
	inf := math.Inf(1)
	tests := []struct {
		name       string
		x, y, w, h float64
		geoM       ebiten.GeoM
	}{
		{"zero width", 0, 0, 0, 10, ebiten.GeoM{}},
		{"negative height", 0, 0, 10, -1, ebiten.GeoM{}},
		{"NaN width", 0, 0, math.NaN(), 10, ebiten.GeoM{}},
		{"infinite width", 0, 0, inf, 10, ebiten.GeoM{}},
		{"infinite height", 0, 0, 10, inf, ebiten.GeoM{}},
		{"NaN x", math.NaN(), 0, 10, 10, ebiten.GeoM{}},
		{"infinite y", 0, -inf, 10, 10, ebiten.GeoM{}},
		{"zero GeoM scale", 0, 0, 10, 10, zero},
		{"NaN GeoM", 0, 0, 10, 10, nan},
		{"overflowing scale", 0, 0, 1e300, 1e300, func() (g ebiten.GeoM) { g.Scale(1e300, 1); return }()},
	}
	s := mustParse(t, halves)
	dst := ebiten.NewImage(16, 16)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img := NewImage(s)
			for range 2 {
				img.Draw(dst, tt.x, tt.y, tt.w, tt.h, &DrawOptions{GeoM: tt.geoM})
			}
			img.Prepare(tt.x, tt.y, tt.w, tt.h, &DrawOptions{GeoM: tt.geoM})
			if got := held(img); got != "none" {
				t.Errorf("held %s; want none", got)
			}
		})
	}
}

// TestDrawDisposedDst checks that drawing into a disposed image, which
// Ebitengine ignores, does not panic.
func TestDrawDisposedDst(t *testing.T) {
	dst := ebiten.NewImage(16, 16)
	//lint:ignore SA1019 Dispose is the only way to get a disposed image.
	dst.Dispose()
	opts := &DrawOptions{Fit: Cover}
	for _, moving := range []bool{false, true} {
		t.Run(fmt.Sprintf("moving=%v", moving), func(t *testing.T) {
			newImage(mustParse(t, halves), moving, 0, 0, 16, 16, opts).Draw(dst, 0, 0, 16, 16, opts)
		})
	}
}

func TestPanics(t *testing.T) {
	s := mustParse(t, halves)
	dst := ebiten.NewImage(16, 16)
	tests := []struct {
		name string
		f    func()
		want string
	}{
		{"NewImage(nil)", func() { NewImage(nil) }, "ebitsvg: NewImage called with nil SVG"},
		{"invalid Fit", func() { NewImage(s).Draw(dst, 0, 0, 16, 16, &DrawOptions{Fit: Stretch + 1}) }, "ebitsvg: invalid Fit 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tt.want {
					t.Errorf("panic = %v; want %q", got, tt.want)
				}
			}()
			tt.f()
		})
	}
}
