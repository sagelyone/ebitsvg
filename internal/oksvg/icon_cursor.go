// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley

package oksvg

import (
	"encoding/xml"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/srwiley/rasterx"
)

// maxMiterLimit bounds miter limits to keep miters, which rasterx measures
// in fixed point, in its range.
const maxMiterLimit = 32

// iconCursor holds the state of parsing an SVG document into an icon.
type iconCursor struct {
	pathCursor
	icon      *Icon
	styles    []pathStyle // the styles of the open elements
	grad      *rasterx.Gradient
	gradDepth int // the number of open gradient elements
	defsDepth int // the number of open <defs> elements
	defs      []definition
	defIDs    map[string]int // the indexes in defs of the elements with ids
	classes   map[string]styleAttribute
	gradDefs  map[*rasterx.Gradient]*gradDef
	using     map[string]bool // the ids of the <use> elements being expanded
	expanded  int             // the cost of expanded <use> elements
}

// definition records the start of an element in <defs>, or its end if tag
// is empty.
type definition struct {
	tag   string
	attrs []xml.Attr
}

// gradPoints maps the geometry attributes of linear and radial gradients to
// their indexes in rasterx.Gradient.Points.
var gradPoints = map[bool]map[string]int{
	false: {"x1": 0, "y1": 1, "x2": 2, "y2": 3},
	true:  {"cx": 0, "cy": 1, "fx": 2, "fy": 3, "r": 4},
}

var (
	gradUnits = map[string]rasterx.GradientUnits{
		"userSpaceOnUse": rasterx.UserSpaceOnUse, "objectBoundingBox": rasterx.ObjectBoundingBox,
	}
	spreadMethods = map[string]rasterx.SpreadMethod{
		"pad": rasterx.PadSpread, "reflect": rasterx.ReflectSpread, "repeat": rasterx.RepeatSpread,
	}
)

// gradDef records the attributes a gradient element sets itself, which
// gradients that reference it inherit unless they set them too.
type gradDef struct {
	href string
	set  map[string]bool
}

// readGradient reads the attributes of a gradient element into g, which
// then collects its stops.
func (c *iconCursor) readGradient(g *rasterx.Gradient, attrs []xml.Attr) error {
	c.grad = g
	def := &gradDef{set: make(map[string]bool)}
	c.gradDefs[g] = def
	for _, attr := range attrs {
		k, v := attr.Name.Local, attr.Value
		var ok bool
		var err error
		switch k {
		case "id":
			c.icon.grads[v] = g
		case "href":
			def.href, _ = strings.CutPrefix(v, "#")
		case "gradientTransform":
			g.Matrix, err = c.parseTransform(v, rasterx.Identity)
			ok = err == nil
		case "gradientUnits":
			var u rasterx.GradientUnits
			if u, ok = gradUnits[strings.TrimSpace(v)]; ok {
				g.Units = u
			}
		case "spreadMethod":
			var m rasterx.SpreadMethod
			if m, ok = spreadMethods[strings.TrimSpace(v)]; ok {
				g.Spread = m
			}
		default:
			var i int
			if i, ok = gradPoints[g.IsRadial][k]; ok {
				g.Points[i], err = readFraction(v)
			}
		}
		if err != nil {
			return err
		}
		if ok {
			def.set[k] = true
		}
	}
	setFocus(g, def.set)
	return nil
}

// setFocus places the focus of a radial gradient at its center unless set.
func setFocus(g *rasterx.Gradient, set map[string]bool) {
	if !g.IsRadial {
		return
	}
	if !set["fx"] {
		g.Points[2] = g.Points[0]
	}
	if !set["fy"] {
		g.Points[3] = g.Points[1]
	}
}

// resolveGradHrefs gives gradients the attributes they do not set, and
// their stops if they have none, from the gradients they reference,
// directly or through others.
func (c *iconCursor) resolveGradHrefs() {
	for g, def := range c.gradDefs {
		set := maps.Clone(def.set)
		id := def.href
		for range len(c.gradDefs) {
			ref := c.icon.grads[id]
			if id == "" || ref == nil || ref == g {
				break
			}
			refDef := c.gradDefs[ref]
			for k := range refDef.set {
				if set[k] {
					continue
				}
				switch k {
				case "gradientTransform":
					g.Matrix = ref.Matrix
				case "gradientUnits":
					g.Units = ref.Units
				case "spreadMethod":
					g.Spread = ref.Spread
				default:
					if g.IsRadial != ref.IsRadial {
						continue
					}
					i := gradPoints[g.IsRadial][k]
					g.Points[i] = ref.Points[i]
				}
				set[k] = true
			}
			if len(g.Stops) == 0 {
				g.Stops = ref.Stops
			}
			id = refDef.href
		}
		setFocus(g, set)
	}
}

// pushStyle parses the style of an element and pushes it on the style stack.
// Presentation attributes apply first, then class rules, then the style
// attribute; color applies before the other properties, which may refer to
// it as currentColor.
func (c *iconCursor) pushStyle(attrs []xml.Attr) error {
	var decls [][2]string
	var class, inline string
	for _, attr := range attrs {
		switch k := strings.ToLower(attr.Name.Local); k {
		case "style":
			inline = attr.Value
		case "class":
			class = attr.Value
		default:
			decls = append(decls, [2]string{k, attr.Value})
		}
	}
	for _, name := range strings.Fields(class) {
		for k, v := range c.classes[name] {
			decls = append(decls, [2]string{k, v})
		}
	}
	for _, pair := range strings.Split(stripComments(inline), ";") {
		if k, v, ok := strings.Cut(pair, ":"); ok {
			decls = append(decls, [2]string{strings.ToLower(strings.TrimSpace(k)), v})
		}
	}
	style := c.styles[len(c.styles)-1]
	inherited := style.opacity
	style.opacity = 1
	for _, colorFirst := range []bool{true, false} {
		for _, d := range decls {
			k, v := d[0], strings.TrimSpace(d[1])
			if (k == "color") != colorFirst || v == "inherit" {
				continue
			}
			if err := c.readStyleAttr(&style, k, v); err != nil {
				return fmt.Errorf("unsupported attribute or style value %s=%q: %w", k, v, err)
			}
		}
	}
	style.opacity *= inherited
	c.styles = append(c.styles, style)
	return nil
}

// applyTransform returns m followed by the transform name with the
// arguments in the cursor's points.
func (c *iconCursor) applyTransform(m rasterx.Matrix2D, name string) (rasterx.Matrix2D, error) {
	p := c.points
	n := len(p)
	switch strings.ToLower(name) {
	case "rotate":
		switch n {
		case 1:
			return m.Rotate(p[0] * math.Pi / 180), nil
		case 3:
			return m.Translate(p[1], p[2]).Rotate(p[0]*math.Pi/180).Translate(-p[1], -p[2]), nil
		}
	case "translate":
		switch n {
		case 1:
			return m.Translate(p[0], 0), nil
		case 2:
			return m.Translate(p[0], p[1]), nil
		}
	case "skewx":
		if n == 1 {
			return m.SkewX(p[0] * math.Pi / 180), nil
		}
	case "skewy":
		if n == 1 {
			return m.SkewY(p[0] * math.Pi / 180), nil
		}
	case "scale":
		switch n {
		case 1:
			return m.Scale(p[0], p[0]), nil
		case 2:
			return m.Scale(p[0], p[1]), nil
		}
	case "matrix":
		if n == 6 {
			return m.Mult(rasterx.Matrix2D{A: p[0], B: p[1], C: p[2], D: p[3], E: p[4], F: p[5]}), nil
		}
	default:
		return m, fmt.Errorf("unknown transform %q", name)
	}
	return m, fmt.Errorf("wrong number of arguments to %s", name)
}

// parseTransform returns m followed by the transform list v.
func (c *iconCursor) parseTransform(v string, m rasterx.Matrix2D) (rasterx.Matrix2D, error) {
	for t := range strings.SplitSeq(v, ")") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		name, args, ok := strings.Cut(t, "(")
		if !ok || args == "" || strings.Contains(args, "(") {
			return m, errors.New("invalid transform syntax")
		}
		if err := c.getPoints(args, false); err != nil {
			return m, err
		}
		var err error
		if m, err = c.applyTransform(m, strings.Trim(name, " \t\r\n,")); err != nil {
			return m, err
		}
	}
	return m, nil
}

// readStyleAttr sets the property k of style to the value v.
func (c *iconCursor) readStyleAttr(style *pathStyle, k, v string) error {
	var err error
	switch k {
	case "fill":
		style.fill, err = parsePaint(v, style.color)
	case "stroke":
		style.stroke, err = parsePaint(v, style.color)
	case "color":
		style.color, err = parseColor(v, style.color)
	case "stop-color":
		style.stopColor, err = parseColor(v, style.color)
	case "fill-rule":
		style.evenOdd = v == "evenodd"
	case "display":
		if v == "none" {
			style.hidden = true
		}
	case "visibility":
		style.invisible = v == "hidden" || v == "collapse"
	case "stroke-linecap":
		switch v {
		case "butt":
			style.lineCap = rasterx.ButtCap
		case "round":
			style.lineCap = rasterx.RoundCap
		case "square":
			style.lineCap = rasterx.SquareCap
		case "cubic":
			style.lineCap = rasterx.CubicCap
		case "quadratic":
			style.lineCap = rasterx.QuadraticCap
		}
	case "stroke-linejoin":
		switch v {
		case "miter":
			style.lineJoin = rasterx.Miter
		case "miter-clip":
			style.lineJoin = rasterx.MiterClip
		case "arc-clip":
			style.lineJoin = rasterx.ArcClip
		case "round":
			style.lineJoin = rasterx.Round
		case "arc", "arcs":
			style.lineJoin = rasterx.Arc
		case "bevel":
			style.lineJoin = rasterx.Bevel
		}
	case "stroke-miterlimit":
		var limit float64
		if limit, err = parseNumber(v); err == nil {
			style.miterLimit = min(max(limit, 1), maxMiterLimit)
		}
	case "stroke-width":
		var width float64
		if width, err = parseLength(v); err == nil && width < 0 {
			err = errNegative
		}
		style.lineWidth = width
	case "stroke-dashoffset":
		style.dashOffset, err = parseLength(v)
	case "stroke-dasharray":
		if v == "none" {
			style.dash = nil
			break
		}
		fields := splitOnCommaOrSpace(v)
		dash := make([]float64, len(fields))
		for i, f := range fields {
			d, err := parseLength(f)
			if err != nil {
				return err
			}
			if d < 0 {
				return errNegative
			}
			dash[i] = d
		}
		style.dash = dash
	case "opacity", "stroke-opacity", "fill-opacity", "stop-opacity":
		var op float64
		if op, err = readFraction(v); err != nil {
			return err
		}
		op = min(max(op, 0), 1)
		switch k {
		case "opacity":
			style.opacity = op
		case "stroke-opacity":
			style.lineOpacity = op
		case "fill-opacity":
			style.fillOpacity = op
		case "stop-opacity":
			style.stopOpacity = op
		}
	case "transform":
		style.transform, err = c.parseTransform(v, style.transform)
	}
	return err
}

// recorded reports whether an element is recorded in defs rather than drawn:
// it is in <defs> and is not a gradient or in one.
func (c *iconCursor) recorded(tag string) bool {
	return c.defsDepth > 0 && c.gradDepth == 0 && !isGradient(tag)
}

func isGradient(tag string) bool {
	return tag == "linearGradient" || tag == "radialGradient"
}

// readStartElement draws an element or, in <defs>, records it for <use>.
func (c *iconCursor) readStartElement(se xml.StartElement) error {
	if !c.recorded(se.Name.Local) {
		return c.drawElement(se.Name.Local, se.Attr)
	}
	for _, attr := range se.Attr {
		if attr.Name.Local == "id" {
			if _, dup := c.defIDs[attr.Value]; !dup {
				c.defIDs[attr.Value] = len(c.defs)
			}
		}
	}
	c.defs = append(c.defs, definition{se.Name.Local, se.Attr})
	return nil
}

func (c *iconCursor) readEndElement(tag string) {
	if c.recorded(tag) {
		c.defs = append(c.defs, definition{})
	}
}

// drawElement adds the paths of an element whose style is on top of the
// stack. An invalid element is drawn up to the first error, which is
// returned only for <use>, whose errors are fatal.
func (c *iconCursor) drawElement(tag string, attrs []xml.Attr) error {
	df, ok := drawFuncs[tag]
	if !ok {
		return nil
	}
	if err := df(c, attrs); err != nil && tag == "use" {
		return err
	}
	if len(c.Path) > 0 {
		style := c.styles[len(c.styles)-1]
		if !style.hidden && !style.invisible {
			c.icon.paths = append(c.icon.paths, svgPath{style, slices.Clone(c.Path)})
		}
		c.Path = c.Path[:0]
	}
	return nil
}
