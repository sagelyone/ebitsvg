// Bounce plays a sprite sheet animation of a ball bouncing, drawn from the
// frames in ball.svg. Drag the window edges to see it stay sharp.
package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"image/color"
	"log"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/sagelyone/ebitsvg"
)

//go:embed ball.svg
var ballSVG []byte

// cycle is the order the frames play in: falling, squashing on the ground,
// and rising again. Each frame shows for the same time, and the ball moves
// further between later frames, so it speeds up as it falls.
var cycle = []int{0, 1, 2, 3, 4, 5, 4, 3, 2, 1}

// ticksPerFrame plays the cycle in two-thirds of a second at 60 TPS.
const ticksPerFrame = 4

type game struct {
	// Each frame gets its own Image, which keeps a raster for it, so once
	// every frame has been shown, the animation rasterizes nothing until
	// the window size changes.
	frames   []*ebitsvg.Image
	prepared bool
	ticks    int
}

func (g *game) Update() error {
	g.ticks++
	return nil
}

func (g *game) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{0xf4, 0xef, 0xe6, 0xff})
	w, h := float64(screen.Bounds().Dx()), float64(screen.Bounds().Dy())
	if !g.prepared {
		// An Image draws nothing until its first raster is ready, so each
		// frame would flicker the first time it is shown.
		for _, f := range g.frames {
			f.Prepare(0, 0, w, h, nil)
		}
		g.prepared = true
	}
	frame := cycle[g.ticks/ticksPerFrame%len(cycle)]
	g.frames[frame].Draw(screen, 0, 0, w, h, nil)
}

// LayoutF returns the window size in whole device pixels, so that the ball
// is rasterized at the display's full resolution, even at fractional scale
// factors.
func (g *game) LayoutF(width, height float64) (float64, float64) {
	s := ebiten.Monitor().DeviceScaleFactor()
	return math.Round(width * s), math.Round(height * s)
}

// Layout is never called, because game implements ebiten.LayoutFer.
func (g *game) Layout(int, int) (int, int) { panic("unreachable") }

func main() {
	sheet, err := ebitsvg.Parse(bytes.NewReader(ballSVG))
	if err != nil {
		log.Fatal(err)
	}
	g := &game{frames: make([]*ebitsvg.Image, 6)}
	for i := range g.frames {
		s, err := sheet.Sprite(fmt.Sprintf("bounce-%d", i))
		if err != nil {
			log.Fatal(err)
		}
		g.frames[i] = ebitsvg.NewImage(s)
	}
	ebiten.SetWindowTitle("ebitsvg: a sprite sheet animation")
	ebiten.SetWindowSize(320, 480)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}
