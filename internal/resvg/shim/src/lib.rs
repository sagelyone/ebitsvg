//! A C ABI over resvg for ebitsvg, which runs it as WebAssembly. Pointers
//! are offsets into linear memory, and a return value of 0 means failure.

use std::cell::RefCell;

use resvg::tiny_skia::{PixmapMut, Transform};
use resvg::usvg::{Error, Options, Tree, roxmltree};

thread_local! {
    static LAST_ERROR: RefCell<(u32, String)> = const { RefCell::new((0, String::new())) };
}

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
/// and sets the last error.
///
/// # Safety
///
/// ptr must point to len readable bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn parse(ptr: *const u8, len: u32) -> *mut Tree {
    let data = unsafe { std::slice::from_raw_parts(ptr, len as usize) };
    match Tree::from_data(data, &Options::default()) {
        Ok(tree) => Box::into_raw(Box::new(tree)),
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
/// by the matrix (sx 0 0 sy dx dy). It returns the pixmap's w*h*4 bytes, or
/// 0 if they cannot be allocated.
///
/// # Safety
///
/// tree must be from parse, and not freed yet.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn render(
    tree: *const Tree,
    w: u32,
    h: u32,
    sx: f32,
    sy: f32,
    dx: f32,
    dy: f32,
) -> *mut u8 {
    let tree = unsafe { &*tree };
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
    resvg::render(
        tree,
        Transform::from_row(sx, 0.0, 0.0, sy, dx, dy),
        &mut pixmap,
    );
    Box::into_raw(data.into_boxed_slice()).cast()
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
/// size, 3 for too many elements, 4 for input that is not UTF-8.
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
