package ebitsvg

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode"
)

var errNotSVG = errors.New("not an SVG document")

// normalize checks the encoding and the root element of the SVG document b
// and returns its size and a copy with the root's width and height set to
// that size. resvg scales the viewBox to width and height, so the copy's
// user space is the viewBox, only offset by its origin, whatever
// preserveAspectRatio says. The copy also declares the SVG namespace if b
// declares no default namespace: resvg draws elements in no namespace but
// only detects <use> cycles among those in the SVG namespace.
func normalize(b []byte) (src []byte, w, h float64, err error) {
	switch {
	case bytes.HasPrefix(b, []byte{0x1f, 0x8b}):
		return nil, 0, 0, errors.New("gzip-compressed input (.svgz): decompress it first")
	case bytes.HasPrefix(b, []byte{0xfe, 0xff}), bytes.HasPrefix(b, []byte{0xff, 0xfe}):
		return nil, 0, 0, errors.New("UTF-16 input: input must be UTF-8 or ASCII")
	}
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Entity = make(map[string]string)
	d.CharsetReader = func(label string, r io.Reader) (io.Reader, error) {
		switch strings.ToLower(label) {
		case "ascii", "us-ascii":
			return r, nil
		}
		return nil, fmt.Errorf("unsupported encoding %q: input must be UTF-8 or ASCII", label)
	}
	for {
		start := d.InputOffset()
		t, err := d.RawToken()
		if err == io.EOF {
			return nil, 0, 0, errNotSVG
		}
		if _, ok := err.(*xml.SyntaxError); ok {
			return nil, 0, 0, fmt.Errorf("%w: %w", errNotSVG, err)
		}
		if err != nil {
			return nil, 0, 0, err
		}
		switch t := t.(type) {
		case xml.Directive:
			if bytes.Contains(t, []byte("<!ENTITY")) {
				declareEntities(string(t), d.Entity)
			}
		case xml.StartElement:
			if t.Name.Local != "svg" {
				return nil, 0, 0, errNotSVG
			}
			if w, h, err = rootSize(t.Attr); err != nil {
				return nil, 0, 0, err
			}
			end := d.InputOffset()
			return rewriteRoot(b, start, end, t, w, h), w, h, nil
		}
	}
}

// rewriteRoot returns a copy of b with the root start tag root, which spans
// b[start:end], rewritten with width w and height h.
func rewriteRoot(b []byte, start, end int64, root xml.StartElement, w, h float64) []byte {
	var buf bytes.Buffer
	buf.Grow(len(b) + 64)
	buf.Write(b[:start])
	buf.WriteByte('<')
	writeName(&buf, root.Name)
	xmlns := false
	for _, a := range root.Attr {
		if a.Name.Space == "" && (a.Name.Local == "width" || a.Name.Local == "height") {
			continue
		}
		xmlns = xmlns || a.Name == xml.Name{Local: "xmlns"}
		buf.WriteByte(' ')
		writeName(&buf, a.Name)
		buf.WriteString(`="`)
		xml.EscapeText(&buf, []byte(a.Value))
		buf.WriteByte('"')
	}
	if !xmlns && root.Name.Space == "" {
		buf.WriteString(` xmlns="http://www.w3.org/2000/svg"`)
	}
	fmt.Fprintf(&buf, ` width="%s" height="%s"`, strconv.FormatFloat(w, 'g', -1, 64), strconv.FormatFloat(h, 'g', -1, 64))
	if bytes.HasSuffix(b[:end], []byte("/>")) {
		buf.WriteString("/>")
	} else {
		buf.WriteByte('>')
	}
	buf.Write(b[end:])
	return buf.Bytes()
}

// writeName writes a name as RawToken returned it, with its prefix.
func writeName(buf *bytes.Buffer, n xml.Name) {
	if n.Space != "" {
		buf.WriteString(n.Space)
		buf.WriteByte(':')
	}
	buf.WriteString(n.Local)
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

// rootSize returns the size of the root element: that of its viewBox or,
// without one, its width and height.
func rootSize(attrs []xml.Attr) (w, h float64, err error) {
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
	if viewBox != nil {
		f := strings.FieldsFunc(*viewBox, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
		if len(f) != 4 {
			return 0, 0, fmt.Errorf("invalid viewBox %q", *viewBox)
		}
		var vb [4]float64
		for i := range vb {
			if vb[i], err = parseNumber(f[i]); err != nil {
				return 0, 0, fmt.Errorf("invalid viewBox %q", *viewBox)
			}
		}
		if !(vb[2] > 0 && vb[3] > 0) {
			return 0, 0, fmt.Errorf("invalid viewBox %q", *viewBox)
		}
		return vb[2], vb[3], nil
	}
	if width == nil || height == nil {
		return 0, 0, errors.New("root <svg> has neither a viewBox nor both width and height")
	}
	if w, err = pixels("width", *width); err != nil {
		return 0, 0, err
	}
	if h, err = pixels("height", *height); err != nil {
		return 0, 0, err
	}
	return w, h, nil
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

// absoluteUnits holds the sizes of absolute length units in user units, at
// 96 user units per inch.
var absoluteUnits = map[string]float64{
	"px": 1, "in": 96, "cm": 96 / 2.54, "mm": 96 / 25.4, "pt": 96.0 / 72, "pc": 16,
}

// parseLength parses a number, optionally in an absolute unit, in user
// units.
func parseLength(s string) (float64, error) {
	s = strings.TrimSpace(s)
	i := len(s)
	for i > 0 && (s[i-1] == '%' || 'a' <= s[i-1]|0x20 && s[i-1]|0x20 <= 'z') {
		i--
	}
	v, err := parseNumber(s[:i])
	if err != nil {
		return 0, err
	}
	if unit := strings.ToLower(s[i:]); unit != "" {
		k, ok := absoluteUnits[unit]
		if !ok {
			return 0, fmt.Errorf("unsupported unit %q", unit)
		}
		v *= k
	}
	return v, nil
}

// parseNumber parses a finite number.
func parseNumber(s string) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, fmt.Errorf("invalid number %q", s)
	}
	return v, nil
}
