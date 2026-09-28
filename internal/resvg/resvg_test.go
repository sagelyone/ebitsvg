package resvg

import (
	"bytes"
	"container/list"
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
	"weak"
)

var update = flag.Bool("update", false, "update testdata/pixels.txt")

// TestMain sets GOMAXPROCS, which sizes the pool, so that TestPool renders
// a few large rasters rather than dozens.
func TestMain(m *testing.M) {
	runtime.GOMAXPROCS(2)
	os.Exit(m.Run())
}

func mustParse(t testing.TB, p *pool, src []byte) *Doc {
	t.Helper()
	d, err := p.parse(src)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// fill returns an SVG document that fills a 1×1 image with the color
// #rrggbb, and draws n more rectangles outside it, each of which adds about
// 400 bytes to its tree.
func fill(rrggbb string, n int) []byte {
	return fmt.Appendf(nil, `<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"><rect width="1" height="1" fill="#%s"/>%s</svg>`,
		rrggbb, strings.Repeat(`<rect x="2" width="1" height="1"/>`, n))
}

// checkFill renders d, a document from fill, and checks its pixel.
func checkFill(t testing.TB, d *Doc, rrggbb string) {
	t.Helper()
	dst := make([]byte, 4)
	if err := d.Render(1, 1, 1, 1, 0, 0, dst); err != nil {
		t.Error(err)
	} else if got := fmt.Sprintf("%02x%02x%02x%02x", dst[0], dst[1], dst[2], dst[3]); got != rrggbb+"ff" {
		t.Errorf("pixel = #%s; want #%sff", got, rrggbb)
	}
}

// withIdle calls f with the only idle instance of p.
func withIdle(t *testing.T, p *pool, f func(*instance)) {
	t.Helper()
	if n := len(p.idle); n != 1 {
		t.Fatalf("%d idle instances; want 1", n)
	}
	f(p.idle[0])
}

// TestPool checks that instances that grew to render a large raster are
// dropped, that at most GOMAXPROCS idle instances are kept, and that each
// keeps the tree of the Doc it rendered.
func TestPool(t *testing.T) {
	p := instances()
	d, err := Parse(fill("ff0000", 0))
	if err != nil {
		t.Fatal(err)
	}
	render := func(size int) {
		var wg sync.WaitGroup
		for range 2 * p.max {
			wg.Go(func() {
				dst := make([]byte, size*size*4)
				if err := d.Render(size, size, float32(size), float32(size), 0, 0, dst); err != nil {
					t.Error(err)
				} else if dst[len(dst)-4] != 0xff || dst[len(dst)-1] != 0xff {
					t.Errorf("%d×%d: last pixel = %v; want red", size, size, dst[len(dst)-4:])
				}
			})
		}
		wg.Wait()
	}

	render(4096)
	if n := len(p.idle); n != 0 {
		t.Errorf("%d idle instances after rendering 4096×4096; want 0", n)
	}
	render(16)
	if n := len(p.idle); n < 1 || n > runtime.GOMAXPROCS(0) {
		t.Errorf("%d idle instances; want 1 to %d", n, runtime.GOMAXPROCS(0))
	}
	for _, in := range p.idle {
		if size := len(in.memory()); size > pooledLimit {
			t.Errorf("idle instance has %d bytes of memory; want at most %d", size, pooledLimit)
		}
		if in.trees.elems[d.key] == nil {
			t.Error("idle instance does not keep the tree of the Doc it rendered")
		}
	}
}

// TestPoolReuse checks that rendering one document after another uses the
// same instance, rather than parsing the document in each idle one.
func TestPoolReuse(t *testing.T) {
	p := &pool{max: 3}
	for _, in := range []*instance{p.acquire(), p.acquire(), p.acquire()} {
		p.release(in)
	}
	d := mustParse(t, p, fill("00ff00", 0))
	for range 3 {
		checkFill(t, d, "00ff00")
	}
	var n int
	for _, in := range p.idle {
		n += in.trees.lru.Len()
	}
	if n != 1 {
		t.Errorf("idle instances keep %d trees; want 1", n)
	}
}

// TestTrap checks that a trap is reported as an error, that the instance
// that trapped is dropped, and that later calls work.
func TestTrap(t *testing.T) {
	p := &pool{max: 1}
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
	for _, in := range p.idle {
		if in == trapped {
			t.Error("the instance that trapped is idle")
		}
	}
	checkFill(t, mustParse(t, p, fill("0000ff", 0)), "0000ff")
}

// TestTreeReused checks that rendering a Doc reuses the tree that parsing
// it kept.
func TestTreeReused(t *testing.T) {
	p := &pool{max: 1}
	d := mustParse(t, p, fill("0080ff", 0))
	var e *list.Element
	withIdle(t, p, func(in *instance) { e = in.trees.elems[d.key] })
	if e == nil {
		t.Fatal("Parse did not keep the tree")
	}
	for range 3 {
		checkFill(t, d, "0080ff")
	}
	withIdle(t, p, func(in *instance) {
		if in.trees.elems[d.key] != e || in.trees.lru.Len() != 1 {
			t.Error("Render parsed the Doc again")
		}
	})
}

// TestDocsCollected checks that the trees an instance keeps do not keep
// their Docs from being garbage collected.
func TestDocsCollected(t *testing.T) {
	p := &pool{max: 1}
	d := mustParse(t, p, fill("000000", 0))
	checkFill(t, d, "000000")
	w := weak.Make(d)
	for try := 0; w.Value() != nil; try++ {
		if try == 10 {
			t.Fatal("a Doc whose tree an instance keeps was not garbage collected")
		}
		runtime.GC()
	}
}

// TestTreeBudget checks that an instance keeps at most treeBudget of trees
// of Docs that are still in use, and that it measures them.
func TestTreeBudget(t *testing.T) {
	p := &pool{max: 1}
	var docs []*Doc
	for range 60 {
		docs = append(docs, mustParse(t, p, fill("000000", 500)))
	}
	withIdle(t, p, func(in *instance) {
		c := &in.trees
		var sum int
		for e := c.lru.Front(); e != nil; e = e.Next() {
			sum += e.Value.(cachedTree).size
		}
		if sum != c.size || c.size > treeBudget || c.size < treeBudget/2 {
			t.Errorf("instance keeps %d trees of %d bytes, counted as %d; want about treeBudget", c.lru.Len(), sum, c.size)
		}
		if size := len(in.memory()); size > treeBudget+4<<20 {
			t.Errorf("instance has %d bytes of memory; want at most %d", size, treeBudget+4<<20)
		}
		if in.trees.elems[docs[len(docs)-1].key] == nil {
			t.Error("the tree of the most recently parsed Doc was freed")
		}
	})
	runtime.KeepAlive(docs)
}

// TestRenderConcurrent renders many Docs in parallel, with more trees than
// treeBudget holds, while the garbage collector finds dropped Docs. Run it
// with -race.
func TestRenderConcurrent(t *testing.T) {
	p := &pool{max: runtime.GOMAXPROCS(0)}
	type doc struct {
		d      *Doc
		rrggbb string
	}
	var docs []doc
	for i := range 16 {
		rrggbb := fmt.Sprintf("%02x%02x40", 16*i, 255-16*i)
		docs = append(docs, doc{mustParse(t, p, fill(rrggbb, treeBudget/10/400)), rrggbb})
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 40 {
				d := docs[(g*5+i*3)%len(docs)]
				checkFill(t, d.d, d.rrggbb)
				if i%8 == 0 {
					d, err := p.parse(fill("ffffff", 0))
					if err != nil {
						t.Error(err)
						return
					}
					checkFill(t, d, "ffffff")
					runtime.GC()
				}
			}
		})
	}
	wg.Wait()
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
		d, err := Parse(src)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		dst := make([]byte, size*size*4)
		if err := d.Render(size, size, 1, 1, 0, 0, dst); err != nil {
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
