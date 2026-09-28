package ebitsvg

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

type benchSVG struct {
	name string
	src  []byte
}

// benchSVGs returns the SVGs in testdata. Besides the test files,
// they include three real-world files that their authors released under
// CC0 on Wikimedia Commons: inkscape-nephron.svg ("Nephron
// illustration.svg"), figma-magnetic-dip.svg ("Magnetic Dip.svg") and
// icon-senarist.svg ("Senarist ikon.svg").
func benchSVGs(b *testing.B) []benchSVG {
	paths, err := filepath.Glob("testdata/*.svg")
	if err != nil || len(paths) == 0 {
		b.Fatalf("no SVGs: %v", err)
	}
	var svgs []benchSVG
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			b.Fatal(err)
		}
		svgs = append(svgs, benchSVG{filepath.Base(path), src})
	}
	return svgs
}

func BenchmarkParse(b *testing.B) {
	for _, svg := range benchSVGs(b) {
		b.Run(svg.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := Parse(bytes.NewReader(svg.src)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkRasterize rasterizes each SVG with its longer side at each size.
func BenchmarkRasterize(b *testing.B) {
	for _, svg := range benchSVGs(b) {
		s, err := Parse(bytes.NewReader(svg.src))
		if err != nil {
			b.Fatal(err)
		}
		sw, sh := s.Size()
		for _, size := range []int{24, 64, 512, 2048} {
			k := float64(size) / max(sw, sh)
			w, h := max(1, int(math.Round(sw*k))), max(1, int(math.Round(sh*k)))
			b.Run(fmt.Sprintf("%s/%d", svg.name, size), func(b *testing.B) {
				for b.Loop() {
					s.Rasterize(w, h)
				}
			})
		}
	}
}
