package resvg

import (
	"container/list"
	"weak"
)

// treeBudget bounds the size of the trees that each instance keeps. It
// leaves most of pooledLimit to rendering, so that keeping trees does not
// make instances that render up to about 1024×1024 outgrow the pool and
// lose them.
const treeBudget = 8 << 20

// A Doc is an SVG document that resvg can parse. It is immutable and safe
// for concurrent use.
//
// Each instance that parses a Doc keeps its tree until the instance is
// dropped or trees that the instance used more recently fill treeBudget.
// Keeping it does not keep the Doc from being garbage collected; the tree
// of a collected Doc, which is no longer used, is then among the first
// freed.
type Doc struct {
	p   *pool
	src []byte
	key weak.Pointer[Doc]
}

// Parse parses the SVG document src, which must not be modified afterwards,
// and returns an error if resvg cannot parse it.
func Parse(src []byte) (*Doc, error) {
	return instances().parse(src)
}

func (p *pool) parse(src []byte) (*Doc, error) {
	d := &Doc{p: p, src: src}
	d.key = weak.Make(d)
	err := p.run(func(in *instance) error {
		_, err := in.tree(d)
		return err
	})
	if err != nil {
		return nil, err
	}
	return d, nil
}

// Render renders the document into dst, a w×h image of premultiplied RGBA,
// transformed by the matrix (sx 0 0 sy dx dy) from the document's user
// space. If id is not empty, it renders only the group with that id, as
// transformed in the document, and nothing if there is none. On failure,
// dst is unchanged.
func (d *Doc) Render(id string, w, h int, sx, sy, dx, dy float32, dst []byte) error {
	return d.p.run(func(in *instance) error {
		tree, err := in.tree(d)
		if err != nil {
			return err
		}
		return in.renderTree(tree, id, w, h, sx, sy, dx, dy, dst)
	})
}

// Bounds returns the bounds (x, y, w, h) of the group with the given id,
// in the coordinates that Render transforms: the axis-aligned bounding box
// of the first shape in the group, in document order, that has an area and
// neither fill nor stroke.
func (d *Doc) Bounds(id string) (b [4]float32, err error) {
	err = d.p.run(func(in *instance) error {
		tree, err := in.tree(d)
		if err != nil {
			return err
		}
		b, err = in.bounds(tree, id)
		return err
	})
	return b, err
}

// A treeCache holds an instance's trees, which only the call holding the
// instance may use.
type treeCache struct {
	elems map[weak.Pointer[Doc]]*list.Element
	lru   list.List // of cachedTree, most recently used first
	size  int       // the sum of the trees' sizes
}

type cachedTree struct {
	key  weak.Pointer[Doc]
	tree int32
	size int
}

// tree returns d's tree in the instance, parsing d if the instance does not
// keep its tree.
func (in *instance) tree(d *Doc) (int32, error) {
	c := &in.trees
	if e, ok := c.elems[d.key]; ok {
		c.lru.MoveToFront(e)
		return e.Value.(cachedTree).tree, nil
	}
	tree, size, err := in.parseTree(d.src)
	if err != nil {
		return 0, err
	}
	if c.elems == nil {
		c.elems = make(map[weak.Pointer[Doc]]*list.Element)
	}
	c.elems[d.key] = c.lru.PushFront(cachedTree{d.key, tree, size})
	c.size += size
	for c.size > treeBudget && c.lru.Len() > 1 {
		in.evict(c.lru.Back())
	}
	return tree, nil
}

func (in *instance) evict(e *list.Element) {
	c := &in.trees
	t := c.lru.Remove(e).(cachedTree)
	delete(c.elems, t.key)
	c.size -= t.size
	in.freeTree(t.tree)
}
