// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley

package oksvg

import (
	"errors"
	"image/color"
	"math"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/image/colornames"
)

// parsePaint parses a fill or stroke value, returning nil for none.
func parsePaint(v string, current color.NRGBA) (*paint, error) {
	if rest, ok := strings.CutPrefix(v, "url("); ok {
		ref, fallback, ok := strings.Cut(rest, ")")
		id := strings.TrimPrefix(strings.Trim(strings.TrimSpace(ref), `"'`), "#")
		if !ok || id == "" {
			return nil, errors.New("invalid url")
		}
		p := &paint{grad: id}
		if fallback = strings.TrimSpace(fallback); fallback != "" && fallback != "none" {
			var err error
			if p.color, err = parseColor(fallback, current); err != nil {
				return nil, err
			}
		}
		return p, nil
	}
	if v == "none" {
		return nil, nil
	}
	c, err := parseColor(v, current)
	if err != nil {
		return nil, err
	}
	return &paint{color: c}, nil
}

// parseColor parses a CSS color: a name, #rgb, #rgba, #rrggbb, #rrggbbaa,
// rgb(), rgba(), hsl(), hsla(), transparent or currentColor.
func parseColor(v string, current color.NRGBA) (color.NRGBA, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "currentcolor":
		return current, nil
	case "transparent":
		return color.NRGBA{}, nil
	}
	if c, ok := colornames.Map[v]; ok {
		return color.NRGBA{c.R, c.G, c.B, 0xff}, nil
	}
	if hex, ok := strings.CutPrefix(v, "#"); ok {
		return parseHexColor(hex)
	}
	name, args, open := strings.Cut(v, "(")
	args, closed := strings.CutSuffix(args, ")")
	if !open || !closed {
		return color.NRGBA{}, errors.New("unsupported color")
	}
	f := strings.FieldsFunc(args, func(r rune) bool { return r == ',' || r == '/' || unicode.IsSpace(r) })
	if len(f) != 3 && len(f) != 4 {
		return color.NRGBA{}, errors.New("invalid color")
	}
	c := [4]float64{3: 1} // in [0, 1]
	var err error
	switch strings.TrimSpace(name) {
	case "rgb", "rgba":
		for i, s := range f {
			if c[i], err = colorComponent(s, i < 3); err != nil {
				return color.NRGBA{}, err
			}
		}
	case "hsl", "hsla":
		h, err := parseNumber(strings.TrimSuffix(f[0], "deg"))
		if err != nil {
			return color.NRGBA{}, err
		}
		for i, s := range f[1:] {
			if c[i+1], err = colorComponent(s, false); err != nil {
				return color.NRGBA{}, err
			}
		}
		c[0], c[1], c[2] = hslToRGB(h, c[1], c[2])
	default:
		return color.NRGBA{}, errors.New("unsupported color")
	}
	var b [4]uint8
	for i, v := range c {
		b[i] = uint8(math.Round(v * 0xff))
	}
	return color.NRGBA{b[0], b[1], b[2], b[3]}, nil
}

// colorComponent parses a percentage or a number, which is out of 255 for an
// rgb() channel and out of 1 otherwise, returning it clamped to [0, 1].
func colorComponent(s string, channel bool) (float64, error) {
	d := 1.0
	if channel {
		d = 255
	}
	if p, ok := strings.CutSuffix(s, "%"); ok {
		s, d = p, 100
	}
	v, err := parseNumber(s)
	if err != nil {
		return 0, err
	}
	return min(max(v/d, 0), 1), nil
}

// parseHexColor parses the digits of a #rgb, #rgba, #rrggbb or #rrggbbaa
// color.
func parseHexColor(hex string) (color.NRGBA, error) {
	if len(hex) == 3 || len(hex) == 4 {
		b := make([]byte, 0, 8)
		for i := range len(hex) {
			b = append(b, hex[i], hex[i])
		}
		hex = string(b)
	}
	if len(hex) == 6 {
		hex += "ff"
	}
	n, err := strconv.ParseUint(hex, 16, 32)
	if len(hex) != 8 || err != nil {
		return color.NRGBA{}, errors.New("invalid color")
	}
	return color.NRGBA{uint8(n >> 24), uint8(n >> 16), uint8(n >> 8), uint8(n)}, nil
}

// hslToRGB converts a hue in degrees and a saturation and lightness in
// [0, 1] to red, green and blue in [0, 1].
func hslToRGB(h, s, l float64) (r, g, b float64) {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	h /= 60
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h, 2)-1))
	switch int(h) {
	case 0:
		r, g = c, x
	case 1:
		r, g = x, c
	case 2:
		g, b = c, x
	case 3:
		g, b = x, c
	case 4:
		r, b = x, c
	default:
		r, b = c, x
	}
	m := l - c/2
	return r + m, g + m, b + m
}
