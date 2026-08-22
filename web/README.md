# Algo Glockenspiel Web App

The browser front end: a React 19 + TypeScript app built with Vite, driving the
Go synthesis core through WebAssembly. The instrument view mirrors the
plugin-editor mockup in
[algo-glockenspiel-vst3](https://github.com/CWBudde/algo-glockenspiel-vst3)
(`vst3/ui/`) and uses the same note geometry, piano alignment and control
layout. [../docs/web-app.md](../docs/web-app.md)
describes the architecture and the reasoning behind it; this file is about
building, running and using it.

## Layout

```
web/
  index.html          Vite's entry document
  placeholder.html    embedded in the binary, shown when dist is not built
  src/
    App.tsx           tab bar, hash router and the audio engine
    routes/           PlayPage, OptimizePage
    components/       Topbar, PresetStrip, ControlRail, Dial, Rack, Keyboard
    audio/            the engine worker, the worklet, and the AudioContext graph
    api/              the typed fit-API client and the wire types
    features/optimize/  the fit form, the event stream, the chart, the audition
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
2. `scripts/build-wasm.sh` — `web/dist/glockenspiel.wasm` and
   `web/dist/manifest.json`,
3. copies `web/wasm_exec.js` into `web/dist` verbatim.

The copy comes last because `--refresh-wasm-exec` is handled inside
`build-wasm.sh`: it rewrites `web/wasm_exec.js` from the toolchain in use, and a
copy taken before that would leave the pre-upgrade shim beside a module built by
the new toolchain.

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
can talk to a running `serve`. It also maps `/glockenspiel.wasm` and
`/manifest.json` onto `web/dist`, because those two are build output rather than
sources and the dev server would otherwise answer 404 for them; `just build-web`
still has to have run at least once.

Checks, all of which CI runs:

```bash
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web run test
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

## Optimize

The **Optimize** tab fits the model against a reference recording, using the fit
API of the `glockenspiel serve` process that is hosting the page. It needs that
process: the tab probes `GET api/version` on mount and, where nothing answers,
renders the command that makes the API reachable instead of a form that would
fail on submit. That is the GitHub Pages case, and it is the only difference
between the hosted build and a local one.

The loop:

1. Choose a reference WAV — up to 16 MiB, mono or the first channel of a
   multi-channel file — and optionally a starting preset and narrowed bounds.
2. Choose the metric (`rms`, `log`, `spectral`), the optimizer (`simple`,
   `mayfly`) and the scalars. The defaults are the `fit` command's own, so a
   preset fitted from the browser and one fitted from the terminal are the same
   fit. Every field is held to the server's range before anything is uploaded.
3. **Start fit.** The server runs one fit at a time; a second start is refused
   with "a fit is already running", which the form shows in those words.
4. The cost curve, the counters and the stop reason fill live from
   `api/fit/events`, a Server-Sent Events stream carrying a whole status object
   per report. Nothing polls.
5. **Cancel fit** stops it. A cancelled run keeps the best parameters it found,
   so the audition and the download stay available.
6. **Render and play** auditions the fitted preset at the note and velocity the
   fit ran against; **Download preset JSON** saves it in the schema
   `glockenspiel synth --preset` loads.

The wire types live in `src/api/types.ts`, transcribed by hand from the Go
structs that produce them. Renaming a field in the server is meant to become a
type error here.

See [../docs/serve.md](../docs/serve.md) for the API itself and
[../docs/web-app.md](../docs/web-app.md) for how the tab is put together.

## The WASM Bridge

`cmd/glockenspiel-wasm` publishes exactly one global, `glockenspielWasm`,
carrying `init`, `noteOn`, `setMasterGain` and `processBlock`. It announces
itself by calling `__glockenspielWasmReady` — installed by
`src/audio/engine.worker.ts` before the Go runtime starts — so the page waits
for an actual signal rather than guessing how long start-up takes.

The module runs in a Web Worker, not on the page: an `AudioWorkletNode` drains
the blocks the worker renders, over a channel the two hold directly. Pass
`?audio=scriptprocessor` to force the `ScriptProcessorNode` fallback that
browsers without `AudioWorklet` get. See
[../docs/audio-transport.md](../docs/audio-transport.md).

`processBlock` returns a pointer into Go's linear memory, and
`src/audio/engine.worker.ts` reads the samples through a cached `Float32Array`
before copying them into the buffer it sends on. That cache is revalidated on
every block: growing Go's heap detaches the underlying `ArrayBuffer`, and a
stale view over a detached buffer returns `undefined` instead of throwing, which
lands in the output buffer as NaN. See the comment on `interleavedFrames`.

## Usage

- Click or tap bars and keys to strike notes, or Tab to one and press Enter
- Use the printed keyboard bindings for quick play. Note a carried-over bug: the
  hints printed on the natural bars are indexed by position among the naturals
  while the key map uses the semitone offset, so from D4 upwards a bar names a
  key that strikes a different note. The keyboard itself is right; the label is
  not. See the comment in `src/lib/layout.ts`
- Adjust `Velocity` for attack strength
- Adjust `Volume` for overall output gain, including while a note rings
- The status line reports the sample rate, and the number of dropouts once
  there has been one
- Switch to **Optimize** to fit a preset against a recording; see above
