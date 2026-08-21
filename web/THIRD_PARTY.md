# Third-party files

## `wasm_exec.js`

Copied verbatim from the Go toolchain (`$(go env GOROOT)/lib/wasm/wasm_exec.js`)
by `scripts/build-wasm.sh`. It is part of the Go distribution and is covered by
Go's BSD-3-Clause license, not by this repository's MIT license:
<https://github.com/golang/go/blob/master/LICENSE>.

Do not edit it by hand — it is a toolchain-versioned ABI shim and must match the
Go version that compiled `dist/glockenspiel.wasm`.
