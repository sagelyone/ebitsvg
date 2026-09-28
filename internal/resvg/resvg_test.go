package resvg

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var update = flag.Bool("update", false, "update testdata/pixels.txt")

// TestMain sets GOMAXPROCS, which sizes the pool, so that TestPool renders
// a few large rasters rather than dozens.
func TestMain(m *testing.M) {
	runtime.GOMAXPROCS(2)
	os.Exit(m.Run())
}

// TestPool checks that instances that grew to render a large raster are
// dropped, and that at most GOMAXPROCS idle instances are kept.
func TestPool(t *testing.T) {
	m := instances()
	src := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"><rect width="1" height="1" fill="#f00"/></svg>`)
	render := func(size int) {
		var wg sync.WaitGroup
		for range 2 * cap(m.idle) {
			wg.Go(func() {
				dst := make([]byte, size*size*4)
				if err := Render(src, size, size, float32(size), float32(size), 0, 0, dst); err != nil {
					t.Error(err)
				} else if dst[len(dst)-4] != 0xff || dst[len(dst)-1] != 0xff {
					t.Errorf("%d×%d: last pixel = %v; want red", size, size, dst[len(dst)-4:])
				}
			})
		}
		wg.Wait()
	}

	render(4096)
	if n := len(m.idle); n != 0 {
		t.Errorf("%d idle instances after rendering 4096×4096; want 0", n)
	}
	render(16)
	if n := len(m.idle); n < 1 || n > runtime.GOMAXPROCS(0) {
		t.Errorf("%d idle instances; want 1 to %d", n, runtime.GOMAXPROCS(0))
	}
	for range len(m.idle) {
		in := <-m.idle
		if size := len(in.memory()); size > pooledLimit {
			t.Errorf("idle instance has %d bytes of memory; want at most %d", size, pooledLimit)
		}
		m.idle <- in
	}
}

// TestTrap checks that a trap is reported as an error, that the instance
// that trapped is dropped, and that later calls work.
func TestTrap(t *testing.T) {
	p := instances()
	var trapped *instance
	err := p.run(func(in *instance) error {
		trapped = in
		// Allocating more than the memory limit aborts, which traps.
		in.mod.Xalloc(1 << 30)
		return nil
	})
	if !errors.As(err, new(trapError)) {
		t.Fatalf("err = %v; want a trapError", err)
	}
	for range len(p.idle) {
		in := <-p.idle
		if in == trapped {
			t.Error("the instance that trapped is idle")
		}
		p.idle <- in
	}
	if err := Parse([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>`)); err != nil {
		t.Error(err)
	}
}

// TestPixels checks that every SVG in testdata renders the pixels that
// testdata/pixels.txt records. WebAssembly arithmetic is deterministic, so
// the pixels must be the same on every platform; run with -update after
// changing the renderer.
func TestPixels(t *testing.T) {
	paths, err := filepath.Glob("../../testdata/*.svg")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no SVGs: %v", err)
	}
	const size = 128
	var got bytes.Buffer
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		dst := make([]byte, size*size*4)
		if err := Render(src, size, size, 1, 1, 0, 0, dst); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		fmt.Fprintf(&got, "%x  %s\n", sha256.Sum256(dst), filepath.Base(path))
	}
	if *update {
		if err := os.WriteFile("testdata/pixels.txt", got.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile("testdata/pixels.txt")
	if err != nil {
		t.Fatal(err)
	}
	gotLines, wantLines := strings.Split(got.String(), "\n"), strings.Split(string(want), "\n")
	if len(gotLines) != len(wantLines) {
		t.Fatalf("rendered %d SVGs; testdata/pixels.txt has %d", len(gotLines)-1, len(wantLines)-1)
	}
	for i := range gotLines {
		if gotLines[i] != wantLines[i] {
			t.Errorf("got %q; want %q", gotLines[i], wantLines[i])
		}
	}
}
