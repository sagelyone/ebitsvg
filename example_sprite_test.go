package ebitsvg_test

import (
	"fmt"
	"log"
	"strings"

	"github.com/sagelyone/ebitsvg"
)

func ExampleSVG_Sprite() {
	// Each sprite is a group with an id. A rectangle without fill or
	// stroke sets its bounds: here, 16×16 cells with 2 units of padding
	// around a 12-unit disc.
	sheet, err := ebitsvg.Parse(strings.NewReader(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 16">
		<g id="on">
			<rect width="16" height="16" fill="none"/>
			<circle cx="8" cy="8" r="6" fill="#fc0"/>
		</g>
		<g id="off">
			<rect x="16" width="16" height="16" fill="none"/>
			<circle cx="24" cy="8" r="6" fill="#888"/>
		</g>
	</svg>`))
	if err != nil {
		log.Fatal(err)
	}
	for _, id := range []string{"on", "off"} {
		s, err := sheet.Sprite(id)
		if err != nil {
			log.Fatal(err)
		}
		// In a game, draw the sprite with ebitsvg.NewImage(s).
		w, h := s.Size()
		img := s.Rasterize(16, 16)
		fmt.Println(id, w, h, img.At(8, 8), img.At(0, 0))
	}
	// Output:
	// on 16 16 {255 204 0 255} {0 0 0 0}
	// off 16 16 {136 136 136 255} {0 0 0 0}
}
