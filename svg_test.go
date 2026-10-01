package ebitsvg

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// halves is a 20×10 SVG: red on the left half, blue on the right.
const halves = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 10">
	<rect width="10" height="10" fill="#f00"/>
	<rect x="10" width="10" height="10" fill="#00f"/>
</svg>`

var (
	red   = color.RGBA{255, 0, 0, 255}
	blue  = color.RGBA{0, 0, 255, 255}
	black = color.RGBA{0, 0, 0, 255}
)

func mustParse(t testing.TB, src string) *SVG {
	t.Helper()
	s, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		name, src string
		w, h      float64
	}{
		{"viewBox", `<svg viewBox="5 5 20 10" width="200" height="100"/>`, 20, 10},
		{"viewBox with commas", `<svg viewBox="0,0, 20,10"/>`, 20, 10},
		{"width and height", `<svg width="30" height="15"/>`, 30, 15},
		{"px", `<svg width="30px" height=" 15px "/>`, 30, 15},
		{"percent before viewBox", `<svg width="100%" height="100%" viewBox="0 0 20 10"/>`, 20, 10},
		{"percent after viewBox", `<svg viewBox="0 0 20 10" width="100%" height="100%"/>`, 20, 10},
		{"em before viewBox", `<svg width="2em" viewBox="0 0 20 10" height="1em"/>`, 20, 10},
		{"auto before viewBox", `<svg height="auto" viewBox="0 0 20 10"/>`, 20, 10},
		{"mm with viewBox", `<svg width="10mm" height="5mm" viewBox="0 0 20 10"/>`, 20, 10},
		{"absolute units", `<svg width="0.5in" height="3pt"/>`, 48, 4},
		{"ASCII", `<?xml version="1.0" encoding="US-ASCII"?><svg width="3" height="2"/>`, 3, 2},
		{"more absolute units", `<svg width="2.54cm" height="1PC"/>`, 96, 16},
		{"nested svg", `<svg viewBox="0 0 20 10"><svg viewBox="0 0 5 5" width="5" height="5"/></svg>`, 20, 10},
		{"nested svg without viewBox", `<svg width="20" height="10"><svg viewBox="0 0 5 5"/></svg>`, 20, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if w, h := mustParse(t, tt.src).Size(); w != tt.w || h != tt.h {
				t.Errorf("Size() = %v, %v; want %v, %v", w, h, tt.w, tt.h)
			}
		})
	}
}

// fanOut returns an SVG whose <use> elements expand to n^depth rectangles.
func fanOut(n, depth int) string {
	var b strings.Builder
	b.WriteString(`<svg viewBox="0 0 1 1"><defs><rect id="u0" width="1" height="1"/>`)
	for i := 1; i <= depth; i++ {
		fmt.Fprintf(&b, `<g id="u%d">%s</g>`, i, strings.Repeat(fmt.Sprintf(`<use href="#u%d"/>`, i-1), n))
	}
	fmt.Fprintf(&b, `</defs><use href="#u%d"/></svg>`, depth)
	return b.String()
}

// nest returns an SVG document whose root holds inner in n nested <g>
// elements, so that inner's elements are n+2 deep.
func nest(n int, inner string) string {
	return `<svg viewBox="0 0 1 1">` + strings.Repeat("<g>", n) + inner + strings.Repeat("</g>", n) + "</svg>"
}

// TestParseNesting checks that Parse accepts elements nested 256 deep, and
// that it counts only elements.
func TestParseNesting(t *testing.T) {
	for _, src := range []string{
		nest(255, ""),
		nest(254, `<rect width="1" height="1"/>`),
		nest(254, `<g id="/>"><!-- `+strings.Repeat("<g>", 300)+` --></g>`),
		nest(254, `<text><![CDATA[`+strings.Repeat("<g>", 300)+`]]></text>`),
		`<!DOCTYPE svg [<!ENTITY e "http://www.w3.org/2000/svg"><!-- "<g>" -->]>` + nest(255, ""),
	} {
		if _, err := Parse(strings.NewReader(src)); err != nil {
			t.Errorf("Parse(%.60q…): %v", src[len(src)/2:], err)
		}
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ src, want string }{
		{``, "not an SVG document"},
		{`hello`, "not an SVG document"},
		{"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR", "not an SVG document"},
		{`<html><svg viewBox="0 0 1 1"/></html>`, "not an SVG document"},
		{`<svg viewBox="0 0 10 10"`, "not an SVG document"},
		{`<svg viewBox="0 0 10 10"><rect></svg>`, "XML syntax error"},
		{`<svg/>`, "neither a viewBox nor both width and height"},
		{`<svg width="10"/>`, "neither a viewBox nor both width and height"},
		{`<svg viewBox="0 0 10 0"/>`, `invalid viewBox "0 0 10 0"`},
		{`<svg viewBox="0 0 10" width="10" height="10"/>`, "invalid viewBox"},
		{`<svg viewBox="0 0 NaN 10"/>`, "invalid viewBox"},
		{`<svg width="100%" height="100%"/>`, `unsupported root width "100%"`},
		{`<svg width="10" height="2em"/>`, `unsupported root height "2em"`},
		{`<svg width="10mm" height="50%"/>`, `unsupported root height "50%"`},
		{`<?xml version="1.0" encoding="ISO-8859-1"?><svg viewBox="0 0 1 1"/>`, "input must be UTF-8 or ASCII"},
		{`<svg viewBox="0 0 1 1"><text>&bogus;</text></svg>`, "ebitsvg: XML syntax error"},
		{`<svg width="-1" height="10"/>`, `unsupported root width "-1"`},
		{`<svg viewBox="0 0 1 1"><text>a&nbsp;b</text></svg>`, "ebitsvg: XML syntax error: unknown entity reference 'nbsp'"},
		{`<svg viewBox="0 0 1 1"><use xlink:href="#r"/></svg>`, "unknown namespace prefix 'xlink'"},
		{`<svg xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 10 10"><defs>
			<g id="a"><use xlink:href="#b"/></g><g id="b"><use xlink:href="#a"/></g>
		</defs><use xlink:href="#a"/></svg>`, "too many elements"},
		{"\xff\xfe<\x00s\x00v\x00g\x00/\x00>\x00", "UTF-16 input: input must be UTF-8 or ASCII"},
		{"\x1f\x8b\x08\x00\x00\x00\x00\x00", "gzip-compressed input (.svgz): decompress it first"},
		{`<svg viewBox="0 0 1 1"><rect width="1" height="1"/>` + "\xff</svg>", "input must be UTF-8 or ASCII"},
		{fanOut(10, 6), "too many elements"},
		{nest(256, ""), "elements nested too deeply"},
		{nest(255, `<g id="/>"></g>`), "elements nested too deeply"},
		{nest(100000, ""), "elements nested too deeply"},
		{`<!DOCTYPE svg [<!ENTITY e "` + strings.Repeat("<g>", 26) + strings.Repeat("</g>", 26) + `">]><svg viewBox="0 0 1 1">&e;</svg>`,
			"elements nested too deeply"},
		{`<!DOCTYPE svg [<!ENTITY e "` + strings.Repeat("<g>", 25) + "&e;" + strings.Repeat("</g>", 25) + `">]><svg viewBox="0 0 1 1">&e;</svg>`,
			"XML syntax error"},
	}
	for _, tt := range tests {
		_, err := Parse(strings.NewReader(tt.src))
		if err == nil || !strings.HasPrefix(err.Error(), "ebitsvg: ") || strings.Count(err.Error(), "ebitsvg") > 1 ||
			strings.Contains(err.Error(), "strconv") || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Parse(%.40q) error = %v; want one containing %q", tt.src, err, tt.want)
		}
	}
}

// TestParseIgnoresInvalid checks that invalid attribute and style values
// are ignored, as SVG specifies, rather than rejected.
func TestParseIgnoresInvalid(t *testing.T) {
	for _, attrs := range []string{
		`style="fill:var(--c)"`,
		`fill="hsl(1,,)"`,
		`fill="rgb(,,)"`,
		`fill="#12345"`,
		`stroke="url(#g"`,
		`stroke-width="NaN"`,
		`stroke-width="-1"`,
		`stroke-width="abc"`,
		`stroke-width="px"`,
		`stroke-miterlimit=""`,
		`transform="rotate(1,2)"`,
		`transform="spin(1)"`,
		`transform="scale"`,
	} {
		mustParse(t, `<svg viewBox="0 0 1 1"><rect width="1" height="1" `+attrs+`/></svg>`).Rasterize(1, 1)
	}
	for _, src := range []string{
		`<svg viewBox="0 0 1 1"><stop stop-color="rgb(1 2)"/></svg>`,
		`<svg viewBox="0 0 1 1"><style>.a{fill}</style></svg>`,
		`<svg viewBox="0 0 1 1"><style>.a{fill:red} .b</style></svg>`,
		`<svg viewBox="0 0 1 1"><style>.a{fill:red !important}</style><rect class="a"/></svg>`,
		`<svg viewBox="0 0 1 1"><rect width="1" height="1" stroke-width="5%"/></svg>`,
		`<svg viewBox="0 0 1 1"><defs><rect id="r"/></defs><use href="#r" x="1em"/></svg>`,
		`<svg viewBox="0 0 10 10"><defs><g id="a"><use href="#a"/></g></defs><use href="#a"/></svg>`,
		strings.Replace(fanOut(10, 3), `<rect id="u0" width="1" height="1"/>`,
			`<g id="u0">`+strings.Repeat(`<foo/>`, 10000)+`</g>`, 1),
	} {
		mustParse(t, src).Rasterize(1, 1)
	}
}

func TestParseUse(t *testing.T) {
	for _, src := range []string{
		`<svg viewBox="0 0 1 1"><defs><rect id="r" width="1" height="1" fill="#f00"/></defs><use href="#r"/></svg>`,
		`<svg xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 1 1"><defs><g id="g"><rect width="1" height="1" fill="#f00"/></g></defs><use xlink:href="#g"/></svg>`,
		// Elements outside <defs>, and defined after the <use>.
		`<svg viewBox="0 0 1 1"><rect id="r" width="1" height="1" fill="#f00"/><use href="#r"/></svg>`,
		`<svg viewBox="0 0 1 1"><use href="#r"/><rect id="r" width="1" height="1" fill="#00f"/><rect width="1" height="1" fill="#f00"/></svg>`,
		strings.Replace(fanOut(10, 3), `height="1"/>`, `height="1" fill="#f00"/>`, 1),
		// Elements with ids inside a used group.
		`<svg viewBox="0 0 1 1"><defs><g id="g"><rect id="a" width="1" height="1" fill="#00f"/><rect width="1" height="1" fill="#f00"/></g></defs><use href="#g"/></svg>`,
		// An element without an id after one with an id is not part of it.
		`<svg viewBox="0 0 1 1"><defs><rect id="r" width="1" height="1" fill="#f00"/><rect width="1" height="1" fill="#00f"/></defs><use href="#r"/></svg>`,
		// Elements that are not drawn, and their content, are skipped.
		`<svg viewBox="0 0 1 1"><defs><g id="g"><text><tspan>t</tspan></text><foo/><defs><rect width="1" height="1"/></defs>
			<rect width="1" height="1" fill="#f00"/></g></defs><use href="#g"/></svg>`,
		`<svg viewBox="0 0 1 1"><defs><g id="g"><rect width="1" height="1" fill="#f00"/><mask><rect id="m" width="1" height="1"/></mask></g></defs>
			<use href="#g"/></svg>`,
		`<svg viewBox="0 0 1 1"><defs><rect id="r" width="1" height="1" fill="#f00"/><defs><rect id="b" width="1" height="1"/></defs></defs>
			<use href="#b"/><use href="#r"/></svg>`,
	} {
		if got := mustParse(t, src).Rasterize(1, 1).At(0, 0); got != red {
			t.Errorf("%.60q: pixel = %v; want red", src, got)
		}
	}
}

func TestParseSkipsUnsupported(t *testing.T) {
	s := mustParse(t, `<svg xmlns="http://www.w3.org/2000/svg"
		xmlns:sodipodi="http://sodipodi.sourceforge.net/DTD/sodipodi-0.dtd" viewBox="0 0 1 1">
		<sodipodi:namedview/>
		<rect width="1" height="1" fill="#f00"/>
		<text>ignored</text>
	</svg>`)
	if got := s.Rasterize(1, 1).At(0, 0); got != red {
		t.Errorf("pixel = %v; want red", got)
	}
}

func TestParseXML(t *testing.T) {
	for _, src := range []string{
		`<?xml version="1.0" encoding="ASCII"?><svg viewBox="0 0 1 1"><rect width="1" height="1" fill="#f00"/></svg>`,
		`<?xml version="1.0" encoding="us-ascii"?><svg viewBox="0 0 1 1"><rect width="1" height="1" fill="#f00"/></svg>`,
		`<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd" [
			<!ENTITY ns_svg "http://www.w3.org/2000/svg">
			<!ENTITY % param "ignored">
			<!ENTITY ext SYSTEM "ignored.xml">
			<!ENTITY red '#f00'>
		]><svg xmlns="&ns_svg;" viewBox="0 0 1 1"><rect width="1" height="1" fill="&red;"/></svg>`,
	} {
		if got := mustParse(t, src).Rasterize(1, 1).At(0, 0); got != red {
			t.Errorf("%.40q: pixel = %v; want red", src, got)
		}
	}
}

func TestParseStyle(t *testing.T) {
	tests := []struct {
		name, src string
		want      color.RGBA
	}{
		{"currentColor fill", `<rect width="4" height="4" fill="currentColor"/>`, black},
		{"currentColor style", `<rect width="4" height="4" fill="#00f" style="fill: CurrentColor"/>`, black},
		{"currentColor stop", `<defs><linearGradient id="g"><stop stop-color="currentcolor"/></linearGradient></defs>
			<rect width="4" height="4" fill="url(#g)"/>`, black},
		{"color property", `<g color="#00f"><rect width="4" height="4" fill="currentColor" color="#f00"/></g>`, red},
		{"color before currentColor", `<rect width="4" height="4" fill="currentColor" style="color:#f00"/>`, red},
		{"inherit", `<g fill="#f00"><rect width="4" height="4" fill="inherit"/></g>`, red},
		{"rgba", `<rect width="4" height="4" fill="rgba(255, 0, 0, 0.5)"/>`, color.RGBA{128, 0, 0, 128}},
		{"hsl", `<rect width="4" height="4" fill="hsl(240, 100%, 50%)"/>`, blue},
		{"hsla", `<rect width="4" height="4" fill="hsla(-360, 100%, 50%, 1)"/>`, red},
		{"#rgba", `<rect width="4" height="4" fill="#F008"/>`, color.RGBA{0x88, 0, 0, 0x88}},
		{"#rrggbbaa", `<rect width="4" height="4" fill="#0000ff00"/>`, color.RGBA{}},
		{"transparent", `<rect width="4" height="4" fill="#f00"/><rect width="4" height="4" fill="transparent"/>`, red},
		{"fill-opacity inherits", `<g fill-opacity=".25"><rect width="4" height="4" fill="#f00" fill-opacity="1"/></g>`, red},
		{"opacity multiplies", `<g opacity=".5"><rect width="4" height="4" fill="#f00" opacity=".5"/></g>`, color.RGBA{64, 0, 0, 64}},
		{"opacity once", `<rect width="4" height="4" fill="#f00" opacity=".5" style="opacity:.5"/>`, color.RGBA{128, 0, 0, 128}},
		{"class opacity once", `<style>.c{opacity:.5}</style><g opacity=".5"><rect class="c" width="4" height="4" fill="#f00" opacity=".5"/></g>`,
			color.RGBA{64, 0, 0, 64}},
		{"class and style precedence", `<style>.c{fill:#0f0}</style><rect class="c" width="4" height="4" fill="#00f" style="fill:#f00"/>`, red},
		{"class over attribute", `<style><![CDATA[ .c{fill:#f00} ]]></style><rect class="x c" width="4" height="4" fill="#00f"/>`, red},
		{"display none", `<rect width="4" height="4" fill="#f00"/><g display="none"><rect width="4" height="4"/></g>`, red},
		{"visibility", `<g visibility="hidden"><rect width="4" height="4"/><rect width="4" height="4" fill="#f00" visibility="visible"/></g>`, red},
		{"clipPath content", `<rect width="4" height="4" fill="#f00"/><clipPath id="c"><rect width="4" height="4"/></clipPath>`, red},
		{"gradient defined later", `<rect width="4" height="4" fill="url(#g) #00f"/><linearGradient id="g"><stop stop-color="#f00"/></linearGradient>`, red},
		{"missing gradient fallback", `<rect width="4" height="4" fill="url(#none) #f00"/>`, red},
		{"missing gradient", `<rect width="4" height="4" fill="#f00"/><rect width="4" height="4" fill="url(#none)"/>`, red},
		{"gradient href", `<linearGradient id="a"><stop stop-color="#f00"/><stop offset="1" stop-color="#f00"/></linearGradient>
			<linearGradient id="b" href="#a" x1="1" x2="0"/><rect width="4" height="4" fill="url(#b)"/>`, red},
		{"gradient href attributes", `<linearGradient id="a" y1="4" x2="0" gradientUnits="userSpaceOnUse">
			<stop stop-color="#f00"/><stop offset=".5" stop-color="#f00"/><stop offset=".5" stop-color="#00f"/></linearGradient>
			<linearGradient id="b" href="#a"/><linearGradient id="c" href="#b"/><rect width="4" height="4" fill="url(#c)"/>`, red},
		{"radial gradient href attributes", `<radialGradient id="a" cx="0" cy="0" r="8" gradientUnits="userSpaceOnUse">
			<stop stop-color="#f00"/><stop offset=".7" stop-color="#f00"/><stop offset=".7" stop-color="#00f"/></radialGradient>
			<radialGradient id="b" href="#a"/><rect width="4" height="4" fill="url(#b)"/>`, red},
		{"gradient href cycle", `<rect width="4" height="4" fill="#f00"/><linearGradient id="a" href="#b"/><linearGradient id="b" href="#a"/>
			<rect width="4" height="4" fill="url(#a)"/>`, red},
		{"stop style", `<linearGradient id="g"><stop style="stop-color:#f00"/><stop offset="1" style="stop-color:#f00"/></linearGradient>
			<rect width="4" height="4" fill="url(#g)"/>`, red},
		{"scale with one argument", `<rect width="2" height="2" fill="#f00" transform="scale(2)"/>`, red},
		{"transform list with commas", `<rect width="1" height="1" fill="#f00" transform="translate(1, 1), scale(3)"/>`, red},
		{"percentage length", `<rect width="4" height="4" fill="#f00"/><rect width="50%" height="4" fill="#00f"/>`, red},
		{"style comments", `<style>/* <![CDATA[ */ /* c */ .c{fill:#f00} /* ]]> */</style><rect class="c" width="4" height="4"/>`, red},
		{"empty style rule", `<style>.e{} .c{fill:#f00}</style><rect class="c e" width="4" height="4"/>`, red},
		{"style sheets merge", `<style>.c{fill:#f00}</style><style>.c{stroke:#00f;stroke-width:0}</style><rect class="c" width="4" height="4"/>`, red},
		{"style after use", `<rect class="c" width="4" height="4"/><style>.c{fill:#f00}</style>`, red},
		{"grouped class selectors", `<style>.a, .c{fill:#f00}</style><rect class="c" width="4" height="4"/>`, red},
		{"at-rule blocks", `<style>.c{fill:#f00}@media (prefers-color-scheme:dark){rect{fill:#fff}.c{fill:#00f}}
			@supports (fill:red){.c{fill:#00f}}@font-face{font-family:x}</style><rect class="c" width="4" height="4"/>`, red},
		{"style attribute comments", `<rect width="4" height="4" style="fill:#00f;/* c */fill:#f00"/>`, red},
		{"selector specificity", `<style>#c{fill:#f00} rect.c.d, g .c{fill:#00f} *{fill:#0f0}</style>
			<g><rect id="c" class="c d" width="4" height="4"/></g>`, red},
		{"selector list", `<style>.a, g > rect{fill:#f00}</style><g><rect width="4" height="4"/></g>`, red},
		{"fill-rule", `<rect width="4" height="4" fill="#f00"/><g fill-rule="evenodd"><path d="M0 0h4v4H0zM2 2h2v2H2z" fill="#00f"/></g>`, red},
		{"absolute units", `<rect x="0.03125in" width="0.0625in" height="4" fill="#f00"/>`, red},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := mustParse(t, `<svg viewBox="0 0 4 4">`+tt.src+`</svg>`)
			if got := s.Rasterize(4, 4).At(3, 3); got != tt.want {
				t.Errorf("pixel = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestRasterize(t *testing.T) {
	tests := []struct {
		name, src   string
		w, h        int
		left, right color.RGBA
	}{
		{"same aspect", halves, 4, 2, red, blue},
		{"stretched", halves, 2, 4, red, blue},
		{"viewBox origin", strings.Replace(halves, `"0 0 20 10"`, `"10 0 10 10"`, 1), 2, 2, blue, blue},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img := mustParse(t, tt.src).Rasterize(tt.w, tt.h)
			if got := img.Bounds(); got != image.Rect(0, 0, tt.w, tt.h) {
				t.Errorf("bounds = %v; want %dx%d", got, tt.w, tt.h)
			}
			if got := img.At(0, tt.h-1); got != tt.left {
				t.Errorf("left pixel = %v; want %v", got, tt.left)
			}
			if got := img.At(tt.w-1, 0); got != tt.right {
				t.Errorf("right pixel = %v; want %v", got, tt.right)
			}
		})
	}
}

func TestRasterizeSizes(t *testing.T) {
	s := mustParse(t, halves)
	for _, sz := range [][2]int{{0, 0}, {0, 5}, {5, 0}} {
		if got, want := s.Rasterize(sz[0], sz[1]).Bounds(), image.Rect(0, 0, sz[0], sz[1]); got != want {
			t.Errorf("Rasterize(%d, %d) bounds = %v; want %v", sz[0], sz[1], got, want)
		}
	}
	for _, sz := range [][2]int{{-1, 5}, {5, -1}} {
		func() {
			defer func() {
				if r, _ := recover().(string); !strings.HasPrefix(r, "ebitsvg: negative raster size") {
					t.Errorf("Rasterize(%d, %d) panic = %q; want an ebitsvg panic", sz[0], sz[1], r)
				}
			}()
			s.Rasterize(sz[0], sz[1])
		}()
	}
}

// TestRasterizeRoot checks that the root's width, height and
// preserveAspectRatio do not change what Rasterize draws: the viewBox.
func TestRasterizeRoot(t *testing.T) {
	const content = `<rect x="5" y="5" width="10" height="10" fill="#f00"/><circle cx="25" cy="15" r="8" fill="#00f"/>`
	want := mustParse(t, `<svg viewBox="5 5 30 20">`+content+`</svg>`).Rasterize(48, 40).Pix
	for _, root := range []string{
		`<svg viewBox="5 5 30 20" width="100" height="300" preserveAspectRatio="xMidYMid slice">`,
		`<svg width="10mm" viewBox="5 5 30 20" height="2" preserveAspectRatio="xMaxYMin meet">`,
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="5 5 30 20" width="30" height="20" preserveAspectRatio="none">`,
	} {
		if got := mustParse(t, root+content+`</svg>`).Rasterize(48, 40).Pix; !slices.Equal(got, want) {
			t.Errorf("%s: pixels differ from those without width, height and preserveAspectRatio", root)
		}
	}
}

// TestRasterizeFailure checks that a raster the renderer runs out of memory
// for is transparent.
func TestRasterizeFailure(t *testing.T) {
	s := mustParse(t, `<svg viewBox="0 0 10 10">
		<filter id="f" filterUnits="userSpaceOnUse" x="-100" y="-100" width="200" height="200"><feFlood flood-color="#f00"/></filter>
		<rect width="10" height="10" filter="url(#f)"/>
	</svg>`)
	if img := s.Rasterize(4096, 4096); slices.ContainsFunc(img.Pix, func(b uint8) bool { return b != 0 }) {
		t.Error("raster is not transparent")
	}
	if got := s.Rasterize(10, 10).At(5, 5); got != red {
		t.Errorf("pixel after failure = %v; want red", got)
	}
}

// sheet returns a 60×40 sprite sheet with the given content between blue
// rectangles that cover the canvas, which sprites must not draw.
func sheet(viewBox, content string) string {
	const cover = `<rect x="-100" y="-100" width="300" height="300" fill="#00f"/>`
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="` + viewBox + `">` +
		cover + content + `<g id="other">` + cover + `</g></svg>`
}

// TestSprite checks that a sprite draws like an SVG of just its group,
// with the viewBox set to its bounds.
func TestSprite(t *testing.T) {
	const art = `<circle cx="20" cy="20" r="6" fill="#f00"/><rect x="12" y="12" width="4" height="4" fill="#0f0"/>`
	tests := []struct {
		name, src, want string
	}{
		{"padding",
			sheet("0 0 60 40", `<g id="s"><rect x="10" y="10" width="20" height="20" fill="none"/>`+art+`</g>`),
			`<svg viewBox="10 10 20 20">` + art + `</svg>`},
		{"viewBox origin",
			sheet("5 5 60 40", `<g id="s"><rect x="10" y="10" width="20" height="20" fill="none"/>`+art+`</g>`),
			`<svg viewBox="10 10 20 20">` + art + `</svg>`},
		{"bounds in style",
			sheet("0 0 60 40", `<g id="s"><rect x="10" y="10" width="20" height="20" style="fill:none;stroke:none"/>`+art+`</g>`),
			`<svg viewBox="10 10 20 20">` + art + `</svg>`},
		{"bounds in style sheet",
			sheet("0 0 60 40", `<style>.bounds { fill: none }</style><g id="s"><rect class="bounds" x="10" y="10" width="20" height="20"/>`+art+`</g>`),
			`<svg viewBox="10 10 20 20">` + art + `</svg>`},
		{"bounds after art",
			sheet("0 0 60 40", `<g id="s">`+art+`<g><path d="M0 8H40" fill="none"/><rect x="8" y="8" width="24" height="16" fill="none"/></g></g>`),
			`<svg viewBox="8 8 24 16">` + art + `</svg>`},
		{"transformed bounds",
			sheet("0 0 60 40", `<g id="s"><rect width="20" height="20" fill="none" transform="translate(10 10)"/>`+art+`</g>`),
			`<svg viewBox="10 10 20 20">` + art + `</svg>`},
		{"transformed group",
			sheet("0 0 60 40", `<g transform="translate(30 0)"><g id="s" transform="scale(0.5)" opacity="0.5">
				<rect x="10" y="10" width="20" height="20" fill="none"/>`+art+`</g></g>`),
			`<svg viewBox="35 5 10 10"><g transform="translate(30 0) scale(0.5)" opacity="0.5">` + art + `</g></svg>`},
		{"use",
			sheet("0 0 60 40", `<defs><g id="frame"><rect x="10" y="10" width="20" height="20" fill="none"/>`+art+`</g></defs>
				<use id="s" href="#frame" x="20"/>`),
			`<svg viewBox="30 10 20 20"><g transform="translate(20 0)">` + art + `</g></svg>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mustParse(t, tt.src).Sprite("s")
			if err != nil {
				t.Fatal(err)
			}
			want := mustParse(t, tt.want)
			gw, gh := got.Size()
			if ww, wh := want.Size(); gw != ww || gh != wh {
				t.Errorf("Size() = %v, %v; want %v, %v", gw, gh, ww, wh)
			}
			w, h := int(gw*4), int(gh*4)
			if !slices.Equal(got.Rasterize(w, h).Pix, want.Rasterize(w, h).Pix) {
				t.Error("pixels differ from those of an SVG of the group")
			}
		})
	}
}

// TestSpriteOfSprite checks that a sprite finds the other sprites in its
// document.
func TestSpriteOfSprite(t *testing.T) {
	s := mustParse(t, sheet("0 0 60 40", `<g id="a"><rect width="10" height="10" fill="none"/></g>
		<g id="b"><rect x="10" width="20" height="30" fill="none"/></g>`))
	a, err := s.Sprite("a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := a.Sprite("b")
	if err != nil {
		t.Fatal(err)
	}
	if w, h := b.Size(); w != 20 || h != 30 {
		t.Errorf("Size() = %v, %v; want 20, 30", w, h)
	}
}

func TestSpriteErrors(t *testing.T) {
	tests := []struct{ content, id, want string }{
		{`<g id="s"><rect width="10" height="10" fill="none"/></g>`, "t", `no group with id "t"`},
		{`<g id="s"><rect width="10" height="10" fill="none"/></g>`, "", `no group with id ""`},
		{`<g><rect width="10" height="10" fill="none"/></g>`, "", `no group with id ""`},
		{`<rect id="s" width="10" height="10" fill="none"/>`, "s", `no group with id "s"`},
		{`<defs><g id="s"><rect width="10" height="10" fill="none"/></g></defs>`, "s", `no group with id "s"`},
		{`<g id="s"><rect width="10" height="10" fill="#f00"/></g>`, "s", `group "s" has no bounds`},
		{`<g id="s"><rect width="10" height="10" fill="#f00" visibility="hidden"/></g>`, "s", `group "s" has no bounds`},
		{`<g id="s"><path d="M0 0H10" fill="none"/></g>`, "s", `group "s" has no bounds`},
	}
	for _, tt := range tests {
		_, err := mustParse(t, sheet("0 0 60 40", tt.content)).Sprite(tt.id)
		if err == nil || !strings.HasPrefix(err.Error(), "ebitsvg: ") || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Sprite(%q) in %.40q error = %v; want one containing %q", tt.id, tt.content, err, tt.want)
		}
	}
}

// line is a 20×20 SVG with a horizontal line through its middle.
func line(attrs string) string {
	return `<svg viewBox="0 0 20 20"><line x1="0" y1="10" x2="20" y2="10" stroke="#000" ` + attrs + `/></svg>`
}

func TestStrokeWidth(t *testing.T) {
	tests := []struct {
		attrs string
		width float64 // in viewBox units
	}{
		{`stroke-width="2"`, 2},
		{`style="stroke-width:3"`, 3},
		{``, 1},
		{`stroke-width="2" transform="translate(10 10) scale(1.5) translate(-10 -10)"`, 3},
		{`stroke-width="2" transform="rotate(180 10 10)"`, 2},
	}
	for _, tt := range tests {
		t.Run(tt.attrs, func(t *testing.T) {
			s := mustParse(t, line(tt.attrs))
			for _, size := range []int{20, 40, 80, 160} {
				img := s.Rasterize(size, size)
				var got float64
				for y := range size {
					got += float64(img.RGBAAt(size/3, y).A) / 255
				}
				if want := tt.width * float64(size) / 20; math.Abs(got-want) > 0.1 {
					t.Errorf("at %dpx: stroke is %.2fpx wide; want %v", size, got, want)
				}
			}
		})
	}
}

func TestDash(t *testing.T) {
	tests := []struct {
		attrs string
		on    []bool // at x = 2.5, 7.5, 12.5, 17.5 in viewBox units
	}{
		{`stroke-width="2" stroke-dasharray="5 5"`, []bool{true, false, true, false}},
		{`stroke-width="2" stroke-dasharray="5"`, []bool{true, false, true, false}},
		{`stroke-width="2" stroke-dasharray="5 5" stroke-dashoffset="5"`, []bool{false, true, false, true}},
		{`stroke-width="2" stroke-dasharray="5 5" stroke-dashoffset="-5"`, []bool{false, true, false, true}},
		{`stroke-width="2" stroke-dasharray="5 5" stroke-dashoffset="1e6"`, []bool{true, false, true, false}},
		{`stroke-width="2" stroke-dasharray="10 5 5"`, []bool{true, true, false, true}},
	}
	for _, tt := range tests {
		t.Run(tt.attrs, func(t *testing.T) {
			s := mustParse(t, line(tt.attrs))
			for _, size := range []int{20, 80} {
				img := s.Rasterize(size, size)
				for i, want := range tt.on {
					x := (5*i + 2) * size / 20
					if got := img.RGBAAt(x, size/2).A > 127; got != want {
						t.Errorf("at %dpx: inked at x=%d is %v; want %v", size, x, got, want)
					}
				}
			}
		})
	}
}

// Run with -race: rasterizing must not write to the shared SVG.
func TestRasterizeConcurrent(t *testing.T) {
	s := mustParse(t, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10">
		<defs><linearGradient id="g"><stop offset="0" stop-color="#f00"/><stop offset="1" stop-color="#00f"/></linearGradient></defs>
		<circle cx="5" cy="5" r="4" fill="url(#g)" stroke="#000" stroke-dasharray="1 2" stroke-dashoffset="1"/>
	</svg>`)
	want := s.Rasterize(32, 32).Pix
	var wg sync.WaitGroup
	for size := range 8 {
		wg.Go(func() { s.Rasterize(8*size+1, 16) })
		wg.Go(func() {
			if !slices.Equal(s.Rasterize(32, 32).Pix, want) {
				t.Error("concurrent Rasterize produced different pixels")
			}
		})
	}
	wg.Wait()
}

// TestReference compares rasters with references named
// <name>.<width>x<height>.png, rendered from <name>.svg by librsvg 2.62.3 with
//
//	rsvg-convert -w <width> -h <height> <name>.svg -o <name>.<width>x<height>.png
func TestReference(t *testing.T) {
	pngs, err := filepath.Glob("testdata/*.png")
	if err != nil || len(pngs) == 0 {
		t.Fatalf("no reference images: %v", err)
	}
	for _, path := range pngs {
		t.Run(filepath.Base(path), func(t *testing.T) {
			name, size, _ := strings.Cut(strings.TrimSuffix(filepath.Base(path), ".png"), ".")
			var w, h int
			if _, err := fmt.Sscanf(size, "%dx%d", &w, &h); err != nil {
				t.Fatal(err)
			}
			src, err := os.ReadFile(filepath.Join("testdata", name+".svg"))
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			ref, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			got := mustParse(t, string(src)).Rasterize(w, h)
			if ref.Bounds() != got.Bounds() {
				t.Fatalf("reference bounds = %v; want %v", ref.Bounds(), got.Bounds())
			}
			var sum int
			for y := range h {
				for x := range w {
					c, want := got.RGBAAt(x, y), color.RGBAModel.Convert(ref.At(x, y)).(color.RGBA)
					sum += abs(int(c.R)-int(want.R)) + abs(int(c.G)-int(want.G)) +
						abs(int(c.B)-int(want.B)) + abs(int(c.A)-int(want.A))
				}
			}
			mad := float64(sum) / float64(len(got.Pix))
			t.Logf("MAD %.2f", mad)
			if why, ok := knownDifferences[name]; ok {
				if mad <= referenceTolerance {
					t.Fatalf("mean absolute difference %.2f is within tolerance; remove %s from knownDifferences", mad, name)
				}
				t.Skipf("known difference: %s", why)
			}
			if mad > referenceTolerance {
				t.Errorf("mean absolute difference %.2f; want at most %v", mad, referenceTolerance)
			}
		})
	}
}

// referenceTolerance is the largest mean absolute difference, in 8-bit
// premultiplied channel values, accepted from a reference. Antialiasing
// differences between resvg and librsvg stay below 1 at 64 px and larger
// but reach 2.23 at 24 px; the rendering bugs this guards against measured
// 2.4 to 105, and resvg's color and gradient bugs 17 to 20.
const referenceTolerance = 2.3

// knownDifferences holds the SVGs that resvg renders differently from their
// references, and why. TestReference skips them, but fails once one
// matches, so that its entry is removed.
var knownDifferences = map[string]string{
	"colors":    "svgtypes does not parse CSS Color 4 syntax, such as rgb(0 0 0 / 50%)",
	"grad-href": "usvg does not inherit gradientTransform through href",
}

func abs(v int) int { return max(v, -v) }

func TestPathSyntax(t *testing.T) {
	for _, d := range []string{
		"M0 0h4v4H0z",
		"M0 0h.4E1v4e0H-0z",
		"M0 0H1e+1V4h-1E+1z",
		"M.0.0L4-0 4 4-0 4z",
		"M0 2a2 2 0 104 0a2 2 0 10-4 0z",
		"M0 2a2,2,0,1,0,4,0a2 2 0 1 0 -4 0z",
		"M4 0v4h-4a4 4 0 014-4",
		"M4 0v4h-4A4 4 0 0 1 4 0",
	} {
		s := mustParse(t, `<svg viewBox="0 0 4 4"><path fill="#f00" d="`+d+`"/></svg>`)
		if got := s.Rasterize(4, 4).At(1, 2); got != red {
			t.Errorf("%q: pixel = %v; want red", d, got)
		}
	}
}

// FuzzParse checks that Parse and Rasterize survive any input. resvg runs
// as Go code, in which some failures, such as exhausting the goroutine
// stack, crash the program rather than trap.
func FuzzParse(f *testing.F) {
	paths, err := filepath.Glob("testdata/*.svg")
	if err != nil || len(paths) == 0 {
		f.Fatalf("no SVGs: %v", err)
	}
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		// The fuzzer spends most of its time minimizing large inputs.
		if len(src) <= 4<<10 {
			f.Add(string(src))
		}
	}
	f.Add(halves)
	f.Add(line(`stroke-dasharray="1 2" stroke-dashoffset="3" transform="rotate(30 10 10) scale(2)"`))
	f.Add(fanOut(3, 3))
	f.Add(`<svg viewBox="0 0 10 10"><defs><g id="a"><use href="#a"/></g></defs><use href="#a"/></svg>`)
	f.Add(`<svg width="10" height="10"><defs><radialGradient id="g" gradientUnits="userSpaceOnUse"><stop stop-color="hsl(120, 50%, 50%)"/></radialGradient>
		<style>.c{fill:rgb(1,2,3)}</style></defs><path class="c" d="M1 1L9 9A4 4 0 1 1 1 9Z" fill="url(#g)"/>
		<polygon points="1,1 5,1 5,5" stroke="red"/><ellipse cx="5" cy="5" rx="3" ry="2"/></svg>`)
	f.Add(`<svg viewBox="0 0 4 4" color="red"><style>.a{fill:currentColor}</style><linearGradient id="b" href="#a" gradientTransform="skewX(9)"/>
		<linearGradient id="a"><stop offset=".5" style="stop-color:hsla(1,2%,3%,.4)"/></linearGradient>
		<circle class="a" r="2" stroke="url(#b) #f008" stroke-dasharray="1 .5"/><path d="M0 0a0 1 0 0 1 2 2"/></svg>`)
	f.Add(`<svg width="1in" height="2cm"><style>/* c */ .a{fill-rule:evenodd} rect{}</style><defs><g id="a"><path id="p" class="a" d="M0 0h9v9H0zM2 2h5v5H2z"/>
		<text>t</text><defs><rect id="r" width="1e1" height="2E-1mm"/></defs></g></defs><use href="#a" x="1pt"/><use href="#r"/><path d="M1 1a2 2 0 102 2"/></svg>`)
	f.Fuzz(func(t *testing.T, src string) {
		s, err := Parse(strings.NewReader(src))
		if err != nil {
			return
		}
		if w, h := s.Size(); !(w > 0 && h > 0) {
			t.Fatalf("Size() = %v, %v", w, h)
		}
		s.Rasterize(3, 2)
		s.rasterize(2, 2, 0.5, 2, -1, 1)
	})
}
