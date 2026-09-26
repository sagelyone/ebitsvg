// Resize draws an SVG that follows the window size: drag the window edges
// to see it. A small copy spins and fades in the corner.
package main

import (
	"bytes"
	_ "embed"
	"image/color"
	"log"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/sagelyone/ebitsvg"
)

//go:embed emblem.svg
var emblemSVG []byte

type game struct {
	// Each use of the SVG gets its own Image, because an Image caches rasters
	// for one target.
	emblem, spinner *ebitsvg.Image
	ticks           int
}

func (g *game) Update() error {
	g.ticks++
	return nil
}

func (g *game) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{0xf4, 0xef, 0xe6, 0xff})
	w, h := float64(screen.Bounds().Dx()), float64(screen.Bounds().Dy())

	// Contain, the zero Fit, fills as much of the window as the SVG's aspect
	// ratio allows.
	margin := min(w, h) / 20
	g.emblem.Draw(screen, margin, margin, w-2*margin, h-2*margin, nil)

	size := min(w, h) / 5
	t := float64(g.ticks) / float64(ebiten.TPS())
	opts := &ebitsvg.DrawOptions{}
	opts.GeoM.Translate(-size/2, -size/2)
	opts.GeoM.Rotate(t)
	opts.GeoM.Translate(w-size*0.75, h-size*0.75)
	opts.ColorScale.ScaleAlpha(float32(0.6 + 0.4*math.Cos(2*t)))
	g.spinner.Draw(screen, 0, 0, size, size, opts)
}

// LayoutF returns the window size in whole device pixels, so that the SVG
// is rasterized at the display's full resolution, even at fractional scale
// factors.
func (g *game) LayoutF(width, height float64) (float64, float64) {
	s := ebiten.Monitor().DeviceScaleFactor()
	return math.Round(width * s), math.Round(height * s)
}

// Layout is never called, because game implements ebiten.LayoutFer.
func (g *game) Layout(int, int) (int, int) { panic("unreachable") }

func main() {
	svg, err := ebitsvg.Parse(bytes.NewReader(emblemSVG))
	if err != nil {
		log.Fatal(err)
	}
	ebiten.SetWindowTitle("ebitsvg: resize the window")
	ebiten.SetWindowSize(640, 480)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	g := &game{emblem: ebitsvg.NewImage(svg), spinner: ebitsvg.NewImage(svg)}
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}
