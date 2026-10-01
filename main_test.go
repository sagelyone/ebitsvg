package ebitsvg

import (
	"os"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
)

// testGame runs the tests inside Update, where Ebitengine can read pixels
// back from the GPU.
type testGame struct {
	m    *testing.M
	code int
}

func (g *testGame) Update() error {
	g.code = g.m.Run()
	return ebiten.Termination
}

func (g *testGame) Draw(*ebiten.Image) {}

func (g *testGame) Layout(int, int) (int, int) { return 1, 1 }

// TestMain runs the tests in a game, so they need a display. To run them
// headless, use xvfb-run -a go test ./...
//
// The tests run jobs at once, so that each Draw draws the raster it needs;
// see inBackground.
func TestMain(m *testing.M) {
	start = runAtOnce
	g := &testGame{m: m}
	if err := ebiten.RunGame(g); err != nil {
		panic(err)
	}
	os.Exit(g.code)
}
