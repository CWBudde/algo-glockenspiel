# Algo Glockenspiel Web Demo

Browser demo for the default glockenspiel preset using Go WebAssembly plus a small JavaScript mixer.
The current UI mirrors the plugin-editor mockup under `plugin/vst3/ui/` and uses
the same note geometry, piano alignment, and control layout.

## Build

```bash
./scripts/build-wasm.sh
```

The script writes `web/dist/glockenspiel.wasm` and `web/dist/manifest.json`. Both are build
artifacts: `web/dist` is gitignored, and the page will not run without them.

It builds with `-trimpath -ldflags="-s -w"` and, when [binaryen](https://github.com/WebAssembly/binaryen)
is installed, runs `wasm-opt -O3` as well. `wasm-opt` is optional -- a machine without it
gets a slightly larger module and a note, not a failed build.

`web/wasm_exec.js` is tracked in git rather than copied from `$GOROOT` on every build,
because a build that rewrites a tracked file dirties the working tree behind your back. The
script compares the two and tells you when they differ; refresh it deliberately after a Go
toolchain upgrade with:

```bash
./scripts/build-wasm.sh --refresh-wasm-exec
```

The web build uses the standard Go toolchain, not TinyGo; see
[../docs/tinygo-evaluation.md](../docs/tinygo-evaluation.md) for the reasoning.

## Serve

```bash
go run ./cmd/glockenspiel serve
```

Or with any static file server:

```bash
python3 -m http.server -d web 8080
```

Then open `http://localhost:8080`.

## The WASM Bridge

`cmd/glockenspiel-wasm` publishes exactly one global, `glockenspielWasm`, carrying `init`,
`noteOn`, `setMasterGain` and `processBlock`. It announces itself by calling
`window.__glockenspielWasmReady` -- installed by `main.js` before the Go runtime starts --
so the page waits for an actual signal rather than guessing how long start-up takes.

`processBlock` returns a pointer into Go's linear memory, and `main.js` reads the samples
through a cached `Float32Array`. That cache is revalidated on every callback: growing Go's
heap detaches the underlying `ArrayBuffer`, and a stale view over a detached buffer returns
`undefined` instead of throwing, which lands in the output buffer as NaN. See the comment
on `interleavedFrames` in `main.js`.

## Usage

- Click or tap bars to strike notes
- Use the printed keyboard bindings for quick play
- Adjust `Velocity` for attack strength
- Adjust `Level` for overall output gain
