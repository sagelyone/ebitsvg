// Package ebitsvg draws SVG images with Ebitengine, sharp at the size they
// are displayed, without rasterizing them every frame.
//
// Parse each SVG file once into an [SVG], which is immutable and can be
// shared. An [Image] draws an SVG through cached rasters. While the displayed
// size or position changes, it draws a raster made at twice the displayed
// size, reused until that size halves or doubles. Once they settle, it draws a
// raster made for exactly the pixels it covers.
package ebitsvg

import (
	"fmt"
	"image"
	"io"

	"github.com/sagelyone/ebitsvg/internal/oksvg"
	"github.com/srwiley/rasterx"
)

// SVG is a parsed SVG document. It is immutable and safe for concurrent use.
type SVG struct {
	icon *oksvg.Icon
}

// Parse reads an SVG document, which must be UTF-8 or ASCII. It supports
// paths and basic shapes; fills, with either fill rule, and strokes,
// including dashes, caps and joins; linear and radial gradients; opacity;
// transforms; lengths in user units or absolute units (px, in, cm, mm, pt
// and pc, at 96px per inch); <style> rules for classes; <use> of elements
// in <defs>; and colors as names, #rgb, #rgba, #rrggbb, #rrggbbaa, rgb(),
// rgba(), hsl(), hsla() and currentColor, which is the color property, black
// unless set. It does not support:
//
//   - text, images, symbol, marker and pattern, which are skipped;
//   - filter, clip-path and mask, which are ignored: content is drawn
//     unfiltered and unclipped;
//   - nested <svg> viewports, whose content is drawn in the parent's
//     coordinates;
//   - <switch>, all of whose children are drawn;
//   - paint-order, mix-blend-mode and vector-effect, which are ignored:
//     fills are drawn before strokes, blending is normal, and strokes
//     scale with transforms;
//   - <use> of elements outside <defs>, or defined after the <use>, which
//     draws nothing;
//   - opacity of an element or group as a whole: it applies to each fill
//     and stroke separately, so overlaps show through;
//   - relative lengths (%, em, ex): on shapes they leave the shape
//     undrawn, in stroke widths, dashes and <use> x and y Parse rejects
//     them, and in gradients with gradientUnits="userSpaceOnUse",
//     percentages are fractions of a user unit;
//   - CSS selectors other than a class: a rule with ".a, rect" applies to
//     class a only, and selectors such as rect, #id, .a.b and .a .b match
//     nothing; class rules apply in the order of the class attribute rather
//     than the style sheet, rules in at-rules such as @media are ignored,
//     and values such as var() or !important are rejected.
//
// Stroke widths and dashes scale with transforms by the square root of
// their area scale factor, so they are exact under uniform scaling.
//
// Parse rejects documents whose root element is not <svg>, that are
// compressed (.svgz) or encoded other than as UTF-8 or ASCII, whose size
// cannot be determined (see [SVG.Size]), that have unsupported style values,
// or whose <use> elements form a cycle or expand to an excessive amount of
// content. It reads all of r: to parse untrusted input, limit its size with
// [io.LimitReader].
func Parse(r io.Reader) (*SVG, error) {
	icon, err := oksvg.ReadIcon(r)
	if err != nil {
		return nil, fmt.Errorf("ebitsvg: %w", err)
	}
	return &SVG{icon}, nil
}

// Size returns the size of the SVG's viewBox or, if it has none, its width
// and height, which must then be in absolute units, converted to px. It is
// the size of the coordinate system the SVG is drawn in, which gives its
// aspect ratio, and not necessarily the size a browser displays, which width
// and height set: Material Symbols, for example, report 960×960.
func (s *SVG) Size() (w, h float64) {
	return s.icon.ViewBox.W, s.icon.ViewBox.H
}

// Rasterize renders the SVG to a new w×h image, stretching it to fill the
// image if the aspect ratios differ. It panics if w or h is negative.
func (s *SVG) Rasterize(w, h int) *image.RGBA {
	vb := s.icon.ViewBox
	return s.rasterize(w, h, float64(w)/vb.W, float64(h)/vb.H, 0, 0)
}

// rasterize renders the SVG into a new w×h image, scaling viewBox units by
// (sx, sy) and then offsetting the result by (dx, dy) pixels.
func (s *SVG) rasterize(w, h int, sx, sy, dx, dy float64) *image.RGBA {
	if w < 0 || h < 0 {
		panic(fmt.Sprintf("ebitsvg: negative raster size %dx%d", w, h))
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == 0 || h == 0 {
		return img
	}
	vb := s.icon.ViewBox
	s.icon.Draw(img, rasterx.Identity.Translate(dx, dy).Scale(sx, sy).Translate(-vb.X, -vb.Y))
	return img
}
