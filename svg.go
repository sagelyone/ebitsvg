// Package ebitsvg draws SVG images with Ebitengine, sharp at the size they
// are displayed, without rasterizing them every frame.
//
// Parse each SVG file once into an [SVG], which is immutable and can be
// shared. An [Image] draws an SVG through cached rasters, which it makes in
// the background. While the displayed size or position changes, it draws a
// raster made at twice the displayed size, reused until that size halves or
// doubles. Once they settle, it draws a raster made for exactly the pixels it
// covers.
//
// [SVG.Sprite] takes the sprites out of a sprite sheet, as SVGs of their own.
package ebitsvg

import (
	"fmt"
	"image"
	"io"

	"github.com/sagelyone/ebitsvg/internal/resvg"
)

// SVG is a parsed SVG document, or a sprite in one (see [SVG.Sprite]). It
// is immutable and safe for concurrent use.
type SVG struct {
	doc  *resvg.Doc // of the source with the root's width and height set to its size
	id   string     // of a sprite's group, or empty for the whole document
	x, y float64    // the origin of a sprite's bounds in the document
	w, h float64
}

// Parse reads an SVG document, which must be UTF-8 or ASCII. It renders
// static SVG 1.1 and the parts of SVG 2 that resvg supports, including
// clipping, masks, filters, markers, patterns, nested <svg> elements, CSS
// selectors and <use> of any element. Text and <image> elements are
// skipped. Colors in CSS Color 4 syntax, such as rgb(0 0 0 / 50%), are
// ignored, and a gradient does not inherit gradientTransform through href.
// As SVG specifies, invalid attribute and style values are ignored, and a
// <use> element that references itself or an ancestor draws nothing.
//
// Parse rejects documents whose root element is not <svg>, that are
// compressed (.svgz) or encoded other than as UTF-8 or ASCII, whose size
// cannot be determined (see [SVG.Size]), that are not well-formed XML,
// which includes using entities other than XML's own and those the DOCTYPE
// declares, such as &nbsp;, that have too many elements, including those
// <use> elements copy, which a cycle of them makes unbounded, or that nest
// elements more than 256 deep. It reads all of r: to parse untrusted input,
// limit its size with [io.LimitReader].
func Parse(r io.Reader) (*SVG, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("ebitsvg: %w", err)
	}
	src, w, h, err := normalize(b)
	if err != nil {
		return nil, fmt.Errorf("ebitsvg: %w", err)
	}
	doc, err := resvg.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("ebitsvg: %w", err)
	}
	return &SVG{doc: doc, w: w, h: h}, nil
}

// Sprite returns an SVG of just the group with the given id, sized to the
// group's bounds: the axis-aligned bounding box, in the document, of its
// first shape, in document order, that has an area and neither fill nor
// stroke, normally a <rect fill="none">. The bounds set the sprite's size
// and the padding around its art, and draw nothing, so a sprite sheet,
// with a group for each sprite, also draws as a whole.
//
// The group is drawn with its own transform, opacity and effects, and with
// its ancestors' transforms but not their opacity, clipping, masks or
// filters. If several groups have the id, Sprite uses the first in
// document order. s can be the whole document or any sprite in it.
func (s *SVG) Sprite(id string) (*SVG, error) {
	b, err := s.doc.Bounds(id)
	if err != nil {
		return nil, fmt.Errorf("ebitsvg: %w", err)
	}
	return &SVG{
		doc: s.doc,
		id:  id,
		x:   float64(b[0]),
		y:   float64(b[1]),
		w:   float64(b[2]),
		h:   float64(b[3]),
	}, nil
}

// Size returns the size of the SVG's viewBox or, if it has none, its width
// and height, which must then be in absolute units, converted to px. It is
// the size of the coordinate system the SVG is drawn in, which gives its
// aspect ratio, and not necessarily the size a browser displays, which width
// and height set: Material Symbols, for example, report 960×960. A
// sprite's size is that of its bounds, in the document's units.
func (s *SVG) Size() (w, h float64) {
	return s.w, s.h
}

// Rasterize renders the SVG to a new w×h image, stretching it to fill the
// image if the aspect ratios differ. It panics if w or h is negative. If
// the renderer runs out of memory, which a filter over a large image can
// make it do, the image is transparent.
func (s *SVG) Rasterize(w, h int) *image.RGBA {
	return s.rasterize(w, h, float64(w)/s.w, float64(h)/s.h, 0, 0)
}

// rasterize renders the SVG into a new w×h image, scaling viewBox units by
// (sx, sy) and then offsetting the result by (dx, dy) pixels. If rendering
// fails, the image is transparent.
func (s *SVG) rasterize(w, h int, sx, sy, dx, dy float64) *image.RGBA {
	if w < 0 || h < 0 {
		panic(fmt.Sprintf("ebitsvg: negative raster size %dx%d", w, h))
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == 0 || h == 0 {
		return img
	}
	s.doc.Render(s.id, w, h, float32(sx), float32(sy), float32(dx-s.x*sx), float32(dy-s.y*sy), img.Pix)
	return img
}
