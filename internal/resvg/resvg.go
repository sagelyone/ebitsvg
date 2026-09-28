// Package resvg renders SVG documents with resvg, compiled to WebAssembly
// and translated to Go by wasm2go in the package shim.
//
// Each call runs in an instance of the module that no other call uses at
// the same time; idle instances are pooled, up to GOMAXPROCS of them.
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

type pool struct {
	idle chan *instance
}

var instances = sync.OnceValue(func() *pool {
	return &pool{make(chan *instance, runtime.GOMAXPROCS(0))}
})

// An instance holds an instance of the module, which is not safe for
// concurrent use. Its methods panic if the module traps, which leaves it
// unusable.
type instance struct {
	mod *shim.Module
}

func (p *pool) acquire() *instance {
	select {
	case in := <-p.idle:
		return in
	default:
		return &instance{shim.New()}
	}
}

// release returns in to the pool, unless it grew too large or the pool is
// full.
func (p *pool) release(in *instance) {
	if len(in.memory()) > pooledLimit {
		return
	}
	select {
	case p.idle <- in:
	default:
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

// parse parses src into a tree, which the caller must free with freeTree.
func (in *instance) parse(src []byte) (tree int32, err error) {
	n := int32(len(src))
	ptr := in.mod.Xalloc(n)
	copy(in.memory()[uint32(ptr):], src)
	tree = in.mod.Xparse(ptr, n)
	in.mod.Xdealloc(ptr, n)
	if tree == 0 {
		return 0, in.parseError()
	}
	return tree, nil
}

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
	}
	ptr, n := uint32(in.mod.Xerror_ptr()), uint32(in.mod.Xerror_len())
	return fmt.Errorf("XML syntax error: %s", in.memory()[ptr:ptr+n])
}

// render renders tree into dst, as Render does.
func (in *instance) render(tree int32, w, h int, sx, sy, dx, dy float32, dst []byte) error {
	ptr := in.mod.Xrender(tree, int32(w), int32(h), sx, sy, dx, dy)
	if ptr == 0 {
		return errors.New("out of memory")
	}
	copy(dst, in.memory()[uint32(ptr):])
	in.mod.Xpixels_free(ptr, int32(w), int32(h))
	return nil
}

// withTree parses src in the instance and calls f with the tree.
func (in *instance) withTree(src []byte, f func(tree int32) error) error {
	tree, err := in.parse(src)
	if err != nil {
		return err
	}
	err = f(tree)
	in.freeTree(tree)
	return err
}

// Parse reports whether resvg can parse the SVG document src.
func Parse(src []byte) error {
	return instances().run(func(in *instance) error {
		return in.withTree(src, func(int32) error { return nil })
	})
}

// Render renders the SVG document src into dst, a w×h image of
// premultiplied RGBA, transformed by the matrix (sx 0 0 sy dx dy) from
// the document's user space. On failure, dst is unchanged.
func Render(src []byte, w, h int, sx, sy, dx, dy float32, dst []byte) error {
	return instances().run(func(in *instance) error {
		return in.withTree(src, func(tree int32) error {
			return in.render(tree, w, h, sx, sy, dx, dy, dst)
		})
	})
}
