package ebitsvg_test

import (
	"fmt"
	"log"
	"strings"

	"github.com/sagelyone/ebitsvg"
)

func ExampleSVG_Rasterize() {
	svg, err := ebitsvg.Parse(strings.NewReader(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 2 1">
		<rect width="1" height="1" fill="red"/>
		<rect x="1" width="1" height="1" fill="blue"/>
	</svg>`))
	if err != nil {
		log.Fatal(err)
	}
	img := svg.Rasterize(64, 32)
	fmt.Println(img.Bounds())
	fmt.Println(img.At(16, 16), img.At(48, 16))
	// Output:
	// (0,0)-(64,32)
	// {255 0 0 255} {0 0 255 255}
}
