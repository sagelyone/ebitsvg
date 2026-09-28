// Package resvg renders SVG documents with resvg, compiled to WebAssembly
// and translated to Go by wasm2go in the package shim.
//
// Each call runs in an instance of the module that no other call uses at
// the same time; idle instances are pooled, up to GOMAXPROCS of them. Each
// instance keeps the trees of the documents it parsed, so that rendering a
// [Doc] again in the same instance skips parsing it.
package resvg

import (
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/sagelyone/ebitsvg/internal/resvg/internal/shim"
)

// pooledLimit is the largest instance kept for reuse. Linear memory never
// shrinks, so larger ones are dropped to give their memory back.
const pooledLimit = 32 << 20

// A pool holds up to max idle instances. It hands out the one released
// last, so that a goroutine that renders documents one after another keeps
// using the instance that holds their trees.
type pool struct {
	mu   sync.Mutex
	idle []*instance
	max  int
}

var instances = sync.OnceValue(func() *pool {
	return &pool{max: runtime.GOMAXPROCS(0)}
})

// An instance holds an instance of the module, which is not safe for
// concurrent use. Its methods panic if the module traps, which leaves it
// unusable.
type instance struct {
	mod   *shim.Module
	trees treeCache
}

func (p *pool) acquire() *instance {
	p.mu.Lock()
	if n := len(p.idle); n > 0 {
		in := p.idle[n-1]
		p.idle[n-1] = nil
		p.idle = p.idle[:n-1]
		p.mu.Unlock()
		return in
	}
	p.mu.Unlock()
	return &instance{mod: shim.New()}
}

// release returns in to the pool, unless it grew too large or the pool is
// full. Dropping an instance frees its trees with it.
func (p *pool) release(in *instance) {
	if len(in.memory()) > pooledLimit {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.idle) < p.max {
		p.idle = append(p.idle, in)
	}
}

// A trapError reports that the module trapped.
type trapError struct{ v any }

func (e trapError) Error() string { return fmt.Sprintf("renderer failed: %v", e.v) }

// run calls f with an instance that no other call is using. If the module
// traps, run returns a trapError and drops the instance.
func (p *pool) run(f func(*instance) error) (err error) {
	in := p.acquire()
	defer func() {
		if v := recover(); v != nil {
			err = trapError{v}
			return
		}
		p.release(in)
	}()
	return f(in)
}

// memory returns the instance's linear memory, which growing it replaces.
func (in *instance) memory() []byte {
	return *in.mod.Xmemory().Slice()
}

// parseTree parses src into a tree in the instance, which stays valid until
// freeTree frees it, and returns the tree and the bytes it holds.
func (in *instance) parseTree(src []byte) (tree int32, size int, err error) {
	n := int32(len(src))
	ptr := in.mod.Xalloc(n)
	copy(in.memory()[uint32(ptr):], src)
	tree = in.mod.Xparse(ptr, n)
	in.mod.Xdealloc(ptr, n)
	if tree == 0 {
		return 0, 0, in.parseError()
	}
	return tree, int(uint32(in.mod.Xtree_size())), nil
}

// freeTree frees a tree that parseTree returned.
func (in *instance) freeTree(tree int32) {
	in.mod.Xtree_free(tree)
}

func (in *instance) parseError() error {
	switch in.mod.Xerror_code() {
	case 2:
		return errors.New("invalid size")
	case 3:
		return errors.New("too many elements")
	case 4:
		return errors.New("input must be UTF-8 or ASCII")
	case 5:
		return errors.New("elements nested too deeply")
	}
	ptr, n := uint32(in.mod.Xerror_ptr()), uint32(in.mod.Xerror_len())
	return fmt.Errorf("XML syntax error: %s", in.memory()[ptr:ptr+n])
}

// renderTree renders a tree that parseTree returned into dst, as
// [Doc.Render] describes.
func (in *instance) renderTree(tree int32, w, h int, sx, sy, dx, dy float32, dst []byte) error {
	ptr := in.mod.Xrender(tree, int32(w), int32(h), sx, sy, dx, dy)
	if ptr == 0 {
		return errors.New("out of memory")
	}
	copy(dst, in.memory()[uint32(ptr):])
	in.mod.Xpixels_free(ptr, int32(w), int32(h))
	return nil
}
