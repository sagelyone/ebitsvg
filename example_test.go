package ebitsvg_test

import (
	"log"
	"math"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/sagelyone/ebitsvg"
)

const logo = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">
	<circle cx="12" cy="12" r="10" fill="#fc0" stroke="#000"/>
</svg>`

type Game struct {
	logo *ebitsvg.Image
}

func (g *Game) Update() error { return nil }

func (g *Game) Draw(screen *ebiten.Image) {
	// The logo fills the window however it is resized. While resizing, it
	// is rasterized again only when its size halves or doubles, and once
	// more at the exact size soon after resizing stops.
	b := screen.Bounds()
	g.logo.Draw(screen, 0, 0, float64(b.Dx()), float64(b.Dy()), nil)
}

// LayoutF returns whole device pixels, so that the logo is sharp on
// high-DPI displays, including at fractional scale factors.
func (g *Game) LayoutF(w, h float64) (float64, float64) {
	s := ebiten.Monitor().DeviceScaleFactor()
	return math.Round(w * s), math.Round(h * s)
}

// Layout is never called, because Game implements ebiten.LayoutFer.
func (g *Game) Layout(int, int) (int, int) { panic("unreachable") }

func Example() {
	svg, err := ebitsvg.Parse(strings.NewReader(logo))
	if err != nil {
		log.Fatal(err)
	}
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	if err := ebiten.RunGame(&Game{logo: ebitsvg.NewImage(svg)}); err != nil {
		log.Fatal(err)
	}
}
