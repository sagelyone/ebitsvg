// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley

package oksvg

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// absoluteUnits holds the sizes of absolute length units in user units, at
// 96 user units per inch.
var absoluteUnits = map[string]float64{
	"px": 1, "in": 96, "cm": 96 / 2.54, "mm": 96 / 25.4, "pt": 96.0 / 72, "pc": 16,
}

// parseLength parses a number, optionally in an absolute unit, in user
// units. Relative units, such as % or em, are not supported.
func parseLength(s string) (float64, error) {
	s = strings.TrimSpace(s)
	i := len(s)
	for i > 0 && (s[i-1] == '%' || 'a' <= s[i-1]|0x20 && s[i-1]|0x20 <= 'z') {
		i--
	}
	v, err := parseNumber(s[:i])
	if err != nil {
		return 0, fmt.Errorf("invalid number %q", s)
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

var errNegative = errors.New("negative value")

// parseNumber parses a finite number.
func parseNumber(s string) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, fmt.Errorf("invalid number %q", s)
	}
	return v, nil
}

// splitOnCommaOrSpace splits s around commas and white space.
func splitOnCommaOrSpace(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
}

// parseClasses adds to classes the declarations of the rules in a style
// sheet whose selectors are classes. Other rules, and at-rules, are ignored.
func parseClasses(css string, classes map[string]styleAttribute) error {
	css = stripComments(css)
	for {
		i := strings.IndexAny(css, "{;}")
		if i < 0 {
			if s := strings.TrimSpace(css); s != "" {
				return fmt.Errorf("invalid <style> rule %q", s)
			}
			return nil
		}
		prelude := strings.TrimSpace(css[:i])
		switch css[i] {
		case ';': // ends an at-rule statement, such as @import
			css = css[i+1:]
			continue
		case '}':
			if prelude != "" {
				return fmt.Errorf("invalid <style> rule %q", prelude)
			}
			css = css[i+1:]
			continue
		}
		end := blockEnd(css[i:]) + i
		body := css[i+1 : end]
		css = css[min(end+1, len(css)):]
		if strings.HasPrefix(prelude, "@") {
			continue
		}
		if err := addRule(classes, prelude, body); err != nil {
			return fmt.Errorf("invalid <style> rule %q: %w", prelude+"{"+body+"}", err)
		}
	}
}

// blockEnd returns the index of the brace that closes the block that s
// starts with, or len(s) if it is not closed.
func blockEnd(s string) int {
	depth := 0
	for i := range len(s) {
		switch s[i] {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return len(s)
}

// addRule adds the declarations in body to the classes that selectors, a
// selector list, names.
func addRule(classes map[string]styleAttribute, selectors, body string) error {
	var attrs styleAttribute
	for sel := range strings.SplitSeq(selectors, ",") {
		class, ok := strings.CutPrefix(strings.TrimSpace(sel), ".")
		if !ok || !isIdent(class) {
			continue
		}
		if attrs == nil {
			var err error
			if attrs, err = parseAttrs(body); err != nil {
				return err
			}
		}
		if classes[class] == nil {
			classes[class] = make(styleAttribute, len(attrs))
		}
		maps.Copy(classes[class], attrs)
	}
	return nil
}

// stripComments removes the comments from a style sheet.
func stripComments(css string) string {
	var b strings.Builder
	for {
		before, after, ok := strings.Cut(css, "/*")
		b.WriteString(before)
		if !ok {
			return b.String()
		}
		_, css, _ = strings.Cut(after, "*/")
	}
}

// isIdent reports whether s is a CSS identifier: letters, digits, hyphens,
// underscores and non-ASCII characters.
func isIdent(s string) bool {
	for _, r := range s {
		if !(r == '-' || r == '_' || r >= 0x80 || unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return false
		}
	}
	return s != ""
}

// parseAttrs parses a list of CSS declarations.
func parseAttrs(s string) (styleAttribute, error) {
	decls := strings.Split(s, ";")
	res := make(styleAttribute, len(decls))
	for _, kv := range decls {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		k, v, ok := strings.Cut(kv, ":")
		if !ok {
			return res, fmt.Errorf("invalid declaration %q", kv)
		}
		res[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return res, nil
}

// readFraction parses a percentage as a fraction, or a length.
func readFraction(v string) (float64, error) {
	v = strings.TrimSpace(v)
	if p, ok := strings.CutSuffix(v, "%"); ok {
		f, err := parseLength(p)
		return f / 100, err
	}
	return parseLength(v)
}
