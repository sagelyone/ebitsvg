// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley

package oksvg

import (
	"errors"
	"math"
	"strings"
	"unicode"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/fixed"
)

// pathCursor builds a rasterx path from SVG path data and basic shapes.
type pathCursor struct {
	rasterx.Path
	placeX, placeY         float64
	cntlPtX, cntlPtY       float64
	pathStartX, pathStartY float64
	points                 []float64
	lastKey                uint8
	inPath                 bool

	// scale is the number of path units per user unit. Paths are in fixed
	// point, so scaling them up keeps them precise in small user spaces.
	scale float64
}

// fixed returns a point in user space in path units.
func (c *pathCursor) fixed(x, y float64) fixed.Point26_6 {
	return fixed.Point26_6{
		X: fixed.Int26_6(math.Round(x * c.scale * 64)),
		Y: fixed.Int26_6(math.Round(y * c.scale * 64))}
}

// addArc adds an arc from the current point to the path, as rasterx.AddArc
// does in user space.
func (c *pathCursor) addArc(points []float64, cx, cy float64) {
	s := c.scale
	p := []float64{points[0] * s, points[1] * s, points[2], points[3], points[4], points[5] * s, points[6] * s}
	x, y := rasterx.AddArc(p, cx*s, cy*s, c.placeX*s, c.placeY*s, &c.Path)
	c.placeX, c.placeY = x/s, y/s
}

var (
	errArgCount       = errors.New("wrong number of arguments")
	errCommandUnknown = errors.New("unknown path command")
)

// getPoints reads the numbers in s into the cursor's points, skipping
// characters that are not part of a number. If arc is set, it reads the
// arguments of arc commands, whose flags need no separators.
func (c *pathCursor) getPoints(s string, arc bool) error {
	c.points = c.points[:0]
	for s != "" {
		if i := len(c.points) % 7; arc && (i == 3 || i == 4) {
			s = strings.TrimLeft(s, ", \t\n\r\f")
			if s == "" {
				break
			}
			if s[0] != '0' && s[0] != '1' {
				return errArgCount
			}
			c.points = append(c.points, float64(s[0]-'0'))
			s = s[1:]
			continue
		}
		n := numberLen(s)
		if n == 0 {
			s = s[1:]
			continue
		}
		v, err := parseNumber(s[:n])
		if err != nil {
			return err
		}
		c.points = append(c.points, v)
		s = s[n:]
	}
	return nil
}

// numberLen returns the length of the number at the start of s, or 0 if
// there is none.
func numberLen(s string) int {
	i := 0
	digits := func() int {
		start := i
		for i < len(s) && '0' <= s[i] && s[i] <= '9' {
			i++
		}
		return i - start
	}
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	n := digits()
	if i < len(s) && s[i] == '.' {
		i++
		n += digits()
	}
	if n == 0 {
		return 0
	}
	if end := i; i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if digits() == 0 {
			i = end
		}
	}
	return i
}

// ellipse adds an ellipse centered at (cx, cy) with radii rx and ry.
func (c *pathCursor) ellipse(cx, cy, rx, ry float64) {
	c.placeX, c.placeY = cx+rx, cy
	c.Path.Start(c.fixed(c.placeX, c.placeY))
	// SVG ellipses run in the positive angle direction, which dashes follow,
	// in two halves because rasterx draws a full arc the other way.
	for _, x := range []float64{cx - rx, cx + rx} {
		c.addArc([]float64{rx, ry, 0, 0, 1, x, cy}, cx, cy)
	}
	c.Path.Stop(true)
}

// arcTo adds an elliptical arc from the current point, given the seven
// arguments of an SVG arc command in absolute coordinates.
func (c *pathCursor) arcTo(points []float64) {
	cx, cy := rasterx.FindEllipseCenter(&points[0], &points[1], points[2]*math.Pi/180, c.placeX,
		c.placeY, points[5], points[6], points[4] == 0, points[3] == 0)
	c.addArc(points, cx, cy)
}

// compilePath sets the path to the SVG path data d, up to its first error.
func (c *pathCursor) compilePath(d string) error {
	c.reset()
	lastIndex := -1
	for i, v := range d {
		if unicode.IsLetter(v) && v != 'e' && v != 'E' {
			if lastIndex != -1 {
				if err := c.addSeg(d[lastIndex:i]); err != nil {
					return err
				}
			}
			lastIndex = i
		}
	}
	if lastIndex != -1 {
		if err := c.addSeg(d[lastIndex:]); err != nil {
			return err
		}
	}
	return nil
}

func reflect(px, py, rx, ry float64) (x, y float64) {
	return px*2 - rx, py*2 - ry
}

func (c *pathCursor) valsToAbs(last float64) {
	for i := 0; i < len(c.points); i++ {
		last += c.points[i]
		c.points[i] = last
	}
}

func (c *pathCursor) pointsToAbs(sz int) {
	lastX := c.placeX
	lastY := c.placeY
	for j := 0; j < len(c.points); j += sz {
		for i := 0; i < sz; i += 2 {
			c.points[i+j] += lastX
			c.points[i+1+j] += lastY
		}
		lastX = c.points[(j+sz)-2]
		lastY = c.points[(j+sz)-1]
	}
}

func (c *pathCursor) hasSetsOrMore(sz int, rel bool) bool {
	if !(len(c.points) >= sz && len(c.points)%sz == 0) {
		return false
	}
	if rel {
		c.pointsToAbs(sz)
	}
	return true
}

func (c *pathCursor) reflectControlQuad() {
	switch c.lastKey {
	case 'q', 'Q', 'T', 't':
		c.cntlPtX, c.cntlPtY = reflect(c.placeX, c.placeY, c.cntlPtX, c.cntlPtY)
	default:
		c.cntlPtX, c.cntlPtY = c.placeX, c.placeY
	}
}

func (c *pathCursor) reflectControlCube() {
	switch c.lastKey {
	case 'c', 'C', 's', 'S':
		c.cntlPtX, c.cntlPtY = reflect(c.placeX, c.placeY, c.cntlPtX, c.cntlPtY)
	default:
		c.cntlPtX, c.cntlPtY = c.placeX, c.placeY
	}
}

// addSeg adds a segment of path data: a command and its arguments.
func (c *pathCursor) addSeg(seg string) error {
	k := seg[0]
	if err := c.getPoints(seg[1:], k == 'a' || k == 'A'); err != nil {
		return err
	}
	l := len(c.points)
	rel := false
	switch k {
	case 'z', 'Z':
		if len(c.points) != 0 {
			return errArgCount
		}
		if c.inPath {
			c.Path.Stop(true)
			c.placeX = c.pathStartX
			c.placeY = c.pathStartY
			c.inPath = false
		}
	case 'm':
		rel = true
		fallthrough
	case 'M':
		if !c.hasSetsOrMore(2, rel) {
			return errArgCount
		}
		c.pathStartX, c.pathStartY = c.points[0], c.points[1]
		c.inPath = true
		c.Path.Start(c.fixed(c.pathStartX, c.pathStartY))
		for i := 2; i < l-1; i += 2 {
			c.Path.Line(c.fixed(c.points[i], c.points[i+1]))
		}
		c.placeX = c.points[l-2]
		c.placeY = c.points[l-1]
	case 'l':
		rel = true
		fallthrough
	case 'L':
		if !c.hasSetsOrMore(2, rel) {
			return errArgCount
		}
		for i := 0; i < l-1; i += 2 {
			c.Path.Line(c.fixed(c.points[i], c.points[i+1]))
		}
		c.placeX = c.points[l-2]
		c.placeY = c.points[l-1]
	case 'v':
		c.valsToAbs(c.placeY)
		fallthrough
	case 'V':
		if !c.hasSetsOrMore(1, false) {
			return errArgCount
		}
		for _, p := range c.points {
			c.Path.Line(c.fixed(c.placeX, p))
		}
		c.placeY = c.points[l-1]
	case 'h':
		c.valsToAbs(c.placeX)
		fallthrough
	case 'H':
		if !c.hasSetsOrMore(1, false) {
			return errArgCount
		}
		for _, p := range c.points {
			c.Path.Line(c.fixed(p, c.placeY))
		}
		c.placeX = c.points[l-1]
	case 'q':
		rel = true
		fallthrough
	case 'Q':
		if !c.hasSetsOrMore(4, rel) {
			return errArgCount
		}
		for i := 0; i < l-3; i += 4 {
			c.Path.QuadBezier(
				c.fixed(c.points[i], c.points[i+1]),
				c.fixed(c.points[i+2], c.points[i+3]))
		}
		c.cntlPtX, c.cntlPtY = c.points[l-4], c.points[l-3]
		c.placeX = c.points[l-2]
		c.placeY = c.points[l-1]
	case 't':
		rel = true
		fallthrough
	case 'T':
		if !c.hasSetsOrMore(2, rel) {
			return errArgCount
		}
		for i := 0; i < l-1; i += 2 {
			c.reflectControlQuad()
			c.Path.QuadBezier(
				c.fixed(c.cntlPtX, c.cntlPtY),
				c.fixed(c.points[i], c.points[i+1]))
			c.lastKey = k
			c.placeX = c.points[i]
			c.placeY = c.points[i+1]
		}
	case 'c':
		rel = true
		fallthrough
	case 'C':
		if !c.hasSetsOrMore(6, rel) {
			return errArgCount
		}
		for i := 0; i < l-5; i += 6 {
			c.Path.CubeBezier(
				c.fixed(c.points[i], c.points[i+1]),
				c.fixed(c.points[i+2], c.points[i+3]),
				c.fixed(c.points[i+4], c.points[i+5]))
		}
		c.cntlPtX, c.cntlPtY = c.points[l-4], c.points[l-3]
		c.placeX = c.points[l-2]
		c.placeY = c.points[l-1]
	case 's':
		rel = true
		fallthrough
	case 'S':
		if !c.hasSetsOrMore(4, rel) {
			return errArgCount
		}
		for i := 0; i < l-3; i += 4 {
			c.reflectControlCube()
			c.Path.CubeBezier(c.fixed(c.cntlPtX, c.cntlPtY),
				c.fixed(c.points[i], c.points[i+1]),
				c.fixed(c.points[i+2], c.points[i+3]))
			c.lastKey = k
			c.cntlPtX, c.cntlPtY = c.points[i], c.points[i+1]
			c.placeX = c.points[i+2]
			c.placeY = c.points[i+3]
		}
	case 'a', 'A':
		if !c.hasSetsOrMore(7, false) {
			return errArgCount
		}
		for i := 0; i < l-6; i += 7 {
			if k == 'a' {
				c.points[i+5] += c.placeX
				c.points[i+6] += c.placeY
			}
			if c.points[i] == 0 || c.points[i+1] == 0 {
				// An arc with a zero radius is a line.
				c.placeX, c.placeY = c.points[i+5], c.points[i+6]
				c.Path.Line(c.fixed(c.placeX, c.placeY))
				continue
			}
			c.points[i], c.points[i+1] = math.Abs(c.points[i]), math.Abs(c.points[i+1])
			c.arcTo(c.points[i:])
		}
	default:
		return errCommandUnknown
	}
	c.lastKey = k
	return nil
}

// reset clears the path and the current point.
func (c *pathCursor) reset() {
	c.placeX = 0
	c.placeY = 0
	c.points = c.points[:0]
	c.lastKey = ' '
	c.Path.Clear()
	c.inPath = false
}
