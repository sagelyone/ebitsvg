#!/bin/sh
# Builds shim to WebAssembly with the Rust toolchain that
# shim/rust-toolchain.toml pins, optimizes it with binaryen's wasm-opt,
# translates it to the Go package internal/shim with wasm2go, and writes
# THIRD_PARTY_NOTICES with cargo-about. Paths are remapped so that the
# output does not depend on where the sources are.
#
# wasm2go supports no SIMD, so none is enabled. The module declares a
# maximum of 256 MiB of linear memory, which holds a 4096×4096 pixmap with
# room for filters, and which wasm2go enforces when the memory grows.
# wasm2go's -unsafe, with which memory accesses stay bounds-checked, makes
# rendering about twice as fast.
set -eu
cd "$(dirname "$0")/shim"
export RUSTFLAGS="-C link-arg=--max-memory=268435456 --remap-path-prefix=$PWD=/shim --remap-path-prefix=${CARGO_HOME:-$HOME/.cargo}=/cargo"
cargo build --release --locked --target wasm32-unknown-unknown
out=target/wasm32-unknown-unknown/release
wasm-opt -O3 --enable-bulk-memory --enable-nontrapping-float-to-int \
	--enable-sign-ext --enable-mutable-globals \
	"$out/resvg_shim.wasm" -o "$out/resvg_shim.opt.wasm"
go run github.com/ncruces/wasm2go@v0.4.15 -unsafe -pkg shim -o ../internal/shim/shim.go "$out/resvg_shim.opt.wasm"
cargo about generate --workspace -o ../THIRD_PARTY_NOTICES about.hbs
