# The web app

`web/` is the user-facing product: a React 19 + TypeScript app, built by Vite,
driving the Go synthesis core compiled to WebAssembly. It has two tabs. **Play**
strikes bars and hears them. **Optimize** fits the model against a reference
recording, which needs the Go binary and therefore only works where one is
running.

This page is about how the app is put together and why. For the flags and routes
of the server that hosts it, see [serve.md](serve.md); for building and
developing it, [../web/README.md](../web/README.md).

## The shape of it

```
web/
  index.html          Vite's entry document; loads wasm_exec.js, then the app
  placeholder.html    embedded in the binary, shown when dist is not built
  src/
    App.tsx           the tab bar, the hash router, and the audio engine
    routes/           PlayPage, OptimizePage
    components/       Topbar, PresetStrip, ControlRail, Dial, Rack, Keyboard
    audio/            the WASM load sequence and the AudioContext graph
    features/optimize/  the fit form, the event stream, the chart, the audition
    api/              the typed fit-API client and the wire types
    lib/              note geometry, the procedural wood texture
    styles/           the stylesheet
  dist/               build output, gitignored
```

Two decisions account for most of the structure.

**The audio engine lives in `App.tsx`, not in `PlayPage`.** Switching to
Optimize unmounts the play surface, and neither the Go runtime nor a ringing
note should die because the user looked at another tab. `useWasmEngine` and
`useAudioEngine` are therefore mounted above the router and passed down.

**The Optimize tab is a stack of small pieces around one job.** `OptimizePage`
owns exactly one thing — the snapshot of the fit currently being watched — and
hands it to `FitForm` (start and cancel) and to `FitProgress` (the stream, the
curve, the numbers, the audition). Every snapshot, wherever it arrives from, is
one whole status object; the server never sends a delta, so there is no
reconciliation to write and no second source of truth to keep in step.

Until this phase the front end was 40 KB of hand-written ES modules with no
build step. TypeScript was the reason to change that, more than React: the fit
API's wire shapes are eleven Go structs, and `internal/server/fit_test.go`
already re-declares `fitSnapshot` locally on purpose so that a field renamed in
the server is a failing test rather than a silently renamed wire field.
`src/api/types.ts` is the same guard on the browser side, transcribed by hand
from the Go structs rather than generated, and it lists every field the server
sends including the ones no component reads yet.

## The WASM bridge

`cmd/glockenspiel-wasm` publishes exactly one global, `glockenspielWasm`,
carrying `init`, `noteOn`, `setMasterGain` and `processBlock`. One namespace
rather than five bare `wasm*` globals, so what belongs to this module is
visible at a glance.

### The ready handshake

The module announces itself by calling `window.__glockenspielWasmReady`, which
`src/audio/useWasmEngine.ts` installs **before** the Go runtime starts. It has to
be installed first, because Go calls it from inside `go.run(...)`: the module's
main runs synchronously up to the point where it blocks, so there is no later
moment at which registering the hook would still be in time.

What this replaced was a `setTimeout(resolve, 50)` after `go.run(...)` followed
by a `typeof` check — a guess about how long a machine needs to get through the
Go runtime's start-up, and wrong in both directions: too short on a loaded CI box
or a cold cache, where the page reported "WASM exports not found" for a module
that was seconds from being ready, and 50 ms of dead time on every load where it
was not. A 10 s timer bounds the wait, so a module that never signals produces a
message rather than a spinner.

The module is fetched at its fixed name, `glockenspiel.wasm`, with the content
hash `scripts/build-wasm.sh` records in `manifest.json` appended as `?v=<hash>`.
The name is fixed because `internal/server` hard-codes it to recognise a missing
build; the fingerprint travels in the query string instead. A missing manifest is
not fatal — the module is then fetched unfingerprinted and revalidated normally.

### The detached-`ArrayBuffer` hazard

`processBlock` returns a pointer into Go's linear memory, and
`src/audio/useAudioEngine.ts` reads the samples through a cached `Float32Array`.
The cache exists because a fresh view per callback is one allocation every
~11.6 ms at 512 frames and 44.1 kHz, on the one thread that must not pause for a
GC. But a hoisted view is exactly where this goes wrong, so `interleavedFrames`
revalidates it three ways on every block.

A `WebAssembly.Memory` grows when Go's heap grows, and growing **detaches** the
old `ArrayBuffer`. A stale view over a detached buffer does not throw. Measured
in Chrome after `memory.grow(1)`: the old buffer reports `byteLength` 0,
`memory.buffer` is a different object, the stale view's length drops to 0, and
indexing it returns `undefined` — which becomes NaN the moment it is written into
the output buffer. So the symptom is not an exception at the point of the mistake
but a channel of NaN, starting at whatever unrelated moment the heap happened to
grow: typically minutes in, once, and never while a debugger is attached.

The three checks are buffer identity (a grow hands back a new `ArrayBuffer`
object), `byteLength === 0` (how a detached buffer reports itself), and the
pointer plus length (Go is free to move or resize the allocation between calls,
so a stable buffer does not imply a stable region). When no view can be built the
block is skipped and the output is left silent — one buffer of silence rather
than an exception thrown out of `onaudioprocess`.

The graph itself is still a `ScriptProcessorNode`, created lazily on the first
strike because a browser will not start an `AudioContext` without a user gesture.
Moving it to an `AudioWorklet` is Phase 5.2 and is not done.

## The two-step build

```bash
just build-web
```

runs `scripts/build-web.sh`, which does the React bundle first
(`npm ci && npm run build`) and `scripts/build-wasm.sh` second. Both write into
`web/dist`. Vite runs with `emptyOutDir: false` so it does not delete the module
beside it, and the module is built second, so neither step erases the other's
output whichever one changed.

They are two steps because they are two toolchains, and only one of them is
required to build the Go binary. `go build ./...` on a machine with no Node
installed still has to work — every CI job depends on it — and a checkout has no
`web/dist` at all, because everything in it is a build artifact and the directory
is gitignored.

Which is also why the served root is the dist tree rather than an embedded copy
of the app. `web/embed.go` lists tracked files one by one, precisely so a
gitignored directory can never leak into the binary; and `go:embed` reads the
working tree rather than git, so embedding a build artifact would produce a
binary whose contents depend on whether someone happened to run a build step
first. The embedded tree is therefore **one placeholder page**, which says the
app has not been built and names `just build-web`. The server answers `/` with
it, under a `503`, when `web/dist/index.html` is missing. Honest, small, and
free of the question "which build is in this binary".

## Routing

Routing is on the URL fragment: `#/play` and `#/optimize`. There is one real URL.

`handleStatic` maps only `/` to `index.html` and answers every unknown path with
a hard `404`, deliberately: a silent fallback to `index.html` would turn a
mistyped asset path into a page that loads and then misbehaves. Path-based
routing would need that fallback. GitHub Pages has no rewrite rule either.
A fragment costs neither, and it survives both hosts unchanged.

The same reasoning drives `base: "./"` in `vite.config.ts`. Relative asset URLs
mean one bundle works at `http://localhost:8080/` and under the project sub-path
Pages hands out (`https://cwbudde.github.io/algo-glockenspiel/`); an absolute
base would work at exactly one of the two. Every fetch the app makes — the
manifest, the module, `api/fit/events` — is relative for the same reason.

## The Optimize loop

The tab is a client of the fit API described in [serve.md](serve.md). One fit at
a time, held by the server, so the page's job is to start it, watch it, and read
the result.

1. **Probe.** `useApiAvailable` fetches `GET api/version` once on mount. Where
   there is no server this 404s — there is no `/api/` catch-all, so it falls
   through to the static 404 — and the tab renders an explanation instead of a
   form.
2. **Start.** `FitForm` posts a multipart body to `POST api/fit/start`: the
   reference WAV, an optional starting preset, optional bounds, and every scalar.
   Each scalar is held client-side to the range `internal/server/params.go` holds
   it to, because a `400` that arrives after a 16 MiB upload is a slow way to
   learn that `note` was 200. The `409` — "a fit is already running" — is
   surfaced with those words rather than as a generic failure.
3. **Watch.** `useFitEvents` opens an `EventSource` on `api/fit/events` for the
   job's lifetime. The stream opens with the current snapshot, so attaching
   mid-run, or after the run already ended, paints immediately.
4. **Read.** `CostChart` plots best and current cost against the optimizer's own
   iteration count; `FitStatus` shows the state, the counts, the elapsed time and
   the stop reason; `Audition` renders the fitted preset through
   `api/fit/audio` and links to `api/fit/preset` for the download.

Four details of that loop are load-bearing enough to write down.

**There is no polling.** Not on the progress path — the stream carries it — and
not after a cancel either: `POST api/fit/cancel` waits for the run to actually
stop before it answers, so its `200` already means the slot is free. The one
non-stream read is a single `GET api/fit` on mount, because a fit outlives the
page and a reload should land back on whatever is running with the Cancel button
reachable, rather than stranding the slot.

**The stream must be closed on a terminal event.** It carries no `id:` and no
`retry:`, so there is no Last-Event-ID replay to lean on. A source left open past
`done` is reconnected by the browser, handed the same terminal snapshot, and
reconnected again, forever. `close()` on `done` and on `shutdown` is not
tidiness; it is the only thing that ends the loop.

**`hasPreset` is not `state === "succeeded"`.** A run cancelled after its first
report still has the best parameters found so far, and the server answers
`api/fit/preset` and `api/fit/audio` for it. The audition gates on `hasPreset`
for that reason: someone who watched a curve flatten out and pressed Cancel
wanted the result, not an empty panel.

**Two iteration counts, two rows.** `iteration` counts progress _reports_ and
moves once per `reportEvery`; `optimizerIterations` is the backend's own count
and the only one comparable with the request's `maxIterations`. They are separate
fields in the wire format for a reason and are displayed separately for the same
one. `maxIterations` itself is not in the snapshot — the server does not echo the
request back — so the form passes what it sent alongside the start snapshot.

### What the cost chart has and has not been shown to do

The y axis is logarithmic, which cannot plot a zero or a negative. Both occur:
the snapshot returned by `POST api/fit/start` reports `bestCost: 0` on every run,
before any candidate has been evaluated. Such a sample is dropped rather than
clamped to an invented epsilon that would draw a floor which is not in the data.

The curve has been watched filling live from the stream, at `reportEvery: 1`,
with the coalescing the server documents visible in the gap between the sample
count and the iteration count. It has **not** been watched falling against a real
fit. Fitting `testdata/reference/legacy_synth_a4.wav` from the shipped default
preset reports a flat cost from iteration 1 for both optimizers, which is
expected rather than broken: Phase 5.1 re-fitted that very preset against that
very reference, so a run starting there starts at its own optimum. It makes that
fixture a poor demo, and the falling-curve case was exercised with a synthetic
series instead.

## Why Pages cannot fit

The hosted build is the same bundle, served by GitHub Pages, with no Go process
behind it. Two things follow.

Play works completely. It is the WebAssembly module, an `AudioContext` and static
files, and Pages serves all three.

Optimize cannot run at all, and says so. Fitting is CPU-bound work the Go binary
does — the objective renders and scores the whole reference once per candidate
evaluation — and there is no binary behind a static host. The tab detects this
from the `api/version` probe and renders the command that makes the API
reachable, plus the CLI equivalent for someone who does not want a browser in the
loop at all. It does not render a form that would fail on submit.

Running the optimizer itself in WASM is deferred, not scheduled. It is listed
under `## Deferred` in `PLAN.md`. The reason is not that it cannot be compiled:
it is that a fit is minutes of full-core work, the browser build has one thread
and no SIMD kernels, and a "fit" that is an order of magnitude slower than the
CLI's would be a worse answer than the sentence telling you to run the CLI.
