// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley

package oksvg

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strings"

	"github.com/srwiley/rasterx"
)

// svgFunc reads the attributes of an element, adding the shape it draws, if
// any, to the cursor's path.
type svgFunc func(c *iconCursor, attrs []xml.Attr) error

var (
	drawFuncs = map[string]svgFunc{
		"svg":            gF,
		"g":              gF,
		"line":           lineF,
		"stop":           stopF,
		"rect":           rectF,
		"circle":         circleF,
		"ellipse":        circleF,
		"polyline":       polylineF,
		"polygon":        polygonF,
		"path":           pathF,
		"desc":           gF,
		"title":          gF,
		"linearGradient": linearGradientF,
		"radialGradient": radialGradientF,
	}

	gF    svgFunc = func(*iconCursor, []xml.Attr) error { return nil }
	rectF svgFunc = func(c *iconCursor, attrs []xml.Attr) error {
		var x, y, w, h float64
		rx, ry := -1.0, -1.0 // auto
		var err error
		for _, attr := range attrs {
			switch attr.Name.Local {
			case "x":
				x, err = parseLength(attr.Value)
			case "y":
				y, err = parseLength(attr.Value)
			case "width":
				w, err = parseLength(attr.Value)
			case "height":
				h, err = parseLength(attr.Value)
			case "rx":
				rx, err = parseLength(attr.Value)
			case "ry":
				ry, err = parseLength(attr.Value)
			}
			if err != nil {
				return err
			}
		}
		if !(w > 0 && h > 0) {
			return nil
		}
		if rx < 0 {
			rx = ry
		} else if ry < 0 {
			ry = rx
		}
		k := c.scale
		rasterx.AddRoundRect(x*k, y*k, (w+x)*k, (h+y)*k, rx*k, ry*k, 0, rasterx.RoundGap, &c.Path)
		return nil
	}
	circleF svgFunc = func(c *iconCursor, attrs []xml.Attr) error {
		var cx, cy, rx, ry float64
		var err error
		for _, attr := range attrs {
			switch attr.Name.Local {
			case "cx":
				cx, err = parseLength(attr.Value)
			case "cy":
				cy, err = parseLength(attr.Value)
			case "r":
				rx, err = parseLength(attr.Value)
				ry = rx
			case "rx":
				rx, err = parseLength(attr.Value)
			case "ry":
				ry, err = parseLength(attr.Value)
			}
			if err != nil {
				return err
			}
		}
		if !(rx > 0 && ry > 0) { // not drawn, but not an error
			return nil
		}
		c.ellipse(cx, cy, rx, ry)
		return nil
	}
	lineF svgFunc = func(c *iconCursor, attrs []xml.Attr) error {
		var x1, x2, y1, y2 float64
		var err error
		for _, attr := range attrs {
			switch attr.Name.Local {
			case "x1":
				x1, err = parseLength(attr.Value)
			case "x2":
				x2, err = parseLength(attr.Value)
			case "y1":
				y1, err = parseLength(attr.Value)
			case "y2":
				y2, err = parseLength(attr.Value)
			}
			if err != nil {
				return err
			}
		}
		c.Path.Start(c.fixed(x1, y1))
		c.Path.Line(c.fixed(x2, y2))
		return nil
	}
	polylineF svgFunc = func(c *iconCursor, attrs []xml.Attr) error {
		c.points = c.points[:0]
		for _, attr := range attrs {
			if attr.Name.Local != "points" {
				continue
			}
			if err := c.getPoints(attr.Value, false); err != nil {
				return err
			}
			if len(c.points)%2 != 0 {
				return errors.New("odd number of coordinates in points")
			}
		}
		if len(c.points) >= 4 {
			c.Path.Start(c.fixed(c.points[0], c.points[1]))
			for i := 2; i < len(c.points)-1; i += 2 {
				c.Path.Line(c.fixed(c.points[i], c.points[i+1]))
			}
		}
		return nil
	}
	polygonF svgFunc = func(c *iconCursor, attrs []xml.Attr) error {
		err := polylineF(c, attrs)
		if len(c.points) >= 4 {
			c.Path.Stop(true)
		}
		return err
	}
	pathF svgFunc = func(c *iconCursor, attrs []xml.Attr) error {
		for _, attr := range attrs {
			if attr.Name.Local != "d" {
				continue
			}
			if err := c.compilePath(attr.Value); err != nil {
				return err
			}
		}
		return nil
	}
	linearGradientF svgFunc = func(c *iconCursor, attrs []xml.Attr) error {
		return c.readGradient(&rasterx.Gradient{Points: [5]float64{0, 0, 1, 0, 0}, Matrix: rasterx.Identity}, attrs)
	}
	radialGradientF svgFunc = func(c *iconCursor, attrs []xml.Attr) error {
		g := &rasterx.Gradient{Points: [5]float64{0.5, 0.5, 0.5, 0.5, 0.5}, IsRadial: true, Matrix: rasterx.Identity}
		return c.readGradient(g, attrs)
	}
	stopF svgFunc = func(c *iconCursor, attrs []xml.Attr) error {
		if c.gradDepth == 0 {
			return nil
		}
		style := c.styles[len(c.styles)-1]
		clr := style.stopColor
		// Stop colors are opaque for rasterx, which applies their opacity.
		stop := rasterx.GradStop{Opacity: style.stopOpacity * float64(clr.A) / 0xff}
		clr.A = 0xff
		stop.StopColor = clr
		for _, attr := range attrs {
			if attr.Name.Local == "offset" {
				var err error
				if stop.Offset, err = readFraction(attr.Value); err != nil {
					return err
				}
			}
		}
		// Offsets are clamped to [0, 1] and made non-decreasing.
		stop.Offset = min(max(stop.Offset, 0), 1)
		if n := len(c.grad.Stops); n > 0 {
			stop.Offset = max(stop.Offset, c.grad.Stops[n-1].Offset)
		}
		c.grad.Stops = append(c.grad.Stops, stop)
		return nil
	}
	useF svgFunc = func(c *iconCursor, attrs []xml.Attr) error {
		var (
			href string
			x, y float64
			err  error
		)
		for _, attr := range attrs {
			switch attr.Name.Local {
			case "href":
				href = attr.Value
			case "x":
				x, err = parseLength(attr.Value)
			case "y":
				y, err = parseLength(attr.Value)
			}
			if err != nil {
				return err
			}
		}
		top := &c.styles[len(c.styles)-1]
		top.transform = top.transform.Translate(x, y)
		id, ok := strings.CutPrefix(href, "#")
		start, found := c.defIDs[id]
		if !ok || !found {
			return nil
		}
		if c.using[id] {
			return fmt.Errorf("<use> reference cycle through %q", href)
		}
		c.using[id] = true
		defer delete(c.using, id)
		depth := len(c.styles)
		defer func() { c.styles = c.styles[:depth] }()
		// level is the depth in the used element, and skip the depth in an
		// element that is not drawn.
		level, skip := 0, 0
		for _, def := range c.defs[start:] {
			if def.tag == "" {
				level--
				if skip > 0 {
					skip--
				} else {
					c.styles = c.styles[:len(c.styles)-1]
				}
				if level == 0 {
					break
				}
				continue
			}
			level++
			c.expanded += expansionCost(def)
			if c.expanded > maxExpansion {
				return errors.New("<use> elements expand to too much content")
			}
			if _, ok := drawFuncs[def.tag]; skip > 0 || !ok {
				skip++
				continue
			}
			if err := c.pushStyle(def.attrs); err != nil {
				return err
			}
			if err := c.drawElement(def.tag, def.attrs); err != nil {
				return err
			}
		}
		return nil
	}
)

// maxExpansion bounds the content that <use> elements expand to, in bytes of
// markup, where each element counts at least elementCost bytes: about 100k
// small elements.
const (
	maxExpansion = 10 << 20
	elementCost  = 100
)

func expansionCost(def definition) int {
	n := elementCost + len(def.tag)
	for _, a := range def.attrs {
		n += len(a.Name.Local) + len(a.Value)
	}
	return n
}

// useF draws elements through drawFuncs, so adding it to drawFuncs in its
// initializer would be an initialization cycle.
func init() {
	drawFuncs["use"] = useF
}
