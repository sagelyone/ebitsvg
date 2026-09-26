# ebitsvg

Draw SVG images in [Ebitengine] games, sharp at whatever size they are
displayed.

Ebitengine has no public SVG support. One workaround is to rasterize each SVG
once at load time, which blurs it when it is drawn larger and wastes texture
memory when it is drawn smaller; the other is to rebuild the artwork by hand
with `ebiten/vector`. ebitsvg draws the SVG file itself, rasterizing it again
when the size it is displayed at calls for it.

<img src=".github/comparison.png" width="432" alt="The same SVG at 192 px:
rasterized at 48 px and scaled up on the left, drawn by ebitsvg on the right">

The same SVG displayed at 192 px. Left: rasterized once at 48 px and scaled
up 4× with bilinear filtering. Right: drawn by ebitsvg.

[API reference](https://pkg.go.dev/github.com/sagelyone/ebitsvg)

## Install

```sh
go get github.com/sagelyone/ebitsvg
```

ebitsvg needs Go 1.25 or later. On Linux, Ebitengine needs some system
packages; see its [install guide](https://ebitengine.org/en/documents/install.html).

## Quick start

Parse each SVG once, create an `Image` for each place it is drawn, and draw
it into a box from your game's `Draw` method:

```go
package main

import (
	"bytes"
	_ "embed"
	"log"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/sagelyone/ebitsvg"
)

//go:embed logo.svg
var logoSVG []byte

type game struct {
	logo *ebitsvg.Image
}

func (g *game) Update() error { return nil }

func (g *game) Draw(screen *ebiten.Image) {
	b := screen.Bounds()
	g.logo.Draw(screen, 0, 0, float64(b.Dx()), float64(b.Dy()), nil)
}

// LayoutF returns whole device pixels, so that the logo is sharp on
// high-DPI displays, including at fractional scale factors.
func (g *game) LayoutF(w, h float64) (float64, float64) {
	s := ebiten.Monitor().DeviceScaleFactor()
	return math.Round(w * s), math.Round(h * s)
}

// Layout is never called, because game implements ebiten.LayoutFer.
func (g *game) Layout(int, int) (int, int) { panic("unreachable") }

func main() {
	svg, err := ebitsvg.Parse(bytes.NewReader(logoSVG))
	if err != nil {
		log.Fatal(err)
	}
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	if err := ebiten.RunGame(&game{logo: ebitsvg.NewImage(svg)}); err != nil {
		log.Fatal(err)
	}
}
```

To watch an SVG follow the window size, run the example:

```sh
go run github.com/sagelyone/ebitsvg/examples/resize@latest
```

## Drawing

`Draw(dst, x, y, w, h, opts)` fits the SVG into the w×h box at (x, y). A nil
`opts` fits it inside the box, centered. `DrawOptions` has:

- `Fit`: `Contain` (the zero value) fits the SVG inside the box, `Cover`
  fills the box and clips the overflow, `Stretch` fills it exactly, ignoring
  the aspect ratio.
- `AlignX`, `AlignY`: where a `Contain` or `Cover` fit sits in the box, from
  -1 (left or top) through 0 (centered) to 1 (right or bottom).
- `GeoM`: transforms the placed box, for rotation, zoom or a camera. Its
  scale counts towards the displayed size, so zooming in rasterizes at the
  zoomed size.
- `ColorScale`, `Blend`: passed to `DrawImage`.

`Prepare` takes `Draw`'s arguments except `dst` and rasterizes now what
`Draw` settles on, so that drawing with them later does not rasterize. Use it
to render ahead of time, such as behind a loading screen.

`SVG.Rasterize(w, h)` renders to an `*image.RGBA` on the CPU, for a window
icon or any other one-off image. It stretches the SVG to w×h; `SVG.Size`
gives the aspect ratio. `Size` is the viewBox size where there is one, not
the display size that width and height set: Material Symbols, for example,
report 960×960.

Sharpness is measured in pixels of `dst`, so `dst` must reach the screen
unscaled. On high-DPI displays, have `LayoutF` return device pixels, as above.
An SVG is sharp up to 4096 pixels on a side. Displayed larger, it is
magnified from a 4096-pixel raster and slightly soft, and a `Cover` fit's
clip edge can be off by up to half a raster pixel, about 1 px at 8192 px.

## How it works

An `Image` first draws a raster made for exactly the pixels it covers, 1:1.
While its size or subpixel position keeps changing, it draws from a raster
made at twice the displayed size, and reuses that raster until the displayed
size halves or doubles. Resizing a window therefore rasterizes only now and
then, and the raster is not magnified when drawn.

Once the same size and position have lasted two game ticks, the `Image`
draws an exact raster again. It keeps the 2× raster until they have lasted 60
ticks, and if they change within that time, it waits those 60 ticks before
making another exact raster. Content that moves in steps therefore reuses its
rasters, and a still `Image` ends up holding one. Moving by whole pixels keeps
the exact raster. A rotated or flipped `GeoM` stays on the 2× raster.

An `Image` caches for one target, so use one `Image` for each independently
drawn use of an SVG. A parsed `SVG` is immutable and safe to share, even
across goroutines; an `Image` is not safe for concurrent use.

## SVG support

Rendering uses a modified copy of [oksvg], drawn with [rasterx]. `Parse`
reads UTF-8 or ASCII documents and supports paths and basic shapes; fills,
with either fill rule, and strokes, including dashes, caps and joins; linear
and radial gradients; opacity; transforms; lengths in user units or absolute
units (px, in, cm, mm, pt and pc, at 96 px per inch); `<style>` rules for
classes; `<use>` of elements in `<defs>`; and colors as names, `#rgb`,
`#rgba`, `#rrggbb`, `#rrggbbaa`, `rgb()`, `rgba()`, `hsl()`, `hsla()` and
`currentColor`, which is the `color` property, black unless the SVG sets it.

Not supported:

- text, images, `<symbol>`, `<marker>` and `<pattern>`, which are skipped;
- `filter`, `clip-path` and `mask`, which are ignored: content is drawn
  unfiltered and unclipped;
- nested `<svg>` viewports, whose content is drawn in the parent's
  coordinates;
- `<switch>`, all of whose children are drawn;
- `paint-order`, `mix-blend-mode` and `vector-effect`, which are ignored:
  fills are drawn before strokes, blending is normal, and strokes scale with
  transforms;
- `<use>` of elements outside `<defs>`, or defined after the `<use>`, which
  draws nothing;
- opacity of an element or group as a whole: it applies to each fill and
  stroke separately, so overlaps show through;
- relative lengths (`%`, `em`, `ex`): on shapes they leave the shape
  undrawn, in stroke widths, dashes and `<use>` x and y `Parse` rejects them,
  and in gradients with `gradientUnits="userSpaceOnUse"`, percentages are
  fractions of a user unit;
- CSS selectors other than a class: a rule with `.a, rect` applies to class
  `a` only, and selectors such as `rect`, `#id`, `.a.b` and `.a .b` match
  nothing; class rules apply in the order of the `class` attribute rather
  than the style sheet, rules in at-rules such as `@media` are ignored,
  and values such as `var()` or `!important` are rejected.

Stroke widths and dashes scale with transforms by the square root of their
area scale factor, so they are exact under uniform scaling.

`Parse` rejects documents whose root element is not `<svg>`, that are
compressed (.svgz) or encoded other than as UTF-8 or ASCII, whose size cannot
be determined (see `SVG.Size`), that have unsupported style values, or whose
`<use>` elements form a cycle or expand to an excessive amount of content. It
reads all of its input: to parse untrusted input, limit its size with
`io.LimitReader`.

To recolor an icon drawn in `currentColor`, replace that keyword with a color
before parsing:

```go
src = regexp.MustCompile(`(?i)currentcolor`).ReplaceAll(src, []byte("tomato"))
```

## Testing

Tests need a display. On headless Linux, run them with `xvfb-run -a go test
./...`. The reference images in `testdata` are rendered with `rsvg-convert`;
the comment on `TestReference` in `svg_test.go` gives the command.

## License

MIT. `internal/oksvg` is derived from [oksvg] and keeps its BSD 3-Clause
license, in [internal/oksvg/LICENSE](internal/oksvg/LICENSE), so programs
distributed in binary form must reproduce that notice too.

[Ebitengine]: https://ebitengine.org
[oksvg]: https://github.com/srwiley/oksvg
[rasterx]: https://github.com/srwiley/rasterx
