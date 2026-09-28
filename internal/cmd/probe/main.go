// Probe measures how long ebitsvg takes to start and how much memory it
// uses, on Linux. It reports the time of the first Parse and the peak and
// current resident set size (VmHWM and VmRSS) after it, after rasterizing
// the SVG at size×size, and after returning freed memory to the system.
//
// Usage:
//
//	go run ./internal/cmd/probe [-size n] file.svg
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/sagelyone/ebitsvg"
)

func main() {
	size := flag.Int("size", 2048, "rasterize at `n`×n pixels")
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	src, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	start := time.Now()
	svg, err := ebitsvg.Parse(bytes.NewReader(src))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("first Parse: %v\n", time.Since(start).Round(time.Millisecond))
	report("after Parse")
	svg.Rasterize(*size, *size)
	report("after Rasterize")
	runtime.GC()
	debug.FreeOSMemory()
	report("after FreeOSMemory")
}

func report(when string) {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		log.Fatal(err)
	}
	fields := make(map[string]string)
	for line := range strings.Lines(string(status)) {
		if k, v, ok := strings.Cut(line, ":"); ok {
			fields[k] = strings.TrimSpace(v)
		}
	}
	fmt.Printf("%s: VmHWM %s, VmRSS %s\n", when, fields["VmHWM"], fields["VmRSS"])
}
