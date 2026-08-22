# Algo Glockenspiel Web App

The browser front end: a React 19 + TypeScript app built with Vite, driving the
Go synthesis core through WebAssembly. The instrument view mirrors the
plugin-editor mockup under `plugin/vst3/ui/` and uses the same note geometry,
piano alignment and control layout.

## Layout

```
web/
  index.html          Vite's entry document; loads wasm_exec.js, then the app
  placeholder.html    embedded in the binary, shown when dist is not built
  src/
    App.tsx           tab bar and hash router
    routes/           PlayPage, OptimizePage
    components/       Topbar, PresetStrip, ControlRail, Dial, Rack, Keyboard
    audio/            the WASM load sequence and the AudioContext graph
    lib/              note geometry and the procedural wood texture
    styles/           the stylesheet
  assets/             SVG source assets, inlined or hashed by Vite
  wasm_exec.js        vendored from the Go toolchain; never edit
  dist/              build output, gitignored
```

## Build

```bash
just build-web
```

That runs `scripts/build-web.sh`, which does three things in order:

1. `npm ci && npm run build` in `web/` — the React bundle into `web/dist`,
2. copies `web/wasm_exec.js` into `web/dist` verbatim,
3. `scripts/build-wasm.sh` — `web/dist/glockenspiel.wasm` and
   `web/dist/manifest.json`.

Both halves land in the same directory. Vite runs with `emptyOutDir: false` so
it does not delete the module beside it, and the module is built second, so
neither step erases the other's output whichever one changed.

Everything in `web/dist` is a build artifact: the directory is gitignored and
the page will not run without it. `web/package-lock.json`, on the other hand, is
tracked — `npm ci` is what makes a local build and a CI build the same build.

To build only the module:

```bash
just build-wasm
```

`web/wasm_exec.js` is tracked in git rather than copied from `$GOROOT` on every
build, because a build that rewrites a tracked file dirties the working tree
behind your back. The script compares the two and tells you when they differ;
refresh it deliberately after a Go toolchain upgrade with:

```bash
./scripts/build-wasm.sh --refresh-wasm-exec
```

The web build uses the standard Go toolchain, not TinyGo; see
[../docs/tinygo-evaluation.md](../docs/tinygo-evaluation.md) for the reasoning.

## Develop

```bash
go run ./cmd/glockenspiel serve   # in one terminal, for the fit API
npm --prefix web run dev          # in another, for hot reload
```

`vite.config.ts` proxies `/api` to `http://localhost:8080`, so the dev server
can talk to a running `serve`. The WebAssembly module is read from `web/dist`,
so `just build-web` still has to have run at least once.

Checks, all of which CI runs:

```bash
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run build
```

## Serve

```bash
just build-web
go run ./cmd/glockenspiel serve
```

Or with any static file server, which is also the GitHub Pages case:

```bash
npx serve web/dist
```

Then open `http://localhost:8080`. `vite.config.ts` sets `base: "./"`, so the
same bundle works at a server root and under the project sub-path Pages hands
out. Routing is on the URL fragment — `#/play`, `#/optimize` — because the
server answers an unknown path with a hard 404 by design and neither it nor
Pages has a rewrite rule.

## The WASM Bridge

`cmd/glockenspiel-wasm` publishes exactly one global, `glockenspielWasm`,
carrying `init`, `noteOn`, `setMasterGain` and `processBlock`. It announces
itself by calling `window.__glockenspielWasmReady` — installed by
`src/audio/useWasmEngine.ts` before the Go runtime starts — so the page waits
for an actual signal rather than guessing how long start-up takes.

`processBlock` returns a pointer into Go's linear memory, and
`src/audio/useAudioEngine.ts` reads the samples through a cached
`Float32Array`. That cache is revalidated on every callback: growing Go's heap
detaches the underlying `ArrayBuffer`, and a stale view over a detached buffer
returns `undefined` instead of throwing, which lands in the output buffer as
NaN. See the comment on `interleavedFrames`.

## Usage

- Click or tap bars and keys to strike notes, or Tab to one and press Enter
- Use the printed keyboard bindings for quick play
- Adjust `Velocity` for attack strength
- Adjust `Volume` for overall output gain, including while a note rings
