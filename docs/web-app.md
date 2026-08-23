# The web app

`web/` is the user-facing product: a React 19 + TypeScript app, built by Vite,
driving the Go synthesis core compiled to WebAssembly. It has two tabs. **Play**
strikes bars and hears them. **Optimize** fits the model against a reference
recording. It prefers the native fit service when one is present and otherwise
runs the same fitting stack locally in WebAssembly.

This page is about how the app is put together and why. For the flags and routes
of the server that hosts it, see [serve.md](serve.md); for building and
developing it, [../web/README.md](../web/README.md).

## The shape of it

```
web/
  index.html          Vite's entry document
  placeholder.html    embedded in the binary, shown when dist is not built
  src/
    App.tsx           the tab bar, the hash router, and the audio engine
    routes/           PlayPage, OptimizePage
    components/       Topbar, ControlDeck, Dial, StatusPanel, Playfield, Rack, Keyboard
    audio/            the engine worker, the worklet, and the AudioContext graph
    features/optimize/  the fit form, the event stream, the chart, the audition
    api/              the typed fit-API client and the wire types
    lib/              note geometry, wood metadata and shared species presets
    styles/           the stylesheet
  dist/               build output, gitignored
```

The instrument's wood is still procedural, but no longer generated on the main
thread. `web/scripts/generate-wood-textures.mjs` samples growth rings, pores,
rays and figured ripple into deterministic 1024x576 PNGs during development.
The four tracked assets are imported by the browser like any other image;
`npm run wood:check` keeps them synchronized with their shared JSON presets and
runs automatically before the normal Vite build. It compares inflated PNG
scanlines so equivalent zlib output from different supported Node releases does
not create false stale-asset failures.

Two decisions account for most of the structure.

**The audio engine lives in `App.tsx`, not in `PlayPage`.** Switching to
Optimize unmounts the play surface, and neither the Go runtime nor a ringing
note should die because the user looked at another tab. `useEngineWorker` and
`useAudioEngine` are therefore mounted above the router and passed down.

**The Optimize tab is a stack of small pieces around one job.** `App` keeps the
API probe and lazy browser worker alive across tab switches; `OptimizePage`
selects that worker or the native service and hands its snapshot to `FitForm`
(start and cancel) and `FitProgress` (the curve, numbers and audition). Every
snapshot, wherever it arrives from, is one whole status object; neither backend
sends a delta, so there is no reconciliation to write.

Until this phase the front end was 40 KB of hand-written ES modules with no
build step. TypeScript was the reason to change that, more than React: the fit
API's wire shapes are eleven Go structs, and `internal/server/fit_test.go`
already re-declares `fitSnapshot` locally on purpose so that a field renamed in
the server is a failing test rather than a silently renamed wire field.
`src/api/types.ts` is the same guard on the browser side, transcribed by hand
from the Go structs rather than generated, and it lists every field the server
sends including the ones no component reads yet.

## The WASM bridge

Each Go module publishes exactly one global, `glockenspielWasm`, inside the
worker that owns it. `cmd/glockenspiel-wasm` supplies `init`, `noteOn`,
`setMasterGain`, `setPreset` and `processBlock`; `cmd/glockenspiel-fit-wasm`
supplies `fitStart`, `fitCancel`, `fitPreset` and `fitRender`. The workers have
separate global scopes, and one namespace per module makes ownership visible at
a glance.

### Changing the sound

`setPreset` rebuilds the engine around another embedded preset rather than
retuning the one that exists. `RealtimeEngine` has no preset API, and giving it
one would mean reconfiguring oscillator banks underneath a running audio
callback for a change someone made once, by hand, in a menu. A rebuild costs a
level-calibration sweep -- 61 short renders -- and cannot leave a half-swapped
bar behind. Notes ringing at that moment stop, which is what a patch change does
on any sampler. The master gain is replayed onto the new engine in Go, because
nothing on the page knows the volume changed and nothing would put it back.

The choice reaches the module twice over, which is deliberate. `init` takes the
preset id as an optional second argument and `setPreset` takes it on its own,
because the picker is reachable long before the engine exists: an `AudioContext`
cannot be created until the first user gesture, so a sound chosen on a freshly
loaded page has to survive until the first strike. `engine.worker.ts` holds the
last id and hands it to `init`, and only applies it live once `init` has run.

The option list does **not** come from the module. `cmd/gen-presets` writes
`src/api/presets.generated.ts` from the documents in `assets/presets`, checked
by `just check-presets` in CI, for the same reasons the Mayfly tuning table is
generated: the deck renders long before the module finishes loading, and a
static host has no service to ask, so a picker fed from the engine would be
empty exactly when someone first looks at it -- and empty in the Playwright
baselines. An unknown id is still refused in Go, so the generated list is a
convenience rather than the authority.

### The ready handshake

The module announces itself by calling `__glockenspielWasmReady` on whatever
global scope it is running in, which `src/audio/engine.worker.ts` installs
**before** the Go runtime starts. It has to
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

The audio and fit modules are fetched as `glockenspiel.wasm` and
`glockenspiel-fit.wasm`, with their independent content hashes from
`manifest.json` appended as `?v=<hash>`. The audio name stays fixed because
`internal/server` recognises a missing build by it; both fingerprints travel in
the query string. A missing manifest is not fatal — each worker falls back to
its bare module name and normal revalidation.

### The detached-`ArrayBuffer` hazard

`processBlock` returns a pointer into Go's linear memory, and
`src/audio/engine.worker.ts` reads the samples through a cached `Float32Array`
before copying them into the buffer it sends on. The cache exists because a
fresh view per block is one allocation every ~2.9 ms at 128 frames and 44.1 kHz,
on the thread whose whole job is to stay ahead of the audio callback. But a
hoisted view is exactly where this goes wrong, so `interleavedFrames`
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
block is sent as silence rather than skipped, so the consumer's queue keeps its
pace and the buffer comes back.

The samples are then **copied** into a pooled buffer. They have to be: they live
in Go's linear memory, which the worker cannot give away, and 256 floats per
block is the whole price of the arrangement.

## Where the audio runs

The graph is an `AudioWorkletNode` fed by the engine worker over a `MessagePort`
the two hold directly, created lazily on the first strike because a browser will
not start an `AudioContext` without a user gesture. Synthesis therefore shares no
thread with React, and a blocked main thread no longer interrupts a ringing note:
measured at 0 dropouts against 280 for the `ScriptProcessorNode` this replaced,
under the same three seconds of blocking work.

[audio-transport.md](audio-transport.md) is that decision in full — the pool that
doubles as flow control, why not `SharedArrayBuffer`, why not the module inside
the worklet, the fallback, and the two ordering mistakes that make the dropout
counter lie.

## The two-step build

```bash
just build-web
```

runs `scripts/build-web.sh`, which does the React bundle first
(`npm ci && npm run build`) and `scripts/build-wasm.sh` second. Both write into
`web/dist`. Vite runs with `emptyOutDir: false` so it does not delete the modules
beside it, and the modules are built second, so neither step erases the other's
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

The tab has two backends with one UI contract. When `glockenspiel serve` is
present it is a client of the fit API described in [serve.md](serve.md). On a
static host it sends the same validated inputs to a dedicated optimizer worker.
Both produce the `FitSnapshot` shape consumed by the chart, status and audition.

1. **Probe.** `useApiAvailable` fetches `GET api/version` once when Optimize is
   first opened. An answer selects the native service. Where it 404s — there is no `/api/`
   catch-all, so a static host falls through to its normal 404 —
   `useWasmFitWorker` lazily starts the browser backend and the form stays
   available.
2. **Start.** `FitForm` builds one body containing the reference WAV, an optional
   starting preset, optional bounds, and every scalar. The native backend posts
   it to `POST api/fit/start`; the browser backend transfers its file parts to
   the worker and serializes the scalars. Each scalar is held client-side to the
   server's range, because discovering that `note` was 200 after moving a 16 MiB
   WAV is needless work. A native `409` — "a fit is already running" — is
   surfaced with those words, and its current job is adopted so Cancel is
   reachable. The browser worker already owns and displays its single job.
3. **Watch.** For a native job, `useFitEvents` opens an `EventSource` on
   `api/fit/events`. A browser job posts whole snapshots from its worker. Both
   feed the same in-place cost history and terminal-state handling.
4. **Read.** `CostChart` plots best and current cost against the optimizer's own
   iteration count; `FitStatus` shows the state, counts, elapsed time and stop
   reason. `Audition` either calls `api/fit/audio` and `api/fit/preset`, or asks
   the worker to return equivalent WAV and JSON blobs.

Four details of that loop are load-bearing enough to write down.

**There is no polling.** The native stream and browser-worker messages carry
progress. Both cancel calls resolve only after a terminal snapshot: the server
waits for the run to stop before answering, while the worker resolves its
pending request when Go emits `canceled`. The one non-stream read is a single
`GET api/fit` when the native backend connects, because a server fit outlives a
reload and should return with Cancel reachable rather than strand its slot.

**The stream must be closed on a terminal event.** It carries no `id:` and no
`retry:`, so there is no Last-Event-ID replay to lean on. A source left open past
`done` is reconnected by the browser, handed the same terminal snapshot, and
reconnected again, forever. `close()` on `done` and on `shutdown` is not
tidiness; it is the only thing that ends the loop.

**`hasPreset` is not `state === "succeeded"`.** A run cancelled after its first
report still has the best parameters found so far. The server answers its preset
and audio endpoints for it; the worker returns the corresponding JSON and WAV
blobs. The audition gates on `hasPreset` for that reason: someone who watched a
curve flatten out and pressed Cancel wanted the result, not an empty panel.

**Two iteration counts, two rows.** `iteration` counts progress _reports_ and
moves once per `reportEvery`; `optimizerIterations` is the backend's own count
and the only one comparable with the request's `maxIterations`. They are separate
fields in the wire format for a reason and are displayed separately for the same
one. `maxIterations` itself is not in the snapshot — the server does not echo the
request back — so the form passes what it sent alongside the start snapshot. It
is kept stamped with the job id it was sent for, and read back only for that
job: a run this page did not start — picked up from the mount read, from the
stream after a reload, or after the slot was reused — has no known limit, and
the row then shows the bare iteration count rather than "n of m" against an `m`
belonging to a different fit.

### What the cost chart has and has not been shown to do

The y axis is logarithmic, which cannot plot a zero or a negative. Both occur:
the initial snapshot reports `bestCost: 0` on every run, before any candidate
has been evaluated. Such a sample is dropped rather than clamped to an invented
epsilon that would draw a floor which is not in the data.

A run at `reportEvery: 1` may produce 100,000 samples, so nothing on the path
from the stream to the canvas walks the history: `useFitEvents` grows one array
in place and counts the samples it has recorded, and `CostChart` folds in only
what it has not seen yet and coalesces the redraws into one per animation frame.
Rebuilding either the array or the datasets per event would make drawing the
curve quadratic in the number of samples.

The curve has been watched filling live from the stream, at `reportEvery: 1`,
with the coalescing the server documents visible in the gap between the sample
count and the iteration count. It has **not** been watched falling against a real
fit. Fitting `testdata/reference/legacy_synth_a4.wav` from the shipped default
preset reports a flat cost from iteration 1 for both optimizers, which is
expected rather than broken: Phase 5.1 re-fitted that very preset against that
very reference, so a run starting there starts at its own optimum. It makes that
fixture a poor demo, and the falling-curve case was exercised with a synthetic
series instead.

## Fitting on Pages

GitHub Pages has no Go process, so the failed version probe starts a second,
optimizer-only worker. It loads `glockenspiel-fit.wasm`, leaving Play's smaller
audio module and runtime alone: fitting cannot block the worker that keeps the
audio queue full. The WAV, optional preset and optional bounds cross into the
worker as transferred `ArrayBuffer`s and are decoded in memory by
`internal/browserfit`; no upload or filesystem is involved.

The browser backend exposes both Simple and Mayfly and uses the same objective,
codec, bounds parser and artifact renderers as the native paths. Mayfly is held
to one worker because Go's `js/wasm` target is single-threaded. The optimizer
reports internally once per iteration even when the user asks to display fewer
points. Each internal report sleeps for one millisecond after updating the best
preset, which cooperatively hands the worker event loop back to JavaScript so a
queued Cancel command is actually observable during CPU-bound Go code.

This is a demonstration backend, not a performance replacement for the CLI.
The module is larger because it now includes Gonum, Mayfly and the spectral
objective; execution has neither the native SIMD kernels nor multi-core
candidate evaluation. The UI says so while retaining the native service as the
preferred backend whenever it is reachable.
