// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley

package oksvg

import (
	"image/color"

	"github.com/srwiley/rasterx"
)

// pathStyle holds the computed style of an element.
type pathStyle struct {
	fillOpacity, lineOpacity, opacity float64
	lineWidth, dashOffset, miterLimit float64
	dash                              []float64
	fill, stroke                      *paint      // nil is none
	color                             color.NRGBA // currentColor
	stopColor                         color.NRGBA
	stopOpacity                       float64
	hidden, invisible                 bool // display: none, visibility: hidden
	evenOdd                           bool // fill-rule: evenodd
	lineCap                           rasterx.CapFunc
	lineJoin                          rasterx.JoinMode
	transform                         rasterx.Matrix2D // to the root's user space
}

// A paint is a fill or stroke: the gradient with id grad if there is one, and
// otherwise color.
type paint struct {
	grad  string
	color color.NRGBA
}

// styleAttribute maps CSS property names to values.
type styleAttribute = map[string]string

// defaultStyle holds the initial values of SVG properties: fill black, full
// opacity, no stroke, stroke width 1, butt caps and miter joins.
var defaultStyle = pathStyle{
	fillOpacity: 1, lineOpacity: 1, opacity: 1,
	lineWidth: 1, miterLimit: 4,
	fill:        &paint{color: black},
	color:       black,
	stopColor:   black,
	stopOpacity: 1,
	lineCap:     rasterx.ButtCap,
	lineJoin:    rasterx.Miter,
	transform:   rasterx.Identity,
}

var black = color.NRGBA{0, 0, 0, 0xff}
