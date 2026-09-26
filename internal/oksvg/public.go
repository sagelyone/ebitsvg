// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley

package oksvg

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"strings"
	"unicode"

	"github.com/srwiley/rasterx"
)

var errNotSVG = errors.New("not an SVG document")

// unrendered holds the elements whose content is not drawn where it
// appears, or at all.
var unrendered = map[string]bool{
	"clipPath": true, "mask": true, "pattern": true, "marker": true,
	"symbol": true, "foreignObject": true, "text": true,
}

// ReadIcon reads an SVG document. It supports a subset of SVG, enough to
// draw many icons, and skips elements it does not support.
func ReadIcon(r io.Reader) (*Icon, error) {
	icon := &Icon{grads: make(map[string]*rasterx.Gradient)}
	cursor := &iconCursor{
		styles:   []pathStyle{defaultStyle},
		icon:     icon,
		defIDs:   make(map[string]int),
		classes:  make(map[string]styleAttribute),
		gradDefs: make(map[*rasterx.Gradient]*gradDef),
		using:    make(map[string]bool),
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	switch {
	case bytes.HasPrefix(b, []byte{0x1f, 0x8b}):
		return nil, errors.New("gzip-compressed input (.svgz): decompress it first")
	case bytes.HasPrefix(b, []byte{0xfe, 0xff}), bytes.HasPrefix(b, []byte{0xff, 0xfe}):
		return nil, errors.New("UTF-16 input: input must be UTF-8 or ASCII")
	}
	sheets, entities := scan(b)
	root := true
	skip := 0 // the depth within an unrendered element
	decoder := newDecoder(b, entities)
	for {
		t, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			var syntaxErr *xml.SyntaxError
			if root && errors.As(err, &syntaxErr) {
				return nil, fmt.Errorf("%w: %w", errNotSVG, err)
			}
			return nil, err
		}
		switch se := t.(type) {
		case xml.StartElement:
			if root {
				if se.Name.Local != "svg" {
					return nil, errNotSVG
				}
				if err := cursor.readRoot(se.Attr); err != nil {
					return nil, err
				}
				for _, css := range sheets {
					if err := parseClasses(css, cursor.classes); err != nil {
						return nil, err
					}
				}
				root = false
			}
			if skip > 0 || unrendered[se.Name.Local] {
				skip++
				continue
			}
			if err := cursor.pushStyle(se.Attr); err != nil {
				return nil, err
			}
			if err := cursor.readStartElement(se); err != nil {
				return nil, err
			}
			switch tag := se.Name.Local; {
			case tag == "defs":
				cursor.defsDepth++
			case isGradient(tag):
				cursor.gradDepth++
			}
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			cursor.styles = cursor.styles[:len(cursor.styles)-1]
			switch tag := se.Name.Local; {
			case tag == "defs":
				cursor.defsDepth--
			case isGradient(tag):
				cursor.gradDepth--
			}
			cursor.readEndElement(se.Name.Local)
		}
	}
	if root {
		return nil, errNotSVG
	}
	cursor.resolveGradHrefs()
	return icon, nil
}

// scan returns the content of each <style> element in an SVG document, so
// that its rules apply to elements before it too, and the entities the
// document's DOCTYPE declares. It stops at the first error, which parsing
// the document then reports.
func scan(b []byte) (sheets []string, entities map[string]string) {
	entities = xml.HTMLEntity
	d := newDecoder(b, entities)
	var css *strings.Builder
	root := true
	for {
		t, err := d.Token()
		if err != nil {
			return sheets, entities
		}
		switch t := t.(type) {
		case xml.StartElement:
			root = false
			if t.Name.Local == "style" {
				css = new(strings.Builder)
			}
		case xml.EndElement:
			if css != nil {
				sheets = append(sheets, css.String())
				css = nil
			}
		case xml.CharData:
			if css != nil {
				css.Write(t)
			}
		case xml.Directive:
			if root && bytes.Contains(t, []byte("<!ENTITY")) {
				entities = maps.Clone(xml.HTMLEntity)
				declareEntities(string(t), entities)
				d.Entity = entities
			}
		}
	}
}

// declareEntities adds to entities the general entities that a DOCTYPE
// declares with literal values.
func declareEntities(s string, entities map[string]string) {
	for {
		var ok bool
		if _, s, ok = strings.Cut(s, "<!ENTITY"); !ok {
			return
		}
		s = strings.TrimLeftFunc(s, unicode.IsSpace)
		i := strings.IndexFunc(s, unicode.IsSpace)
		if i < 0 {
			return
		}
		name := s[:i]
		s = strings.TrimLeftFunc(s[i:], unicode.IsSpace)
		if name == "%" || s == "" || (s[0] != '"' && s[0] != '\'') {
			continue
		}
		var value string
		if value, s, ok = strings.Cut(s[1:], s[:1]); !ok {
			return
		}
		entities[name] = value
	}
}

// newDecoder returns a decoder of b that expands entities and accepts UTF-8
// and ASCII.
func newDecoder(b []byte, entities map[string]string) *xml.Decoder {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Entity = entities
	d.CharsetReader = func(label string, r io.Reader) (io.Reader, error) {
		switch strings.ToLower(label) {
		case "ascii", "us-ascii":
			return r, nil
		}
		return nil, fmt.Errorf("unsupported encoding %q: input must be UTF-8 or ASCII", label)
	}
	return d
}

// readRoot sets the icon's viewBox from the root element: its viewBox or,
// without one, its width and height.
func (c *iconCursor) readRoot(attrs []xml.Attr) error {
	var viewBox, width, height *string
	for _, a := range attrs {
		if a.Name.Space != "" {
			continue
		}
		switch a.Name.Local {
		case "viewBox":
			viewBox = &a.Value
		case "width":
			width = &a.Value
		case "height":
			height = &a.Value
		}
	}
	vb := &c.icon.ViewBox
	if viewBox != nil {
		f := splitOnCommaOrSpace(*viewBox)
		if len(f) != 4 {
			return fmt.Errorf("invalid viewBox %q", *viewBox)
		}
		for i, p := range []*float64{&vb.X, &vb.Y, &vb.W, &vb.H} {
			var err error
			if *p, err = parseNumber(f[i]); err != nil {
				return fmt.Errorf("invalid viewBox %q", *viewBox)
			}
		}
		if !(vb.W > 0 && vb.H > 0) {
			return fmt.Errorf("invalid viewBox %q", *viewBox)
		}
		c.setScale()
		return nil
	}
	if width == nil || height == nil {
		return errors.New("root <svg> has neither a viewBox nor both width and height")
	}
	var err error
	if vb.W, err = pixels("width", *width); err != nil {
		return err
	}
	if vb.H, err = pixels("height", *height); err != nil {
		return err
	}
	c.setScale()
	return nil
}

// setScale sets the scale of paths so that the viewBox spans about 1024
// path units, which fixed point resolves to 1/64. Paths then overflow
// beyond 32768 times the viewBox.
func (c *iconCursor) setScale() {
	vb := c.icon.ViewBox
	c.scale = math.Exp2(min(max(math.Floor(math.Log2(1024/max(vb.W, vb.H))), 0), 32))
	c.icon.pathScale = c.scale
}

// pixels parses a root width or height, which without a viewBox must be in
// absolute units: relative ones, such as % or em, have no size without a
// container or a font.
func pixels(name, s string) (float64, error) {
	v, err := parseLength(s)
	if err != nil || !(v > 0) {
		return 0, fmt.Errorf("unsupported root %s %q: without a viewBox, width and height must be positive and in absolute units, such as px or mm", name, s)
	}
	return v, nil
}
