//! A C ABI over resvg for ebitsvg, which runs it as WebAssembly. Pointers
//! are offsets into linear memory, and a return value of 0 means failure.

use std::alloc::{GlobalAlloc, Layout, System};
use std::cell::RefCell;
use std::sync::atomic::{AtomicUsize, Ordering::Relaxed};

use resvg::tiny_skia::{NonZeroRect, PixmapMut, Transform};
use resvg::usvg::{Error, Group, Node, Options, Tree, roxmltree};

thread_local! {
    static LAST_ERROR: RefCell<(u32, String)> = const { RefCell::new((0, String::new())) };
}

/// Counts the bytes allocated, so that parse can measure the trees it
/// returns.
struct Counting;

static ALLOCATED: AtomicUsize = AtomicUsize::new(0);
static TREE_SIZE: AtomicUsize = AtomicUsize::new(0);

unsafe impl GlobalAlloc for Counting {
    unsafe fn alloc(&self, layout: Layout) -> *mut u8 {
        let ptr = unsafe { System.alloc(layout) };
        if !ptr.is_null() {
            ALLOCATED.fetch_add(layout.size(), Relaxed);
        }
        ptr
    }

    unsafe fn alloc_zeroed(&self, layout: Layout) -> *mut u8 {
        let ptr = unsafe { System.alloc_zeroed(layout) };
        if !ptr.is_null() {
            ALLOCATED.fetch_add(layout.size(), Relaxed);
        }
        ptr
    }

    unsafe fn dealloc(&self, ptr: *mut u8, layout: Layout) {
        unsafe { System.dealloc(ptr, layout) };
        ALLOCATED.fetch_sub(layout.size(), Relaxed);
    }

    unsafe fn realloc(&self, ptr: *mut u8, layout: Layout, new_size: usize) -> *mut u8 {
        let new = unsafe { System.realloc(ptr, layout, new_size) };
        if !new.is_null() {
            ALLOCATED.fetch_add(new_size, Relaxed);
            ALLOCATED.fetch_sub(layout.size(), Relaxed);
        }
        new
    }
}

#[global_allocator]
static GLOBAL: Counting = Counting;

/// Allocates len bytes for the host to write into.
#[unsafe(no_mangle)]
pub extern "C" fn alloc(len: u32) -> *mut u8 {
    let mut buf = Vec::<u8>::with_capacity(len as usize);
    let ptr = buf.as_mut_ptr();
    std::mem::forget(buf);
    ptr
}

/// Frees a buffer that alloc returned.
///
/// # Safety
///
/// ptr and len must be from one call to alloc.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn dealloc(ptr: *mut u8, len: u32) {
    drop(unsafe { Vec::from_raw_parts(ptr, 0, len as usize) });
}

/// Parses the SVG document in len bytes at ptr into a tree, or returns 0
/// and sets the last error. tree_size then returns the size of the tree.
///
/// # Safety
///
/// ptr must point to len readable bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn parse(ptr: *const u8, len: u32) -> *mut Tree {
    let data = unsafe { std::slice::from_raw_parts(ptr, len as usize) };
    if nesting(data) > MAX_DEPTH {
        LAST_ERROR.with_borrow_mut(|e| *e = (5, String::new()));
        return std::ptr::null_mut();
    }
    let before = ALLOCATED.load(Relaxed);
    match Tree::from_data(data, &Options::default()) {
        Ok(tree) => {
            let tree = Box::into_raw(Box::new(tree));
            TREE_SIZE.store(ALLOCATED.load(Relaxed) - before, Relaxed);
            tree
        }
        Err(err) => {
            let (code, msg) = match err {
                // usvg reports <use> expanding too deeply or to too many
                // nodes as this XML error.
                Error::ElementsLimitReached
                | Error::ParsingFailed(roxmltree::Error::NodesLimitReached) => (3, String::new()),
                Error::ParsingFailed(e) => (1, e.to_string()),
                Error::InvalidSize => (2, String::new()),
                Error::NotAnUtf8Str => (4, String::new()),
                // Unreachable without the svgz feature.
                Error::SvgzFeatureNotEnabled | Error::MalformedGZip => (1, err.to_string()),
            };
            LAST_ERROR.with_borrow_mut(|e| *e = (code, msg));
            std::ptr::null_mut()
        }
    }
}

/// The deepest nesting of elements that parse accepts. roxmltree and resvg
/// recurse on nesting, and deeper documents could overflow the stack, which
/// traps. parse checks the bound that nesting returns against it.
const MAX_DEPTH: usize = 256;

/// Returns at least the depth to which roxmltree nests the elements of the
/// XML document s, without recursing as roxmltree does. References expand
/// entities, whose values may hold elements, up to 10 deep, so each level
/// of the values counts 10 times. Where s is not well-formed, the result
/// bounds only the part before the error.
fn nesting(s: &[u8]) -> usize {
    let find = |from: usize, pat: &[u8]| {
        s[from..]
            .windows(pat.len())
            .position(|w| w == pat)
            .map_or(s.len(), |n| from + n)
    };
    let (mut depth, mut max, mut entities) = (0usize, 0, 0);
    let mut i = 0;
    while i < s.len() {
        if s[i] != b'<' {
            i += 1;
            continue;
        }
        let rest = &s[i..];
        if rest.starts_with(b"<!--") {
            i = find(i + 4, b"-->") + 3;
        } else if rest.starts_with(b"<![CDATA[") {
            i = find(i + 9, b"]]>") + 3;
        } else if rest.starts_with(b"<?") {
            i = find(i + 2, b"?>") + 2;
        } else if rest.starts_with(b"</") {
            depth = depth.saturating_sub(1);
            i += 2;
        } else if rest.starts_with(b"<!DOCTYPE") {
            // Skips the DTD, counting the elements in its literals, which
            // cannot hold their own quote, so this recurses at most twice.
            i += 9;
            let mut subset = false;
            while i < s.len() {
                match s[i] {
                    b'>' if !subset => break,
                    b'[' => subset = true,
                    b']' => break,
                    q @ (b'"' | b'\'') => {
                        let end = s[i + 1..]
                            .iter()
                            .position(|&c| c == q)
                            .map_or(s.len(), |n| i + 1 + n);
                        entities += nesting(&s[i + 1..end]);
                        i = end;
                    }
                    b'<' if s[i..].starts_with(b"<!--") => i = find(i + 4, b"-->") + 2,
                    b'<' if s[i..].starts_with(b"<?") => i = find(i + 2, b"?>") + 1,
                    _ => {}
                }
                i += 1;
            }
        } else {
            // A start tag, whose quoted attribute values may hold '>'.
            let mut quote = None;
            let mut j = i + 1;
            while j < s.len() {
                match (quote, s[j]) {
                    (None, b'>') => break,
                    (None, q @ (b'"' | b'\'')) => quote = Some(q),
                    (Some(q), c) if c == q => quote = None,
                    _ => {}
                }
                j += 1;
            }
            if s[j - 1] != b'/' {
                depth += 1;
                max = max.max(depth);
            }
            i = j + 1;
        }
    }
    max + 10 * entities
}

/// Returns the bytes that the tree parse last returned holds.
#[unsafe(no_mangle)]
pub extern "C" fn tree_size() -> u32 {
    TREE_SIZE.load(Relaxed) as u32
}

/// Frees a tree that parse returned.
///
/// # Safety
///
/// tree must be from parse, and not freed yet.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn tree_free(tree: *mut Tree) {
    drop(unsafe { Box::from_raw(tree) });
}

/// Renders a tree into a new w×h pixmap of premultiplied RGBA, transformed
/// by the matrix (sx 0 0 sy dx dy). If the id in id_len bytes at id_ptr is
/// not empty, it renders only the group with that id, as transformed in the
/// tree, and nothing if there is none. It returns the pixmap's w*h*4 bytes,
/// or 0 if they cannot be allocated.
///
/// # Safety
///
/// tree must be from parse, and not freed yet, and id_ptr must point to
/// id_len readable bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn render(
    tree: *const Tree,
    id_ptr: *const u8,
    id_len: u32,
    w: u32,
    h: u32,
    sx: f32,
    sy: f32,
    dx: f32,
    dy: f32,
) -> *mut u8 {
    let tree = unsafe { &*tree };
    let id = unsafe { std::slice::from_raw_parts(id_ptr, id_len as usize) };
    let Some(len) = (w as usize)
        .checked_mul(h as usize)
        .and_then(|n| n.checked_mul(4))
    else {
        return std::ptr::null_mut();
    };
    let mut data = Vec::new();
    if data.try_reserve_exact(len).is_err() {
        return std::ptr::null_mut();
    }
    data.resize(len, 0);
    let Some(mut pixmap) = PixmapMut::from_bytes(&mut data, w, h) else {
        return std::ptr::null_mut();
    };
    let transform = Transform::from_row(sx, 0.0, 0.0, sy, dx, dy);
    if id.is_empty() {
        resvg::render(tree, transform, &mut pixmap);
    } else if let Some((node, parent)) = find_group(tree.root(), id)
        && let Some(layer) = node.abs_layer_bounding_box()
    {
        // render_node applies the node's own transform but not its
        // ancestors', and shifts the node by minus its layer's position.
        let transform = transform
            .pre_concat(parent.abs_transform())
            .pre_translate(layer.x(), layer.y());
        resvg::render_node(node, transform, &mut pixmap);
    }
    Box::into_raw(data.into_boxed_slice()).cast()
}

/// Writes to out the bounds (x, y, width, height), in canvas coordinates,
/// of the group in tree with the id in id_len bytes at id_ptr: those of the
/// first path in the group, in document order, that has an area and
/// neither fill nor stroke. It returns 0, or 1 if there is no group with
/// that id, or 2 if the group has no such path.
///
/// # Safety
///
/// tree must be from parse, and not freed yet, id_ptr must point to id_len
/// readable bytes, and out to 16 writable bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn bounds(
    tree: *const Tree,
    id_ptr: *const u8,
    id_len: u32,
    out: *mut [f32; 4],
) -> u32 {
    let tree = unsafe { &*tree };
    let id = unsafe { std::slice::from_raw_parts(id_ptr, id_len as usize) };
    if id.is_empty() {
        return 1;
    }
    let Some((Node::Group(group), _)) = find_group(tree.root(), id) else {
        return 1;
    };
    let Some(r) = invisible_bounds(group) else {
        return 2;
    };
    unsafe { out.write_unaligned([r.x(), r.y(), r.width(), r.height()]) };
    0
}

/// Returns the first group within parent, in document order, with the id,
/// and the group's parent.
fn find_group<'a>(parent: &'a Group, id: &[u8]) -> Option<(&'a Node, &'a Group)> {
    parent.children().iter().find_map(|node| match node {
        Node::Group(g) if g.id().as_bytes() == id => Some((node, parent)),
        Node::Group(g) => find_group(g, id),
        _ => None,
    })
}

/// Returns the canvas bounding box of the first path within group, in
/// document order, that has an area and neither fill nor stroke.
fn invisible_bounds(group: &Group) -> Option<NonZeroRect> {
    group.children().iter().find_map(|node| match node {
        Node::Group(g) => invisible_bounds(g),
        Node::Path(p) if p.fill().is_none() && p.stroke().is_none() => {
            p.abs_bounding_box().to_non_zero_rect()
        }
        _ => None,
    })
}

/// Frees the pixels of a w×h pixmap that render returned.
///
/// # Safety
///
/// ptr, w and h must be from one call to render.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn pixels_free(ptr: *mut u8, w: u32, h: u32) {
    let len = w as usize * h as usize * 4;
    drop(unsafe { Box::from_raw(std::ptr::slice_from_raw_parts_mut(ptr, len)) });
}

/// Returns the kind of the last parse error: 1 for XML, 2 for an invalid
/// size, 3 for too many elements, 4 for input that is not UTF-8, 5 for
/// elements nested too deeply.
#[unsafe(no_mangle)]
pub extern "C" fn error_code() -> u32 {
    LAST_ERROR.with_borrow(|e| e.0)
}

/// Returns the message of the last XML error.
#[unsafe(no_mangle)]
pub extern "C" fn error_ptr() -> *const u8 {
    LAST_ERROR.with_borrow(|e| e.1.as_ptr())
}

/// Returns the length of the message at error_ptr.
#[unsafe(no_mangle)]
pub extern "C" fn error_len() -> u32 {
    LAST_ERROR.with_borrow(|e| e.1.len() as u32)
}
