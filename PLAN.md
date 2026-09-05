# Glockenspiel Plan

## Goal

A small, fast, SIMD-friendly oscillator bank and the tooling around it:

- **Core**: a configurable bank of `N` independent phase-rotation oscillators with `M`
  harmonics each. No `sin()` in the inner loop, no physical modelling. Counts are runtime
  configuration, not compile-time constants.
- **SIMD**: true packed kernels in Go Plan 9 assembly. AVX2 and ARM NEON are first-class;
  SSE2/SSE3 for backward compatibility; AVX-512 where available.
- **Optimizer**: parameter fitting driven by the current release of `cwbudde/mayfly`,
  exposed through a CLI that can also `serve` an interactive web UI.
- **Web app**: the user-facing product. WASM-compiled Go, shipped to GitHub Pages by a
  GitHub Action, with a **Play** tab for a pretrained model and an **Optimize** tab.

## Phase index

| Phase | Title                        | Status                                    |
| ----- | ---------------------------- | ----------------------------------------- |
| 0     | Unblock                      | done                                      |
| 1     | Configurable oscillator bank | done                                      |
| 2     | Real SIMD on three targets   | done                                      |
| 3     | Optimizer                    | done                                      |
| 4     | Serve and the optimizer UI   | done                                      |
| 5     | Web app                      | open — 5.1–5.6 done; payload size remains |
| 6     | Split out VST3               | done                                      |
| 7     | Documentation                | open — 7.1 and 7.2 done                   |
| 8     | Training                     | done — 8.0–8.10                           |

Closed phases are summarised here and documented in full under [docs/](docs/); the detail that
was in this file has moved there rather than been dropped.

## Status (2026-09-02)

Reviewed against the goal above. What exists and works:

- A configurable oscillator bank (`internal/oscbank`): `N` oscillators with `M` harmonic
  partials each, both ordinary runtime values, in an AoSoA float32 layout.
- Three packed oscillator kernels — AVX2, SSE2 and NEON — plus one packed AVX2 Chebyshev
  kernel, under a written numeric contract with a harness that enforces it. Everything else
  runs the portable kernel, roughly 7x slower on amd64 and 5.3x on arm64.
- An audio path that neither allocates nor locks, note-on included.
- Preset schema v1 and v2 side by side, WAV note rendering, offline fitting with two distinct
  objectives (time-domain RMS and an STFT magnitude error; `log` is RMS under a monotone
  transform), Nelder-Mead and Mayfly v0.6.0 backends, checkpoint and resume, legacy-reference
  regression tests.
- CI that builds, vets, race-tests on amd64 at `GOAMD64=v1` and `v3` **and** on arm64, lints,
  checks formatting, checks module tidiness, builds the WASM target, and runs the web unit and
  visual suites.
- A deployed web app: a playable instrument and an Optimize tab that fits either against a
  local `serve` or in the browser.
- A public `model/` package a second module builds against, at `v0.1.0`.

What does not match the goal:

- AVX-512 is deferred rather than written, with the reason in `## Deferred`.
- The remaining Phase 5 item is payload size. Adding the optimizer makes the raw WASM larger,
  so splitting or lazy-loading the Go payload is the material follow-up rather than a cosmetic
  byte reduction. Two of Phase 5's acceptance criteria — first paint under a second, and
  Lighthouse accessibility ≥ 90 — have never been measured, and there is no measurement tooling
  in the repository to measure them with.
- The browser fit path is intentionally the slower demonstration path.
- The optimizer has never been shown to produce a good fit against a recording. Phase 8's
  review says why, item by item; the short version is that the objective cannot express what a
  listener judges and nothing about the search was ever measured on this problem.

---

## Phase 0: Unblock — DONE (2026-08-21)

Goal: make CI able to tell you when something breaks, before changing anything else.

`go build`, `go test -race` and `golangci-lint` pass and run on every push and pull request;
no compiled binaries are tracked; MIT LICENSE with `web/THIRD_PARTY.md` covering the vendored
`wasm_exec.js`; JS, CSS and HTML are covered by `treefmt` plus a prettier CI job; dependencies
current at algo-dsp v0.7.0, algo-fft v0.8.0, mayfly v0.5.1, algo-vecmath v0.1.3.

Worth remembering: `golangci-lint` had never passed before this phase, and `varnamelen` is
configured for DSP names rather than disabled. `check-tidy` was left out of CI because the
`vst3go` `replace` directive made it unrunnable on a runner; Phase 6.3 earned it back.

## Phase 1: Configurable Oscillator Bank — DONE (2026-08-21)

Goal: replace the fixed 4-mode bar model with a bank whose oscillator and harmonic counts are
runtime configuration, laid out for SIMD, without losing performance.

Both counts are ordinary runtime values: `Bank.SetOscillators` takes any `N` and any per-mode
harmonic count. Sixteen rotors run in 1128-1154 ns per 512-sample block against 1314-1384 ns
for the four-rotor kernel they replaced — four times the work in 15% less time — and cost per
rotor-block is flat to within 10% from 16 to 256 rotors, so 64 oscillators cost 3.6x four
rather than 16x. Every shipped preset renders bit-identically after a v1 round-trip and after
a v2 upgrade.

The design, the benchmark tables and the compatibility rules are in
[docs/oscillator-bank.md](docs/oscillator-bank.md). Two deferrals from this phase became
Phase 2 work: cross-voice lane packing and bit-identity across backends.

## Phase 2: Real SIMD On Three Targets — DONE (2026-08-22)

Goal: one kernel shape, three packed backends, one differential test suite.

Three packed oscillator kernels — AVX2, SSE2, NEON — plus one packed AVX2 Chebyshev kernel,
each registered in `availableBackends()` and executed by CI on hardware that has it
(`ubuntu-latest` at `GOAMD64=v1` and `v3`, `ubuntu-24.04-arm`). A written numeric contract with
a harness that enforces it: golden vectors, a differential grid, and `FuzzOscBankMatchesGeneric`
— the first fuzzing in this repo, seeded with the pathology list so CI covers it without
running the fuzzer. The audio path neither allocates nor locks, note-on included. Denormal
flushing is a scope (`oscbank.FlushDenormals`) rather than a branchy per-block floor.

Everything about it is written up in [docs/oscillator-bank.md](docs/oscillator-bank.md): the
[numeric contract](docs/oscillator-bank.md#the-numeric-contract) and its four rules, the
[voice-major bank](docs/oscillator-bank.md#the-voice-major-bank), the
[realtime render path](docs/oscillator-bank.md#the-realtime-render-path) including why nothing
allocates or locks, and the [benchmark tables](docs/oscillator-bank.md#measured-performance).

Two results worth carrying forward:

- **Lane packing bought less than the counting predicts, for an instructive reason.** The rotor
  share of a block at eight voices fell from 25% to 6% — the fourfold saving the lane
  arithmetic promises — but two thirds of it goes straight back out on the scalar interleave,
  and the rest is dwarfed by the per-voice excitation lowpass at 31% of a block, which does not
  pack. Net: -12% at eight sounding voices, +24% at one. Polyphony no longer costs linearly in
  rotors; it still costs linearly in everything else a voice does.
- **What is left is not Phase 2 work.** The excitation lowpass and the scalar 8-lane interleave
  are now the two largest terms in a realtime block, and neither packs across voices.

## Phase 3: Optimizer — DONE (2026-08-21)

Goal: use mayfly as intended, make the objectives measure what they claim to, and make the CLI
usable.

Failures print real errors rather than exiting 1 in silence; a mayfly fit started from the
exact optimum scores near zero; `--time-budget` stops within its budget and `--resume` picks up
at the checkpoint's exact cost rather than a fresh population; objective evaluation sustains
~561% CPU and is race-free; a reference with leading silence converges, aligned cost below
1e-6 against 100x that unaligned.

The parameter space, the objective's contracts, and the checkpoint format are in
[docs/optimizer.md](docs/optimizer.md); the flags are in
[docs/user-guide.md](docs/user-guide.md).

One finding from this phase became later work: the shipped `assets/presets/default.json`
rendered at about +15.8 dBFS, which Phase 5.1 traced to something other than a level error.

## Phase 4: Serve And The Optimizer UI — DONE (2026-08-23)

Goal: run optimization interactively from a browser.

`glockenspiel serve` registers the static app and the fit endpoints from one `Handler()`, so
one process on one port is the whole story. A fit can be started, watched, auditioned and
downloaded from the Optimize tab — driven end to end against
`testdata/reference/legacy_synth_a4.wav`, with the downloaded JSON rendering under
`glockenspiel synth --preset`. On a static host where `GET api/version` 404s, the tab lazily
loads a dedicated WASM fit worker and keeps the same form, curve, cancel, audition and
download; the native service stays preferred when reachable.

The API — routes, the single-slot job manager, bounds, the SSE stream and shutdown — is in
[docs/serve.md](docs/serve.md). The front end — the React rewrite, the WASM bridge, the
two-step build, hash routing, the Optimize loop and fitting on Pages — is in
[docs/web-app.md](docs/web-app.md).

Four decisions from this phase that the docs record and that cost real time to reach:

- **`go:embed` names files, not the tree.** A directory pattern would pull in the gitignored
  `web/dist` whenever it happened to exist, making the binary's contents depend on the state of
  an ignored directory. A Vite bundle cannot be embedded at all without making the binary
  depend on whether someone ran a build step, so `DistDir` became the served root and the
  embedded tree shrank to a placeholder naming `just build-web`.
- **SSE streams are closed before `http.Server.Shutdown` runs.** An SSE response is an active
  connection forever and would otherwise burn the whole shutdown timeout on every Ctrl-C.
- **The fit context is rooted in `context.Background`, not the request that started it**, which
  returns immediately. `POST /api/fit/cancel` waits for the run to actually stop before it
  answers, so cancel-then-start needs no polling.
- **The React rewrite was the decision 4.3 turned on.** An Optimize tab is a stateful form on
  top of a stateful stream, which is where 40 KB of hand-written DOM builders with every piece
  of state as a module-level `let` stops paying for itself. TypeScript earned its place
  separately: eleven Go wire structs transcribed by hand in `src/api/types.ts`, so a renamed
  server field is a failing build rather than a silently undefined value at runtime.

## Phase 5: Web App

Goal: make the user-facing product good.

Acceptance criteria:

- [x] Audio runs off the main thread with no dropouts under load.
- [ ] Volume affects a ringing note; low keys are in phase and unclipped.
- [ ] First paint under one second on a mid-range device.
- [ ] Full keyboard traversal with visible focus; Lighthouse accessibility at least 90.
- [ ] The WASM payload is materially smaller and cache-busted.

### Phase 5.1: Audio correctness

Goal: fix the three level bugs. All of them live in Go and are unit-testable without a browser.

- [x] Make master gain reach sounding notes. `ProcessBlock` now reads `e.masterGain` once per
      block and folds it into the per-voice pan coefficients there, so `SetMasterGain` reaches a
      note that is already ringing. The voice stores unit-gain coefficients only.
- [x] Fix `gainsForNote`. It spans `KeyboardFirstNote`..`KeyboardLastNote` (36..96) and clamps
      the position before it becomes a pan, so every MIDI value — including the 0..35 and 97..127
      a stray note-on can carry — yields two gains inside [0, 1] and no phase inversion.
- [x] Resolve the default preset's level, carried over from Phase 3. `TestDefaultPresetRenders`
      `NearMinusThreeDBFS` pins the result at −3 dBFS at 44.1 kHz so it cannot drift back. This
      closes the item as written, at the preset's own note; levelling the rest of the keyboard
      is the separate item below.

  **Redone.** The first pass divided the amplitudes by 8.72 to bring a +15.8 dBFS render
  down, and that 6.174 peak was almost entirely the Chebyshev shaper's DC offset rather
  than the instrument: the shaper summed `gains[k] * T_(k+1)(x)`, whose even members are
  nonzero at the origin, so it emitted a constant −0.3 for silence, and at the excitation
  stage that drove the bank forever. With the shaper made DC-free the same preset rendered
  at −38.07 dBFS, its oscillator bank 18 dB _below_ the dry `input_mix` path, and no
  rescale could reach −3 dBFS: at velocity 100 the excitation reaching the bank peaks at
  −31.5 dBFS, and with `AmplitudeMax = 2` the ceiling was about −31.7 dBFS. The preset was
  re-fitted against `testdata/reference/legacy_synth_a4.wav` instead — readable at last,
  see the decoder note below — which moved `filter_frequency` from 523 Hz to 1304 Hz and
  bought the missing level there. It renders at −3.19 dBFS at 44.1 kHz, correlates 0.9655
  with its reference against −0.5261 before, and its note decays to silence in 1.85 s
  rather than sustaining for the full four. `just refit-default` is the command.

- [x] Un-mute the low register. `scaledParamsForNote` divides `DecayMs` by the transposition
      ratio, which is correct, but the single `DecayMsMax = 500` served both as the validation
      ceiling and as the optimizer's search bound: the shipped preset's 188.2 ms mode becomes
      1266 ms at note 36, `ValidateBarParams` refused it, and `NoteOn` discarded the error, so
      MIDI 36..52 — the bottom 17 of the 61 playable keys — were silent and left no trace. The
      constant is split into `DecayMsValidationMax` (5000 ms, clearing the search bound
      transposed to note 36 at 3364 ms) and `DecayMsSearchMax` (500 ms, unchanged), and
      `RealtimeEngine` counts refused note-ons rather than dropping them.

- [x] Level the keyboard. Un-muting the low register exposed a 27.78 dB peak spread across
      36..96 with the shipped preset (+13.8 dBFS at note 36, −14.0 dBFS at note 96), which the
      realtime limiter turned into audible distortion: notes 36..50 clipped at the engine's
      default gain, 33425 samples on note 36 alone. `RealtimeEngine` now measures the loaded
      preset once per playable note at construction and normalises every note to the level of
      the preset's own note. The law is measured rather than written down because it is not a
      property of transposition: the shipped preset tilts −0.485 dB/semitone because its four
      modes beat against each other, while single-mode `testdata/presets/minimal` tilts
      −0.134 dB/semitone, so any fixed curve is wrong for one of them. (The −0.46 recorded
      here before the re-fit was not that: it was the DC offset driving each rotor to a
      note-dependent steady state. Removing the DC flattened the old preset to
      −0.071 dB/semitone, and the tilt only came back once the modes carried the signal
      again — so the conclusion held, but not for the stated reason.) Realtime spread is now
      4.08 dB and no note
      clips at maximum velocity and maximum master gain. The remaining 4.08 dB is the stereo
      pan alone — 20·log10(0.8/0.5) exactly — and is energy-preserving by construction, so it
      is not a level error and is not a target for further reduction. The offline path
      (`RenderNote`) is deliberately left untrimmed: the optimizer fits against it.

### Phase 5.2: Audio transport

Goal: get synthesis off the main thread.

- [x] Replace `ScriptProcessorNode` with `AudioWorklet`. The graph is an
      `AudioWorkletNode` (`web/src/audio/renderProcessor.ts`) fed by the worker that owns the
      Go module, over a `MessagePort` the two hold directly, so no audio crosses the main
      thread. Measured in headless Chrome at 48 kHz with a note ringing and the main thread
      blocked solid for 3 s: 0 dropouts, against 280 for the `ScriptProcessorNode` under the
      same load. The old node survives as a fallback for a browser without `AudioWorklet`,
      drains the same `BlockQueue`, and is reachable with `?audio=scriptprocessor` so it stays
      tested.

- [x] Decide explicitly between a same-thread worklet instance and a Worker plus ring buffer.
      **A Worker hosts the module**; the decision and the two rejected alternatives are in
      `docs/audio-transport.md`. `SharedArrayBuffer` needs COOP/COEP, which `internal/server`
      could send and GitHub Pages cannot. Running the module in the `AudioWorkletGlobalScope`
      was the real alternative and was rejected on two counts: `wasm_exec.js` throws by name
      without `crypto`, `performance`, `TextEncoder` and `TextDecoder`, the Go scheduler wants
      `setTimeout`, and that scope has none of them; and it would put Go's collector on the
      render thread with no queue in front of it.

  Flow control is the buffer pool itself. `POOL_SIZE` buffers exist, `postMessage`
  transfers one away and detaches it in the sender, so the worker renders only into a free
  buffer and a buffer coming back is the request for the next block — no timer, no
  unbounded queue, nothing allocated per block. Four 128-frame buffers is ~11.6 ms, which
  is both the jitter tolerated and the worst case for note-on latency.

  Two things that made the dropout counter lie and are fixed here: the node is connected
  only after the producer is running, because `NewRealtimeEngine` measures the preset once
  per playable note and the graph would otherwise pull against an empty queue for that
  whole time; and Chrome calls `process()` on a source worklet node whether or not it is
  connected, so `BlockQueue` counts no underrun before its first block (~120 of them
  otherwise, none audible).

### Phase 5.3: WASM bridge and build

Goal: a smaller, cache-busted payload behind a bridge that says what it means.

- [x] Namespace the globals. `wasmInit`, `wasmNoteOn`, `wasmSetMasterGain`, `wasmProcessBlock`
      and `wasmGetMemoryBuffer` all sit on `js.Global()` (`cmd/glockenspiel-wasm/main.go:18-22`).
      Now one global, `glockenspielWasm`, carrying `init`, `noteOn`, `setMasterGain` and
      `processBlock`.
- [x] Delete `wasmGetMemoryBuffer` (`cmd/glockenspiel-wasm/main.go:74-81`): it reads
      `__algoGlockenspielWasmMemory`, which nothing sets, and no JS calls it.
- [x] Cache the `Float32Array` view instead of allocating one per audio callback
      (`web/main.js:55-59`). The cache is revalidated per callback against buffer identity,
      detachment and the pointer, because growing Go's heap detaches the `ArrayBuffer`.
- [x] Replace the 50 ms `setTimeout` WASM-ready race (`web/main.js:200`) with a real ready
      signal from Go. Go now invokes `window.__glockenspielWasmReady` once its exports are in
      place.
- [x] `scripts/build-wasm.sh`: add `-trimpath`, `-ldflags="-s -w"`, a `wasm-opt` pass and
      content-hash cache busting, and stop it overwriting the tracked `web/wasm_exec.js`.
      3,476,521 -> 3,212,389 bytes with `wasm-opt` installed; the pass is optional, the hash
      travels in the query string so the artifact keeps the name `internal/server` expects,
      and `--refresh-wasm-exec` updates `web/wasm_exec.js` deliberately. A tracked
      `web/wasm_exec.js` that does not match the toolchain fails the build before the
      compiler runs, rather than shipping a module paired with a shim of another ABI.
- [x] Document the TinyGo decision either way. Nothing in `docs/`, `README.md` or `web/README.md`
      mentions it today. Decided against for now; see `docs/tinygo-evaluation.md`.

### Phase 5.4: UI quality

Goal: fast first paint, usable by keyboard, and no controls that lie.

- [x] Bake the wood textures at build time. The sampler and all four species are preserved in a
      deterministic Node generator backed by shared JSON presets. It writes tracked 1024x576
      PNGs using only Node's built-in zlib; `wood:check` gates the normal web build, while the
      browser now switches imported static URLs without canvas work before first paint.
- [x] Fix the printed key hints. `computeNoteLayout` now derives every natural and accidental
      label from `keyBindingFor`, the same source used by `computeKeyMap`. A focused Vitest
      regression checks every bound bar against the keyboard listener map.
- [x] Accessibility. Done with the React rewrite in 4.3, because retrofitting it into markup
      that was being replaced anyway would have cost several times more. Bars and keys are
      real `<button>`s, so click, Enter and Space all strike, and every one carries an
      `aria-label` — the black and non-C white keys used to have no accessible name at all,
      46 of the 61. `:focus-visible` outlines are defined for every control; a Tab to a piano
      key reports a 3 px ring in the browser. The status panel and the fit status are
      `aria-live="polite"`, and the cost chart — a canvas, opaque to a screen reader —
      repeats its reading as visually-hidden live text.

  Not done: the Lighthouse ≥ 90 number in this phase's acceptance criteria has not been
  measured. The individual items above were checked directly instead.

- [x] Wire or remove the inert controls: removed with the rewrite. The hamburger, the
      one-hardcoded-option preset select and the disabled Save/Load buttons are all gone.
      Deleting beats shipping controls that lie.

  Superseded for the preset select: it is back in the performance deck, with two
  real sounds behind it. `assets/presets` is embedded as a directory, so adding a
  preset is adding a file; `cmd/gen-presets` mirrors the list into the browser and
  CI fails on a diff; and `cmd/glockenspiel-wasm` grew `setPreset`, which rebuilds
  the engine around the chosen bar and replays the master gain onto it.

- [x] Fix the `<h1>`, which still read "Algo Glockenspiel VST3" on a page that is not a VST3.
      It reads "Algo Glockenspiel".

- [x] Give the deck a Reverb dial. `internal/synth` grew a stereo bus reverb around
      `algo-dsp`'s `FDNReverb` -- two detuned networks, run wet-only, blended by the engine
      with a per-sample ramp so the dial glides rather than clicks -- and
      `cmd/glockenspiel-wasm` grew `setReverb` beside `setMasterGain`, replayed onto a
      rebuilt engine the same way. It is a live setter, not a preset field: a preset change
      costs a 165 ms calibration sweep and a dial produces a value per pointer move, so a
      rebuild per step would stutter the transport for the length of the gesture. The engine
      default is dry, which is why nothing else in the repository changed sound; the page
      starts the dial at 20%. Measured cost of a 128-frame stereo block: 3.0 us dry against
      58.8 us wet, and a closed dial is an exact bypass that does not run the networks.

### Phase 5.5: Visual and responsive redesign

Goal: keep the warmth of a physical wooden instrument while replacing the repeated faux-wood,
gloss and heavy panel treatment with a balanced contemporary workshop aesthetic. The Play view
must remain the focal instrument; Optimize must read as a guided workflow rather than one long
undifferentiated form.

Baseline captured with Playwright on 2026-08-22 at 1440x1000 and 390x844:

- Desktop Play repeats the same pale texture across the header, selector strip, rack and keyboard,
  while the narrow control rail competes with a much larger central rack.
- Mobile squeezes fixed-size bars into the viewport until their bodies and labels overlap. The
  keyboard scrolls independently, so its pitches no longer remain spatially aligned with the rack.
- Optimize uses full-width fieldsets with nearly identical visual weight, shows an empty chart
  before there is data, and loses most of the instrument character present on Play.

Acceptance criteria:

- [x] At 390px wide, every bar keeps a usable hit target and readable note label; the rack and
      keyboard share one pitch-aligned horizontal viewport rather than overlapping or scrolling
      independently.
- [x] At 1440px wide, the rack is the clear Play-view focal point, the performance controls form
      one balanced control deck, and the keyboard reads as a supporting input aid.
- [x] Wood appears on structural instrument surfaces rather than every panel. Bars and hardware
      have a distinct restrained metal treatment, and text does not rely on textured backgrounds
      for contrast.
- [x] Optimize presents setup, execution and results as a clear sequence, does not render a blank
      chart as though it contained data, and keeps primary job actions near current job state.
- [x] Playwright reference screenshots cover Play and Optimize at 1440px, Play at 1024px, and Play
      at 390px. Keyboard traversal, visible focus, reduced motion and existing audio controls still
      work after the redesign.

Bite-sized tasks, intended to be independently reviewable in this order:

- [x] **5.5.1 — Pin the visual baseline.** A Chromium screenshot project and commands
      that capture deterministic Play and Optimize states at 1440x1000, Play at 1024x768, and Play
      at 390x844 now mock the engine worker and fit API, so neither WASM nor a Go server can make
      the images race. Four Linux references are tracked; generated actual/diff images, traces and
      HTML reports stay ignored and are uploaded by CI when the comparison fails.
- [x] **5.5.2 — Introduce design tokens without moving layout.** `web/src/styles/index.css` now
      defines the workshop palette, material recipes, type roles, spacing, radii and elevation in
      one token layer, with separate roles for canvas, parchment, charcoal ink, structural wood,
      brass/bronze, copper, metal bars and focus. Existing selectors resolve to their previous
      values; all four pixel-exact Playwright references pass without a snapshot update.
- [x] **5.5.3 — Calm the application shell.** The page now has quiet flax canvas depth, while a
      shorter, softly elevated masthead uses restrained dark wood behind a smaller brand mark and
      title. Play/Optimize is one compact segmented control with an unambiguous active segment and
      clean 390px wrapping. The logo, product name, hash links and focus ring remain intact, and the
      inaccurate kicker now reads “Algorithmic Instrument.” All four shell-affected references
      were intentionally updated and pass pixel-exact comparison.
- [x] **5.5.4 — Simplify the Play surfaces.** The preset strip, performance surround, rack bed and
      keyboard deck are now matte neutral surfaces with lighter borders and elevation. The rack's
      single structural frame and its support rails retain the selected procedural wood; the extra
      cream inset, blurred rack shadow and repeated texture layers are gone. The three Play
      references were intentionally updated, while Optimize remains pixel-exact and unchanged.
- [x] **5.5.5 — Rebuild the performance control deck.** `ControlDeck` now places the compact
      Volume and Velocity dials, wood selector and description, and live engine status together
      above the playfield, collapsing into a balanced two-column deck at 390px. Playwright verifies
      the native range bounds, keyboard and wheel changes, formatted outputs, species application,
      and both polite ready and error announcements. The three Play references were intentionally
      updated; Optimize remains pixel-exact and the full six-test Chromium suite passes.
- [x] **5.5.6 — Refine the physical instrument.** Naturals now use a brushed satin-brass face and
      dark labels; accidentals use smoked bronze with light labels, and both carry restrained brush
      lines and layered metal fasteners. Neutral support rails replace the cream/wood rods, and the
      smaller translucent mallet sits fully inside and behind the playable composition. Hover,
      pointer press and sounding-note feedback are separate subtle states without activation-code
      changes. Playwright now pins all 25 named buttons plus the 15/10 material split; the three
      Play references were intentionally updated while Optimize remains unchanged.
- [x] **5.5.7 — Make the keyboard supporting, not competing.** The keybed is shorter, flatter and
      lower-contrast, with muted graphite accidentals, quieter ivory naturals, smaller shadows and
      restrained active colors beneath the brass/bronze rack. Its component and focus rules are
      unchanged. Playwright pins all 61 unique accessible names, the 36/25 key split, MIDI 36–96
      endpoints, data-note hooks and the active-style hook. The visible desktop/tablet references
      were intentionally updated; mobile (keyboard below the fold) and Optimize remain exact.
- [x] **5.5.8 — Fix the mobile playfield.** `Playfield` now owns one C2-C7 horizontal viewport
      below the stationary control deck. At 760px and below, its pure layout geometry uses a 44px
      white-key unit, offsets the 15-unit C4-C6 rack by exactly 14 units, and initializes once at
      C4 without taking control back after user scrolling. Rack and piano centers align within one
      pixel; all 25 bar labels remain distinct on 44px hit targets, C2 and C7 are reachable, touch
      panning remains enabled, and neither the keyboard nor body creates another horizontal
      scroller. The 390px Play reference was intentionally updated; the full nine-test Chromium
      suite passes while the desktop, tablet and Optimize references remain pixel-exact.
- [x] **5.5.9 — Clarify Optimize setup.** Reference, Note and Fit Setup are now three numbered
      sections, with reporting, Mayfly tuning and bounds in a native Advanced disclosure that
      reopens when validation finds a hidden error without discarding entered values. Shorter
      hints keep the common path compact, while a live service badge distinguishes checking,
      connected (with version) and unavailable states. Playwright covers the ordering, disclosure,
      all three service states and the hidden-error path; only the Optimize reference changed.
- [x] **5.5.10 — Clarify Optimize execution and results.** Start and Cancel now share a control
      bar with the watched job's state. A dedicated Results area shows an intentional fresh-state
      prompt, status before the curve, a waiting message before the first sample, and audition plus
      download when a preset exists. The desktop workspace uses balanced setup/results columns and
      collapses below 980px. Mocked Playwright scenarios cover fresh, running, SSE-updated,
      canceled-with-preset and 409 recovery states without changing reconnect or API behavior.
- [x] **5.5.11 — Responsive and accessibility polish.** Play and Optimize now stay inside the body
      at 390, 760, 1024 and 1440px. Narrow layouts keep 44px effective targets without enlarging
      checkbox glyphs, and their form fields surrender intrinsic width instead of overflowing.
      Mobile piano accidentals retain a narrow visual face inside a 44px button. A reduced-motion
      media query removes nonessential animation and transition duration. Playwright walks every
      visible enabled control on both routes and confirms its three-pixel focus treatment, checks
      both available routes with Axe (no serious or critical violations), and verifies all target
      widths plus the shared mobile playfield. `typecheck`, ESLint, 16 Vitest tests, the production
      build and all 21 Chromium tests pass; only the intentionally shifted 390px Play reference was
      updated.
- [x] **5.5.12 — Rack depth and support alignment.** Natural and accidental bars now keep a
      constant 32px/28px visual width while retaining their pitch-dependent length, and both rows
      gain the same subtle eight-pixel rightward baseline drop. One support polyline per row passes
      behind every computed mount-hole center; accidentals explicitly layer above naturals. The
      foreground mallet sits below every bar at 1024px. Pure geometry tests pin width, perspective
      and mount alignment, while focused Playwright measurements pin browser geometry, z-order,
      support count/alignment and mallet clearance. Only the three Play references changed.
- [x] **5.5.13 — Aged-brass control knobs.** Both 66px dial faces now share a code-native aged-
      brass material: a dark bronze rim surrounds a desaturated face with fine directional marks,
      restrained patina and a crisp light indicator instead of the former glossy brown highlight.
      Focus remains three pixels, and Playwright pins the common material hook and gradients, exact
      geometry, keyboard adjustment, pointer adjustment and formatted values. Only the three Play
      references changed; Optimize remains pixel-exact and the full 23-test Chromium suite passes.
- [x] **5.5.14 — Compact mobile composition.** At 760px and below, the shorter shared masthead,
      52px aged-brass dials, paired wood/status panels, tighter rack bed and 56px keyboard put the
      complete C4-B4 frame and keyboard inside 390x844 without shrinking the 44px pitch unit. The
      centered viewport is derived as exactly seven white pitches (308px), initializes at C4 once,
      and still pans across the complete C2-C7 keyboard and C4-C6 rack. Browser coverage pins the
      compact vertical landmarks, effective targets, readable contained copy, one scroller, exact
      octave frame, pitch/support alignment, layering, mallet clearance, edge reachability, full
      keyboard traversal and Axe result. Only the 390px Play reference changed; desktop Play and
      Optimize remain pixel-exact.

### Phase 5.6: Browser optimizer

Goal: make the Optimize workflow demonstrable on GitHub Pages without a background server.

- [x] Keep the native service as the preferred backend and lazily start a dedicated fit worker
      only when `GET api/version` proves the page is on a static host. The optimizer has its own Go
      runtime so CPU-bound fitting cannot starve the realtime audio worker.
- [x] Accept the reference WAV, optional preset and optional bounds as transferred buffers, decode
      them without a filesystem in `internal/browserfit`, and run both Simple and Mayfly against
      the same objective, codec and validation packages used by the CLI and server.
- [x] Preserve the existing UI contract: browser jobs emit the same whole `FitSnapshot` shape,
      support cooperative cancellation, populate the live cost chart, and return fitted JSON and
      rendered WAV blobs for download and audition.
- [x] Document the performance boundary. Go `js/wasm` is single-threaded here, uses the portable
      DSP kernels and runs Mayfly with one evaluation worker, so Pages presents this as the slower
      explanatory backend rather than a native-performance replacement.

## Phase 6: Split Out VST3 — DONE (2026-08-23)

Goal: this repo builds cleanly from a fresh clone.

The plugin now lives in
[algo-glockenspiel-vst3](https://github.com/CWBudde/algo-glockenspiel-vst3) and depends on this
module the ordinary way. `plugin/`, `cmd/glockenspiel-vst3/` and `docs/vst3*.md` are deleted
here; the `replace github.com/cwbudde/vst3go => ../vst3go` directive and its `v0.0.0` require
are gone with them, so `go mod tidy` is a no-op and `go mod tidy -diff` runs as the first step
of `test-can-build`. The module is `github.com/cwbudde/algo-glockenspiel`, matching the
repository, and `v0.1.0` is tagged at the rename merge — which is what the other side already
requires, with no `replace` and no placeholder.

The public surface and its rules are in [docs/public-api.md](docs/public-api.md).

Three things this phase established, none of them obvious in advance:

- **`internal/` is enforced against the module path, not the directory.** That was the actual
  blocker: a separate module could not have imported `internal/model` even with a `replace`
  directive, so promoting the package to `model/` had to come first.
- **Exported is not the same as reachable.** `Bar.BankOscillators` returned
  `[]oscbank.Oscillator` from `internal/oscbank` for two phases. It compiled here and was
  unusable there — callable, but its result could not be assigned, passed or ranged into a
  typed variable. Nothing inside this module could notice, because same-module code may import
  `internal/`. The fix is an exported alias, which re-exports the identity without putting a
  conversion on the note-on path; `model/api_surface_test.go` now fails on any exported
  signature naming an internal type.
- **A module rename is not a repository rename.** Renaming the repository would have left a
  redirect; renaming the module breaks every existing consumer outright. Affordable only
  because both known consumers are ours. The `go-import` meta tag is not an alternative: Go
  resolves `github.com/...` through its built-in rule and never fetches one.

Two corrections this phase made to its own plan are worth keeping, because both were written
down as fact and were not: `NumModes` had already been unexported a phase earlier, and what
`git grep NumModes` still finds is the runtime `Bar.NumModes()` accessor rather than a
compile-time size. And the "twelve `*Min`/`*Max` range constants" the audit was told to check
are thirteen, with **no `DecayMsMax`** — 5.1 split it into `DecayMsSearchMax` and
`DecayMsValidationMax`, so code written against the obvious name does not compile.

## Phase 7: Documentation

Goal: make the docs describe the project that exists.

Acceptance criteria:

- [x] README describes what the repo actually contains.
- [x] The web app is documented. `web/README.md` covers building, serving and using it, and
      [docs/web-app.md](docs/web-app.md) covers how it is built and why (Phase 7.3).
- [ ] `out/` is migrated and removed.

### Phase 7.1: README

Goal: one accurate front page, built around the web app.

- [x] Rewrite `README.md` around the web app as the primary product with the CLI second.
- [x] Fix the Status section: "four-mode bar model with quadrature decay oscillators" was no
      longer what the core is, and "VST or DAW plugin support" and "GUI tooling" were listed
      as not implemented five lines below an "Implemented" list that contradicted it.
- [x] Fix the layout tree: it named `internal/model`, which moved, and omitted `web/`,
      `plugin/`, `docs/`, `scripts/`, `internal/oscbank`, `internal/cpufeat` and
      `cmd/glockenspiel-wasm`.
- [x] Also fixed while in there: the three links that pointed at absolute paths on one
      developer's filesystem (`/mnt/projekte/...`) and so were broken for every reader; the
      description of the synthesis chain as "physical-model", which the goal explicitly is
      not; and the `fit` flag list, which was missing `--bounds`, `--align` and
      `--normalize-gain` and described `--checkpoint-interval 0` as disabling only
      intermediate writes when it disables the final checkpoint too.

### Phase 7.2: Docs against the code

Goal: no path in the docs points at something that moved, in every document except the two
that are being retired rather than repaired. `docs/vst3-evaluation.md` and
`docs/vst3go-spike.md` still name `internal/model` (`docs/vst3go-spike.md:62`) and are left
that way on purpose: they leave with the plugin in Phase 6.3, and rewriting a package list
in a document that is about to move is work thrown away twice.

- [x] Refresh `AGENTS.md`'s package list — it described `internal/model` and omitted
      `internal/oscbank` and `internal/cpufeat`.
- [x] Refresh `docs/user-guide.md` against the current flag set and the v1/v2 preset schema.
      It was missing `--align` and `--normalize-gain` on `fit`, and its parameter guide
      described `modes` as a fixed four with no mention of v2's per-mode harmonics or explicit
      Chebyshev stage.
- [x] Update "Known limits" in `docs/oscillator-bank.md` as Phase 2 closes its items. The
      packed-backend and denormal limits are current. Cross-voice lane packing closed with the
      engine adoption and its limit was rewritten rather than deleted: what is left is the
      scalar interleave and the per-voice excitation chain, which is where a realtime block
      now spends most of its time. The two-sample step and the optimizer's blindness to
      per-mode harmonic gains are still real.

### Phase 7.3: The web app and the leftovers

Goal: document what is undocumented, retire what is finished.

- [x] Document the web app in `docs/`. [docs/web-app.md](docs/web-app.md): the architecture
      after the React rewrite, the WASM bridge — the `glockenspielWasm` global, the
      `__glockenspielWasmReady` handshake and the detached-`ArrayBuffer` hazard behind
      `interleavedFrames` — the two-step build and why the served root is the dist tree, why
      routing is hash-based, the dual-backend Optimize loop, and how Pages fits in a dedicated
      worker. `web/README.md`
      gained an Optimize section, and `docs/serve.md`'s "nothing under `web/` calls any of
      this yet" is no longer true and no longer says so.
- [x] Retire `docs/vst3-evaluation.md` and `docs/vst3go-spike.md` with the 6.3 split. They are
      the two documents Phase 7.2 deliberately left alone: `docs/vst3go-spike.md:62` still lists
      `internal/model` in a package list, which is the last stale path in `docs/` and is fixed
      by the move rather than by an edit. Here that is a deletion — both files are already
      copied into the split-out repository, whose Phase 5 marks them historical rather than
      repairing them. Deleted with 6.3. `docs/` now has no stale paths left.
- [ ] Clear `out/`. It is untracked and gitignored (`.gitignore:17`), so this is local scratch —
      profiles, checkpoints and rendered WAVs — not repo content to migrate. Anything in there
      worth keeping is a benchmark number that belongs in `docs/`.

## Phase 8: Training

Goal: fit the model to recordings the way the sibling MayFlyCircleFit repository
fits circles — registered campaigns, paired seeds, evaluation-matched arms, a restart schedule
chosen by measurement — against several criteria at once, so a shipped preset is the best basin
a budget can find rather than the first one a run fell into.

### What the review found (2026-09-02)

Reviewed with the sibling repositories open: MayFlyCircleFit's twelve optimizer reports, the
Mayfly library's own selection and tuning guides, and algo-piano's optimizer audit. The verdict
first, then the evidence.

**The search engine is not the problem. The objective, the search space and the evidence are.**
Every quantitative claim about the optimizer in this repository is borrowed from algo-piano;
nothing was ever measured on this objective. The only real recording was fitted once, by hand,
with no recorded command, and the shipped file is a hand-retuned edit of that fit. The default
CLI backend — Nelder-Mead, 100 iterations, 30 s — cannot solve the problem the repository's own
tests describe as "a local minimum, not a budget". Swapping Mayfly for CMA-ES on the current
objective would change the number without making the fit good.

#### Objective and matching criteria

1. **The metrics are alternatives, not criteria.** `--metric` selects _one_ of `rms`, `log`,
   `spectral` (`internal/optimizer/objective.go:265-274`), and `log` is a monotone transform of
   `rms` with the same minimiser (`objective.go:29-33`; the two fits in
   `out/phase3-metric-compare` are byte-identical). Two objectives, never combined, never broken
   down. "Fit to several criteria" is not expressible today.
2. **Time-domain RMS is a needle in a haystack in frequency.** After sample-exact onset
   alignment (`rms.go:164-229`) the cost is a raw waveform difference; a 2% error on a 1757 Hz
   partial is twenty cycles of phase drift across the reference
   (`legacy_validation_test.go:78-87`). A global search has to place every mode within a few
   cents inside a `[0.5, 10] × base` log box, all at once. The per-parameter recovery assertions
   were deleted for exactly this reason (`legacy_validation_test.go:68-93`).
3. **The spectral metric cannot resolve pitch.** 2048-point frames at 44.1 kHz are 21.5 Hz bins
   with an 86 Hz Hann main lobe (`spectral.go:18`). At 1053.6 Hz a bin is ±35 cents, so an
   intonation error a listener hears costs nothing. The metric that is robust to phase is deaf
   to pitch; the metric that hears pitch is phase-fragile. Neither is what a listener judges.
4. **The dB-domain error is outvoted by empty bins.** Every bin from 1 to 1023 in every frame
   carries equal weight inside the band (`spectral.go:94-99`). A modal model has ~900 bins at
   the −100 dB floor (`spectralMagnitudeFloor = 1e-5`) where a room recording sits at
   −60..−70 dB, so the candidate is punished for not synthesising the room's noise floor while
   the handful of bins holding the partials are a rounding error. For `glockenspiel_c5.wav` at
   −27.6 dBFS peak, that floor is only 72 dB below the peak.
5. **No decay term, no partial term.** Decay is visible only through frame-to-frame STFT
   magnitudes, and the one test of that (`spectral_test.go:70`) asserts merely "non-zero". The
   shipped default preset rings for 1.85 s against a 0.557 s reference (Phase 5.1 above) and
   nothing in the objective objects. There is no peak picking, no partial tracking, no cents
   error, no half-life error.
6. **Gain is searched, not solved.** The model has no output gain (`model/params.go:198-204`)
   and `--normalize-gain` is off by default, so the fit spends amplitude on level:
   `assets/presets/default.json` has `input_mix` at 2.0, all four amplitudes at ±2 and two
   Chebyshev gains at 0.0 — **seven of nineteen dimensions on a bound**, not the "three
   amplitudes" the previous resume point recorded, and `distance` counts an eighth: mode 1's
   frequency multiplier sits on the edge the codec created when it widened the box to contain
   it (finding 12). algo-piano hit the same wall and now solves output gain in closed form after
   the search.
7. **The reference is used raw.** The C5 recording is stereo, sits at −27.6 dBFS, and contains a
   second event at 2.0 s (`testdata/reference/README.md`). The objective applies no downmix
   policy, no trim, no level normalisation and no onset window; whoever fitted
   `recorded-bar.json` cut "the first second" by hand, and that step is in no recipe.
8. **The final line lies.** `rms=` and `log=` there are un-aligned, PCM16-quantised
   re-measurements (`internal/cli/fit.go:463-466`) printed beside an aligned `best=`, and the
   two were measured to disagree by up to 122 percentage points (`mayfly_test.go:394-401`). A
   run cannot be judged from its own output.

#### Model and parameter space

9. **`base_frequency` is a fitted dimension with no effect on the audio.** `Bar.UpdateParams`
   (`model/bar.go:251`) never reads it, and `TestBaseFrequencyDoesNotReachTheAudio` pins that.
   `(base, mult_i) → base · mult_i` is many-to-one, so every fit has a flat one-dimensional ridge
   through it, and the `justfile` tells the operator to normalise it "back to 440 by hand".
10. **Mode permutation symmetry is unbroken.** Four modes are 24 identical optima; twelve are
    4.8 × 10^8. A population spends its diversity on relabelings. No ordering constraint exists.
11. **The default box has infinite plateaus.** `base ∈ [0.01, 50000]` times `mult ∈ [0.5, 10]`
    exceeds `FrequencyMaxHz`, `DecodeParams` fails, and `Evaluate` returns `+Inf`
    (`objective.go:249-252`). A flat infinity gives a swarm nothing to follow, and Mayfly v0.7
    rejects non-finite objective values outright, so the upgrade will turn this into an error.
12. **The documented box does not contain the shipped preset.** Mode 2 of `default.json` is
    4516 / 440 = 10.26 against `frequencyMultMax = 10`, so every default fit silently runs on a
    widened box (`params.go:300-319`) and the bounds in `docs/user-guide.md` are not the bounds
    that run.
13. **`DecayMsSearchMax = 500 ms` binds on the only real recording.** `decay_ms` is a half-life
    (`internal/oscbank/oscbank.go:417`); the C5 fundamental's is 677 ms
    (`testdata/reference/README.md`). The bound was kept at 500 for a step-size argument
    (`model/params.go:92-97`) — a search concern overriding a physical one.
14. **Mode count comes from the template, not the recording.** `recorded-bar.json` searched 43
    dimensions for a recording with six measurable partials, and the result is textbook
    over-parameterisation: six modes within 14 Hz of 5150 Hz with alternating signs and 5–54 ms
    half-lives, a 0.56 ms "mode", and `harmonic_gains[1]` at 1.9999999999999385. The optimizer
    is building beat patterns to fake an attack the model cannot produce.
15. **The excitation is one impulse** (`model/bar.go:98-100`): no noise burst, no strike
    shaping. Every attack has to be manufactured from mode phases and the shaper. This is a
    structural limit that nothing wrote down as one.
16. **One note, one velocity, one duration per fit.** The keyboard slope is corrected at runtime
    by measurement (Phase 5.1) precisely because the fit knows nothing about other notes.
17. **Per-mode harmonics and the shaper stage are not fitted** (`params.go:203-208`), and a v1
    template writes a v1 preset, so a fit from `default.json` can never grow v2 features.

#### Optimizer algorithm and settings

18. **The default backend is Nelder-Mead** (`internal/cli/fit.go:71`) on a landscape the
    repository documents as multimodal and phase-sharp. The one recipe that produced a shipped
    artifact, `just refit-default`, uses Mayfly. The default and the known-good path disagree.
19. **There is no polish stage.** `simple` and `mayfly` are mutually exclusive
    (`fit.go:379-401`); nothing refines a global result locally.
20. **A warm start seeds one individual** (`mayfly.go:204-206`): one male and one female out of
    a population carry the incumbent. CircleFit's epochs reseed half the population around it.
21. **`--mayfly-seed` defaults to 1.** Every default run is the _same_ run, and the whole
    "zero means choose one and report it" machinery is inert by default. No default workflow
    ever takes a second sample of the landscape.
22. **`--mayfly-variant auto` runs the classifier v0.7.0 replaced.** `mayflyauto.go:63` calls
    v0.6.0's `ClassifyProblem`, which the v0.7.0 changelog documents as classifying a sphere as
    `Rugged` or `Deceptive` depending on the width of the box, and it is fed the unit cube. Three
    places in this repository already say the feature is not worth its budget. It is elaborate
    machinery for a measured non-effect.
23. **The pin is v0.6.0; every sibling number is v0.7.1.** v0.7.0 was a correctness release —
    attraction distances, crossover sex retention, mutation candidates, `Config.Seed`,
    non-finite rejection, QMC initialisation, and a rewritten classifier with a breaking
    `ClassifyProblem` signature. None of CircleFit's or Mayfly's measurements apply to what runs
    here, and the Status section above said v0.5.1 until this review corrected it.
24. **Nothing here was measured.** Round length, warm start, restarts, population, dialect
    choice, the 47.7 evaluations per iteration — every figure in `mayfly.go`,
    `mayflyschedule.go`, `mayflyauto.go` and `docs/optimizer.md` cites algo-piano. There is no
    comparison harness, no paired-seed recipe, no trace file, and no cost curve on disk.
25. **`MaxWorkers` is never set by the CLI**, so a CLI run's parallel width is `NumCPU` and no
    run is reproducible across machines despite the seed machinery.
26. Smaller fragilities: `--mayfly-stagnation 0` cannot be written (`fit.go:708` tests `> 0`);
    `--mayfly-nc-ratio` uses `!= 0` as "was written"; `--checkpoint-interval` counts progress
    reports, so the print cadence silently changes the checkpoint cadence; an aborted round
    under-counts iterations on resume; `archive_size`, `opposition_probability` and
    `aquila_weight` are exposed, validated knobs that do nothing here.

#### Evidence and process

27. **No test runs a fit from the real box.** Every convergence test pins `base_frequency` to a
    point and narrows every range (`e2e_test.go:155-167`, `legacy_validation_test.go:163-173`).
    No absolute-quality assertion exists anywhere.
28. **The only good result is against a synthetic render of unknown provenance.**
    `legacy_synth_a4.wav` and `glockenspiel_a4.wav` are the same bytes, "a render, not a
    recording". The one real recording's fit has no recorded command and no recorded metric
    beyond "residual 11.1 dB below the reference RMS" in a commit message; it was then retuned
    by a factor of 1.667 and had two modes deleted, so the shipped preset is not the fitted one.
29. **`docs/user-guide.md` contradicts itself**: it recommends `spectral` at line 395 and records
    it as worse at line 270, and its benchmark numbers predate the float-WAV decode fix.
30. `out/` holds v1.0 checkpoints the loader rejects, costs measured against a square wave, and
    two "different" fits that are byte-identical.

#### Serve, browser fit and the Optimize tab

31. **One slot, no history, no persistence** (`internal/server/job.go:312`,
    `docs/serve.md:191-196`). Starting job N+1 makes job N's preset, audio and status
    unreachable; nothing survives a restart. A multi-start strategy cannot be expressed from
    the UI at all.
32. **The request is never echoed back.** The snapshot carries note, velocity, optimizer and
    metric and nothing else — no budget, bounds, tuning, population, and no seed for `simple`.
    The downloaded preset carries no provenance: no reference, cost, seed, metric or timestamp.
33. **There is no comparison view.** The reference is never played or drawn; there is no
    waveform, spectrogram, A/B or parameter table. The only judgement available is whether the
    render sounds plausible on its own.
34. **No per-term breakdown, no evaluations per second, no budget progress, no epoch or restart
    index** in the snapshot; `timeBudget` is not in it at all.
35. Duplication: the limits table three times (`server/fit.go`, `browserfit.go`, `types.ts`),
    three `selectOptimizer`s, three tuning-document builders with _different_ epochs/restarts
    precedence, two duration parsers per language; `hasPreset` is true mid-run in WASM and false
    on the server for the same state; `FitForm.tsx` is 1962 lines in one component; Playwright
    never runs a real fit.

### What the sibling projects established

CircleFit's numbers come from 56–84-dimensional fits with cheap evaluations, twelve paired seed
blocks per arm, and Holm correction. The figures do not transfer; the method does.

- **Splitting the budget wins; swapping the engine alone is not shown to.** One long separable
  CMA-ES run against sixteen Mayfly restarts: t = +1.37, p = 0.20. Every significant CMA-ES win
  belongs to an arm that restarts (`docs/cmaes-budget-split-report.md`). Sixteen Mayfly restarts
  beat one Mayfly run in twelve of twelve blocks (`docs/restart-vs-budget-report.md`).
- **Separable CMA-ES with cold restarts at a fixed λ is the best measured shape** (`sep-r5`
  over `mayfly-r16`: 12/12, t = +7.04). IPOP is budget-capped — the ladder's last rung is always
  cut off, and in half the blocks the best result is still restart 0's. Block covariance with
  one dense block per entity wins 11/12 inside the IPOP schedule, and a mode's
  `(amplitude, frequency, decay)` triple is exactly that shape.
- **Dragonfly loses decisively**: zero of twelve blocks in every arm, t = −16.8, and the best of
  576 restarts is worse than every baseline block (`docs/dragonfly-poc-report.md`). It is out.
- **Measured nulls, not to be re-derived:** λ moves the variance, not the mean; QMC
  initialisation; a stagnation criterion by default; `DanceDamp`; population above the crossover
  knee; AOBLMOA.
- **Rare basins need many seeds and best-of at two population sizes**, not a paired test of
  means, "which is the instrument that just found nothing" (`docs/cmaes-restart-ladder-report.md`).
- go-cma-es v0.1.0 has a measured defect above λ 256 (separable, n = 56) or λ 1024 (block) that
  makes `ActiveCMA` inert and drops covariance memory, fixed in 0.2.0. At 15–43 dimensions with
  λ ≤ 64 it does not bite. Pin deliberately and say why.
- **The caveat that matters most here:** CircleFit's landscape is smooth in its parameters.
  Ours is phase-sharp in frequency under the current objective, and no restart schedule fixes
  that. That is why the objective comes first below.

Mayfly's own selection table marks DESMA for _inexpensive_ evaluations and EOBBMA/MPMA for
expensive ones; the wrapper here defaults to DESMA "for historical reasons". At ~1 ms per render
with parallel evaluation the point is not that DESMA is wrong. It is that nobody checked.

### Acceptance criteria

- [ ] A fit is scored on several named criteria at once, each reported as a physical number,
      and the CLI, the server and the browser all show the breakdown.
- [ ] A cold fit from the real default box recovers a synthetic six-mode target's frequencies
      within 5 cents and half-lives within 10% in at least ten of twelve seeds.
- [ ] The default engine, restart shape and budget are chosen by a registered campaign on this
      objective, recorded with its tables in `docs/training.md`.
- [ ] Both shipped presets are re-fitted by a recorded recipe, and the file that ships is the
      file the fit wrote.
- [ ] The Optimize tab keeps a run history, echoes every setting a run used, and shows the
      reference beside the fit.

### Phase 8.0: Baseline the evidence

Goal: know what today's fits score before anything moves.

- [x] Add `glockenspiel distance --reference X --preset Y`, the mirror of algo-piano's
      `piano-distance`: render once, print every term of the objective below for the _written_
      preset, aligned and raw, through the same code the objective uses. Done 2026-09-02:
      `ObjectiveFunction.Measure` returns every term from one render and a test pins it equal to
      `Evaluate` under every alignment and gain policy; `optimizer.Distance` runs the three
      policies and reports pinned dimensions and widened edges; `--json` for scripts.
- [x] Record today's numbers for both shipped presets against both references in
      `docs/training.md`, and fix `docs/user-guide.md`'s contradictory spectral advice and its
      pre-decode-fix benchmark table while there. Done 2026-09-02; `just baseline` re-takes the
      table. What it found: against the recording the least-squares gain is −52 to −93 dB for
      every shipped preset at every note, so the time-domain terms see nothing and both RMS
      policies measure a trivial quantity; a reconstruction that undoes the 1.667 retune reaches
      −26.6 dB and a residual 4.8 dB below the reference RMS, against the 11.1 dB the shipping
      commit recorded; the fitted pre-retune preset does not exist in the repository.
- [x] Clear `out/` (Phase 7.3's open item; it is square-wave-era scratch). Done 2026-09-02.

### Phase 8.1: Reference analysis

Goal: measure the recording once, by code, the way `testdata/reference/README.md` did by hand.

- [x] `internal/analysis.LoadReference`: downmix policy, trim to the first strike (onset to the
      next onset or a fixed window), peak-normalise, and record what was done. Done 2026-09-02:
      `first` or `mean` downmix, the cut ends at a second onset (6 dB above the tail's quietest),
      at the tail no longer falling for half a second, at a fixed window, or at the file's end,
      and the `Reference` record says which. The onset detector moved here from the optimizer,
      which now calls `analysis.Onset`, so the analysis and the alignment share one definition.
      `wavio.DecodeChannels` exists for it.
- [x] `internal/analysis.Partials`: peak-pick the averaged log spectrum with a long window
      (16384 points, ±1.3 Hz), refine each peak by quadratic interpolation, and measure level
      relative to the strongest partial and half-life from a narrowband envelope. Test: the six
      partials of `glockenspiel_c5.wav` within 0.5% in frequency and 20% in half-life. Done
      2026-09-02: `TestTheC5RecordingMeasuresAsTheHandTableSays` pins it; frequencies agree to
      0.02% and half-lives to 15%. Two things the plan did not foresee: a fast-decaying partial's
      own Lorentzian line shape rides the window's sidelobes into false local maxima, so a
      candidate inside a stronger partial's skirt is dropped; and the averaged level is not what
      a mode amplitude has to match, so each partial also carries its attack level, the decay
      line's value at the strike. The hand table's half-lives are least-squares lines over the
      first second, and that is the fit range here too.
- [x] Write the result as `analysis.json` into the run directory; 8.2 and 8.3 read it. Done
      2026-09-02: `glockenspiel analyze --output analysis.json` writes it and `--trimmed-out`
      writes the cut reference as a WAV; `analysis.ReadFile` reads it back and refuses a file
      without the `generated_by` marker. Run directories arrive with 8.5; until then the path is
      the caller's.

### Phase 8.2: A composite objective with a breakdown

Goal: make the objective say what a listener hears, one term per thing.

- [x] A `Metrics` struct of raw physical terms, a `Profile` of weights and norms, and a scalar
      `Score(profile)`. `Evaluate` returns the score; `EvaluateMetrics` returns the struct; the
      progress snapshot and the CLI carry the breakdown; the misleading `rms=`/`log=` line goes.
      Done 2026-09-02: ten terms, `Score` is the weighted mean of each term scaled by its norm
      through `x/(1+x)`, over the terms the reference was long enough to measure; the CLI prints
      the terms under every progress line and as a table at the end, the server's and the
      browser worker's snapshots carry `metrics`, and `distance` prints the same table with the
      score under every profile.
- [x] **Partial term.** For each reference partial, the nearest model mode within ±100 cents:
      cents error, level error in dB, log half-life ratio. An unmatched reference partial costs
      its level; an unmatched model mode above a level floor costs its level too, which is what
      makes the fake beat clusters of finding 14 expensive. Done 2026-09-02, as five terms so
      that each can be read: `partial_cents`, `partial_level_db` (with the offset between the
      lists solved out), `partial_decay_octaves`, `partial_missing` and `partial_extra`. The
      model's partials come from its parameters — transposed, scaled by the excitation lowpass
      (`model.ExcitationResponse`), harmonics included — and a mode that dies within a few
      milliseconds is dropped as a click, by the same rule the analysis lists a partial: its
      predicted averaged level within 40 dB of the strongest. Without that rule the default
      preset's three sub-4 ms modes read as three extra partials against the render they
      were fitted to.
- [x] **Noise-aware log-spectral term.** The STFT dB error with a per-bin floor of
      `max(−100 dB, reference noise estimate + 6 dB)`, bins below it in _both_ signals
      contributing nothing, at two resolutions: 8192 points for placement, 2048 for the
      envelope. Done 2026-09-02, with one addition: the floor is also never more than 60 dB
      under the reference's loudest bin, because a synthetic reference has no noise and the
      candidate's −80 dB residue was being scored against numerical silence. The reference's
      frames are transformed once at construction.
- [x] **Envelope term.** Broadband RMS envelope in dB over log-spaced time after alignment, plus
      the tail's decay slope in dB/s. Done 2026-09-02; the slope rule is the analysis package's
      (`analysis.DecaySlopeDBps`, peak down 30 dB or one second), applied to both signals.
- [x] **Waveform term.** The existing aligned RMS, kept as the phase-sensitive term that only a
      polish stage weights highly. Done 2026-09-02, as the residual after the least-squares gain
      over the reference RMS, so it reads from zero to one.
- [x] Gain solved in closed form before every term and reported as `gain_db`; amplitudes become
      relative. Done 2026-09-02: the RMS-ratio gain over the aligned overlap for the spectral
      and envelope terms, and the least-squares waveform gain — `waveform_gain_db`, the number
      that reads −25 to −65 dB on the recording — for the waveform term alone.
- [x] Profiles `balanced` (default), `placement` (partial-heavy, for the global stage) and
      `polish` (waveform-heavy), with norms measured on the shipped presets so that no term of
      `balanced` saturates on either reference — algo-piano's finding 4b, where 56% of its score
      was a constant. Done 2026-09-02; `docs/training.md` has the table the norms were set
      against. `balanced` is the default of `fit`, the server and the browser.
- [x] Test: `recorded-bar.json` scores worse on the partial term than a six-mode preset built
      straight from `analysis.json`. Done 2026-09-02:
      `TestTheRecordedBarScoresWorseOnThePartialTermThanASeedFromTheAnalysis`, at note 60 where
      the shipped preset comes closest, 0.19 against 0.43 on the partial terms and better on
      every one of the five; `PresetFromAnalysis` is the builder, and is 8.3's seed.
- [x] Wire the loader into the objective. Done 2026-09-02: `fit`, `distance`, the server and the
      browser worker read the reference through `analysis.LoadReference`, with `--downmix`,
      `--window`, `--keep-level` and `--analysis` on the command line and `downmix` and
      `window` as server fields. The baseline table was re-taken through it.

### Phase 8.3: A search space a global search can cover

Goal: remove the gauge, the symmetry, the plateau and the wrong bounds.

- [x] Freeze `base_frequency`: write the template's value through and stop encoding it. Done
      2026-09-02: `ParamCodec` carries the template's value and the vector has two scalars;
      `TestCodecWritesTheTemplateBaseFrequencyThrough`. The `base_frequency` bounds key is
      refused with a reason, and the `just refit-default` note about normalising it by hand is
      gone.
- [x] Encode absolute `log10(frequency)` per mode, bounded below by half the fundamental from
      `analysis.json` and above by the lesser of `0.45 · fs` and the top-key ceiling. No more
      `+Inf` plateau. Done 2026-09-02: `ParamBounds.Frequency` in hertz, `[20, 20000]` by
      default, `FrequencyBoundsFor` narrows it from the measurement and converts to the
      authored note; the ceiling is 0.45 · fs at the fitted note, capped by the default box —
      the top of the keyboard is not enforced because a mode above Nyquist there is a wasted
      oscillator, not an invalid one, by the model's own rule. `TestEveryPointOfTheDefaultBox
Decodes` samples two thousand points and both corners; the log round trip at an edge is
      clamped back into the box.
- [x] Enforce mode order by frequency in the codec, to break the permutation symmetry. Done
      2026-09-02 by sorting on encode and decode: the same sound always writes the same list,
      `Pinned` names a mode by its written index, and a population seeded from the ordered seed
      stays in one ordering. The n! preimages remain in the box; the increasing-offset encoding
      that removes them chains every mode to the one below it and was rejected for that.
- [x] Widen `decay_ms` to `[0.5, 2000]`, still log-encoded; `ValidateAuthoredBarParams` already
      guards the transposed ceiling. Done 2026-09-02: `model.DecayMsSearchMin` and
      `DecayMsSearchMax`, and the objective narrows the ceiling to `AuthoredDecayMsMax` for the
      template's note — 743 ms at note 69 — because a search that could write 2000 ms there
      would produce a preset the file refuses; a box entirely above the ceiling is an error.
      The crossover moved from note 76 to note 52 and the tests that pinned it moved with it.
- [x] Take the mode count from `analysis.json` — partials above a level floor — with a
      `--modes N` override, rather than from the template. Done 2026-09-02: `SeedPreset` wraps
      `PresetFromAnalysis`; `--modes 0` seeds every partial the analysis lists (its −40 dB floor
      is the floor), `N` the strongest N, `-1` keeps the template. `fit`, the service (`modes`
      field) and the browser fit all do it, and a seeded fit from a v1 template writes a v2
      preset because v1 holds exactly four modes. The checkpoint records the choice so a resume
      makes it again.
- [x] Seed the initial population from the analysis: modes at the detected partials with their
      measured levels and half-lives, half the population around that seed at σ 0.05 and the rest
      uniform — CircleFit's continuation profile, replacing the one-individual warm start. Done
      2026-09-02: `seedPopulation` in the Mayfly wrapper, for the first round and every warm
      round after it, with its own stream derived from the run's seed; `SeedFraction` and
      `SeedSigma` on `MayflyOptimizer` override the defaults.
- [x] Keep the Chebyshev gains and `input_mix` in the space, and report any dimension that
      finishes on a bound. Done 2026-09-02: `fit` prints the pinned list after the breakdown,
      the service's terminal snapshot and the browser worker's carry `pinned`, and the Optimize
      tab shows it.
- [x] Test: a cold fit from the real box recovers a synthetic six-mode target within 5 cents and
      10% half-life in at least ten of twelve seeds. Done 2026-09-02:
      `TestAColdFitFromTheRealBoxRecoversASixModeTarget`, twelve of twelve within 0.02 cents and
      0.1% half-life, skipped under `-short`. The numbers, and a first 90 s fit of the C5
      recording through the new space, are in `docs/training.md`: `balanced` 0.18 against the
      shipped preset's 0.42, with the pinned report saying the amplitude ceiling and the
      excitation lowpass together stop the model from reaching the recording's high partials.

### Phase 8.4: Engines and schedule

Goal: the engine shape CircleFit measured, plus a polish stage, behind the existing interface.

- [x] Add `github.com/CWBudde/go-cma-es v0.1.0` as `--optimizer cmaes` behind
      `optimizer.Optimizer` (`internal/optimizer/optimizer.go:20`): separable and block
      covariance with one block per mode triple and one for the scalars, `InitialSigma 0.3` in
      the unit cube, Hansen's λ by default (12 at 19 dimensions) with `--cmaes-lambda`,
      `WithInitialMean` from the 8.3 seed, and cold restarts **until the budget is spent** — a
      count cannot express that, and CircleFit records it as its own open structural fix. Done
      2026-09-02 (the encoded dimension is eighteen since 8.3 froze the base frequency, where
      Hansen's λ is twelve): `optimizer.CMAESOptimizer` in `internal/optimizer/cmaes.go`,
      `separable` by default and `block` from `ParamCodec.BlockGroups()`, run 0 from the seed
      through `WithInitialMean` and every later run from a uniform mean, `--cmaes-covariance`,
      `--cmaes-lambda`, `--cmaes-sigma` and `--cmaes-restarts` on the CLI with the same fields
      on the service and the browser fit. **The restart loop is the wrapper's own**: the
      library's IPOP and BIPOP budget evaluations and a fit is bounded by wall-clock time, so
      the wrapper runs cold restarts until the budget is spent, `--cmaes-restarts N` caps the
      count, and `Result.Restarts` and `Progress.Restart` report it.
- [x] Upgrade Mayfly to v0.7.1. Delete `--mayfly-variant auto` and its limiter rather than port
      the `ClassifyProblem` call; let `Config.Seed` replace the hand-resolved seed; record in
      `docs/optimizer.md` that every earlier Mayfly number is incomparable. Done 2026-09-02: the
      `auto` variant and its limiter are gone, `hmma` is a selectable variant, the seed reaches
      the library as `Config.Seed` with `resolved.Seed - round` per round, `cauchy_mutation_rate`
      and `apply_obl_to_global_best` moved from `gsasma` to `hmma` where v0.7.1 reads them, and
      a non-finite objective value is mapped to a finite penalty rather than rejected mid-run.
      `docs/optimizer.md` records that every Mayfly number taken before today was measured under
      v0.6.0 and does not compare.
- [x] Remove Dragonfly from consideration, with the evidence, in `docs/training.md`. Done
      2026-09-02: "Engines after 8.4" quotes the sibling numbers — zero of twelve blocks in
      every arm, t = −16.8, the best of 576 restarts worse than every baseline block
      (`docs/dragonfly-poc-report.md` in MayFlyCircleFit). It is not a candidate in 8.5's
      designs.
- [x] A `--polish` stage: Nelder-Mead (the existing `SimpleOptimizer`) or CMA-ES at σ 0.02 from
      the incumbent under the `polish` profile, accepting only improvements. `simple` stops being
      a standalone default; the CLI default becomes `cmaes` with restarts until the budget. Done
      2026-09-02: `--polish none|nelder-mead|cmaes` (default `none`) with `--polish-iterations`
      200, `--polish-budget` and `--polish-sigma` 0.02, running under the `polish` profile over
      the primary objective's own codec and bounds. **Acceptance is judged under the primary
      metric, not under `polish`**: the polished vector replaces the incumbent only when its
      primary cost is strictly lower, because every report and every checkpoint scores under the
      metric the fit was started with. The checkpoint keeps the pre-polish result. `--optimizer`
      now defaults to `cmaes`, and **`simple` stays selectable** as the standalone local backend.
- [x] Defaults that change: seed 0 (choose and report), a `--workers` flag whose width is
      recorded in the checkpoint, `--report-every` decoupled from the checkpoint cadence, and
      `--mayfly-stagnation 0` writable. Done 2026-09-02: one `--seed` (default 0, chosen and
      reported) feeds Mayfly, CMA-ES and the polish stage, with `--mayfly-seed` and `--cmaes-seed`
      kept as deprecated aliases and combining one with `--seed` refused; `--workers` is recorded
      as `OptimizerState.Workers` and reused by `--resume`; `--checkpoint-interval` counts
      optimizer iterations rather than progress reports; `--mayfly-stagnation 0` is writable and
      switches the rule off.
- [x] Test: parallel equals serial to the bit per engine at a fixed seed and width; a
      restart-until-budget run spends at least 95% of its cap. Done 2026-09-02: each engine
      reproduces its result at a fixed seed and worker width, and the restart-until-budget test
      measured 95.2% of the cap spent.

### Phase 8.5: A campaign harness

Goal: the instrument CircleFit's `scripts/cmaes-measurement` is, for this objective.

- [x] `cmd/glockenspiel-campaign` with registered designs in code, not flags: arms × paired seed
      blocks, evaluation-matched by construction, one identified binary per campaign, block-major
      order, `plan | run | collect | analyze`, and a manifest that refuses to be overwritten.
      Done 2026-09-02: designs are Go values in `internal/campaign/designs.go` and the only
      design-shaping flag is `--winner`, for `seed-hunt`, whose arms are the winner of
      `engine-shape` and cannot be known until 8.6. Evaluation matching is cap plus trace
      scoring: every arm gets the same `MaxEvaluations`, a backend may overrun by at most one
      generation, and `collect` scores each job at the best trace cost at or below the cap, with
      the finished score kept in its own column. Jobs run in-process and sequentially at a worker
      width the manifest pins, which 8.4 showed costs nothing. The manifest is written `O_EXCL`
      and carries the design, its hash, the binary's path and hash, the build identity and the
      reference's hash; `run` refuses a different binary or reference and there is no override.
      That is the fix for CircleFit's recorded registration discrepancy
      (`cmaes-budget-split-report.md` in MayFlyCircleFit), where a contrast was changed after
      partial results were visible. `just campaign-build|plan|run|collect|analyze|smoke` drive
      it, and [docs/campaign.md](docs/campaign.md) is the document.
- [x] Every job gets its own run directory: `config.json` with the full echo, `analysis.json`,
      `trace.jsonl` (per-iteration best cost, per-term breakdown, restart index, evaluations),
      `checkpoint.json`, `preset.json` carrying provenance (reference hash, profile, seed,
      engine, version, score, terms) and `render.wav`. Done 2026-09-02 in the new
      `internal/fitrun` package, which also writes `result.json` and `log.txt`. A directory whose
      summary reports `context_canceled` is not a finished job: `run` clears and repeats it and
      `collect` refuses it unless `--partial`.
- [x] `analyze` rebuilds the table from the CSV alone: mean, sd, blocks won, paired t, Holm over
      a registered contrast family, and a best-of table, which is the rare-basin instrument.
      Done 2026-09-02: the CSV header is a contract and a file whose header differs is refused.
      A descriptive design gets no inferential statistics. The rules carried from CircleFit are
      recorded in the document: contrast families are registered rather than derived, a design is
      frozen at plan time, a mean-versus-win-count mismatch is not acted on, cap-matched is not
      spend-matched, and absence of evidence is stated as such.
- [x] First design, `engine-shape`: `mayfly-single`, `mayfly-r16`, `sep-cmaes-r`,
      `blk-cmaes-r`, `sep-cmaes-ipop` at one budget, twelve blocks, on the C5 recording under
      `balanced`; primary contrast `blk-cmaes-r` against `mayfly-r16`. Second design,
      `seed-hunt`: best-of over 48 seeds at two λ for the winner. Done 2026-09-02 and planned but
      not run; 8.6 runs them. The budget is 24,000 evaluations per job, because the 8.4 smoke run
      spent about 24,000 evaluations in 60 s on twelve threads, so sixty jobs are about an hour.
      "Restart until the budget is spent" is a per-run cap of 4,800 with no restart limit on the
      wrapper's own loop, and IPOP is `LambdaGrowth 2` on that same loop; `mayfly-r16` is one warm
      round plus fifteen cold restarts, and the mayfly arms take an iteration cap of the budget
      over the measured 43.05 evaluations an iteration costs, so annealing sees a realistic
      length. `seed-hunt` is descriptive and takes `--winner` at plan time. Seed bases 120,000,
      121,000 and 122,000 are disjoint and pinned by a test. Only the four-job `smoke` design has
      been run, as a wiring check, and its output is in [docs/training.md](docs/training.md).

### Phase 8.6: Run it, decide, ship

- [x] Run both designs; write `docs/training.md` with the tables, the chosen default shape and
      the pins line. Done 2026-09-03. `engine-shape` ran from commit `4389279`, sixty jobs in
      about an hour, and every row stopped on `max_evaluations` having spent its budget. The
      answer is the opposite of the design's hypothesis: block-covariance CMA-ES lost the
      registered primary contrast in twelve blocks of twelve (−0.062, t = −10.68, p < 0.0001
      after Holm) and separable CMA-ES lost too (−0.040, t = −4.00, p = 0.002, 2/12). Nothing
      beat `mayfly-r16`. **`seed-hunt` was not run**, by ruling: it refines a _winning CMA-ES
      arm_ by construction — `SeedHunt` refuses a non-cmaes arm — and there is no CMA-ES winner,
      so its precondition is unmet. Part B of this phase already lists λ as a null not to
      re-derive. The design stays registered and `--winner` still takes an arm, so it runs the
      day a CMA-ES arm wins something.
- [x] Two defects the campaign found before it could be read, both fixed and both invalidating
      every restarting figure taken before them. Round and restart random streams were derived
      from the run's seed arithmetically (`seed − k`, `seed + k`, `seed − k − 1`), which keeps one
      run's streams apart but not two runs': a campaign block's seed is `SeedBase + block`, so
      block _b_'s round _k_ was block _b+1_'s round _k∓1_ and a sixteen-round arm's twelve blocks
      shared fourteen of their fifteen restarts. Two blocks of `mayfly-r16` wrote a bit-identical
      preset, which is how it surfaced. `internal/optimizer/randomstream.go` now mixes the seed
      with a family label; the arms that take their result from index zero are bit-identical
      across the fix, which is the check that it is surgical. Separately, `--time-budget` had to
      be positive, so no CLI fit could ever be bounded by `--max-evals` and no hand-run fit could
      reproduce a campaign arm; zero now means "no clock".
- [x] Re-fit both shipped presets by recorded recipe (`just refit-default`, `just refit-recorded`)
      and ship the file the fit wrote. Done 2026-09-03; **neither file shipped**, for two
      different reasons, both recorded in [docs/training.md](docs/training.md). Both recipes run
      the promoted shape at 120,000 evaluations with the clock off, so a rerun at a fixed seed
      reproduces the fit rather than approximating it, and they build `bin/glockenspiel` rather
      than using `go run`, which leaves the provenance revision `unknown`. `refit-recorded` fits
      at note 72, the recording's own pitch, so the ×1.667 hand retune has nothing left to do,
      and it beats the shipped preset by 62% (0.204 against 0.537). It renders 24.5 dB quieter,
      and no post-step here can fix that: amplitudes are bounded at ±2 with one already pinned
      at the bound, `input_mix` is worth at most 4.9 dB more, and the schema has no output gain,
      while the loader peak-normalises the reference by +27.6 dB and the objective divides the
      difference out (`gain +26.95 dB` in the fit's own provenance). `default.json` was not
      refitted at all: `legacy_synth_a4.wav` holds one partial, so a fit against it writes a
      one-mode preset — correct, and useless as the instrument's general-purpose sound.
- [x] Promotion rule, from algo-piano: a default changes only when it wins the registered
      contrast and regresses no term of `balanced` on either reference. **Amended 2026-09-03 with
      a materiality threshold**, because as written the rule cannot be applied: two fits always
      differ on some term, so any candidate regresses something and no default could ever change.
      A term now counts as regressed when the paired difference is both statistically real and
      larger than one percent of that term's norm in `optimizer.DefaultNorms`. Under it,
      `mayfly-r16` is promoted to the CLI default: it wins the registered contrast on the C5
      recording, and its one unanimous regression on the A4 render — `decay_slope_dbps` worse in
      eight paired seeds of eight — is 0.017 dB/s against a norm of 10, about 0.0002 of score.
      The threshold is the rule's own judgement made explicit rather than left to whoever reads
      the table.

### Phase 8.7: Serve and the Optimize tab

Goal: the UI a campaign needs — history, provenance, comparison.

- [x] Run history: each job gets a run directory under `--work-dir`; `GET /api/fit/jobs`,
      `GET /api/fit/jobs/{id}` with the full config echo and metrics, plus `/preset`, `/audio`
      and `/trace` per job; a sequential queue instead of a 409; results survive a restart.
      Done 2026-09-04. The server stopped running its own fit loop: it delegates to
      `internal/fitrun.Run`, so a served fit leaves the same run directory a campaign job does
      and the three parallel pipelines 8.5 deferred become two. `fitrun` grew the hooks that
      needed (`OnProgress`, `OnResolve`, client bounds, an alignment override) and now writes
      `reference.wav`, the cut and normalised signal the objective actually scored, which is what
      makes an honest A/B possible at all. Three request knobs had no home in `Spec` and were
      added rather than dropped. The 409 is gone: a second start is queued, and one worker takes
      jobs in order. Recovery reads the same rule `internal/campaign/status.go` already used, so
      a job that died with its process comes back failed rather than running. **One consequence
      to know:** after a restart over a populated work dir, `GET /api/fit` answers with a
      finished fit from the previous session instead of 404, which is what "results survive a
      restart" means for the mount-time read.
- [x] The snapshot gains the full request echo, the per-term metrics, evaluations per second,
      the budget fraction, the restart and epoch index, `Converged` and the gain applied.
      Done 2026-09-04. The gain needed no new field: `Metrics.GainDB` already carried it, and it
      is now pinned to arrive mid-run rather than only at the end. The epoch index did not exist
      before this phase — the Mayfly tracker never set `Progress.Restart`, so a round index was
      invisible to every front end. The snapshot also carries the active profile's per-term
      weights and norms, so the UI's bars cannot drift from the score by re-deriving
      `optimizer.DefaultNorms` in TypeScript. Restored jobs echo what `config.json` holds and
      **omit** the mayfly form fields they cannot recover rather than presenting a default as
      though it were what the run used. `optimizer.ComputeSpectrogram` exports the objective's
      own STFT, noise-aware floor and all, so `GET /api/fit/jobs/{id}/compare` paints both
      signals from the code that scored them: one time axis, and both clamped to the reference's
      floor exactly as `spectrogram.errorDB` does. Painting each side against its own floor was
      the defect that made the render appear to hold content the score counted as nothing.
- [x] A results view with the reference beside the fit — waveform and spectrogram from the same
      STFT code — A/B audition of both, a parameter table with bound-pinned values highlighted,
      per-term bars, and a run list with a compare picker for cost curves. Done 2026-09-04. Both
      sides share one scale throughout: the amplitude range is scanned across both waveforms, the
      spectrogram ceiling is the louder side's peak, and the floor and the time span arrive from
      the server rather than being derived per side, because a per-side range draws a better
      picture that disagrees with the objective. The bars are computed only from the weights and
      norms the snapshot carries, reproducing `Metrics.Contributions` including its renormalisation
      over measured terms, so nothing about the score is retyped in TypeScript. Pins are read from
      `PinnedDimension` by name rather than by re-comparing values against bounds, and are marked
      three ways that do not rely on colour. The parameter table reconstructs the codec's own
      dimension names against the preset's frequency-sorted mode order, so a row is one mode.
      Selecting a historical run once played the active run's audio instead; audition now
      addresses the per-job endpoints.
- [x] Collapse the three limit tables and three optimizer selectors into one generated source,
      and keep one duration parser per language. Done 2026-09-04. `internal/fitschema` holds the
      one table and `cmd/gen-fit-schema` writes `web/src/api/fitSchema.generated.ts`, with a
      `--check` mode in CI and the justfile beside the other generators; `types.ts` re-exports it
      so no import site moved. Collapsing the copies is what found the divergence it exists to
      prevent: the browser fit accepted five requests the server refuses, because it never
      checked the upper bounds on `cmaes-lambda`, `cmaes-restarts`, `mayfly-nc` and
      `mayfly-nc-ratio`, and never bounded `mayfly-target-cost` at all. Those are now one table's
      job. Only **two** optimizer selectors were ever left to collapse, not three: the server's
      became dead weight when item 1 moved it onto `fitrun`, and `internal/browserfit` cannot
      import `internal/fitrun` without pulling the filesystem into WASM, so the second copy
      stays and is recorded here rather than abstracted away.
- [x] Playwright runs one real short fit through `serve` and asserts that the cost curve falls
      and the comparison view shows both signals. Done 2026-09-04. `playwright.config.ts` runs a
      second `webServer`, guarded so that someone without the Go toolchain still runs the existing
      tests rather than watching them fail, and the new spec asserts the last best cost is below
      the first and finds both signals by their accessible labels rather than by pixels. Running a
      real fit is what found four defects in the run list that no mocked test had exercised,
      `aria-pressed` on a `role="listitem"` among them. **The two Optimize screenshots are still
      stale.** They were already stale before this item, because the run list landed without
      regenerating them, and this machine lacks the faces the baselines assume, so regenerating
      here would commit font-substituted metrics as the new truth. They need a machine with the
      repo's fonts. To tell a real regression from the environment, run the suite against the
      previous commit in a throwaway worktree and compare the failure lists.
- [x] The browser fit keeps its contract and is documented as the single-threaded demonstration
      path; it does not run campaigns. Done 2026-09-04. Its contract held through every task: it
      has no filesystem, no job list, no trace endpoint and no reference, so the run list, the
      compare picker and the A/B control are absent there rather than broken, and the term bars
      fall back to raw values when a snapshot carries no profile. What it did gain is the
      validation it was missing: collapsing the limit tables in item 4 revealed it had been
      accepting five requests the server refuses.

### Phase 8.8: CLI runs visible in the Optimize tab

**Done.** A fit started from the terminal, and a campaign of dozens of them, is watchable live in
the browser, and the search still runs where it was started.

The gap was one caller, not a missing feature. `internal/fitrun`'s own package comment says it
"is the library form of what `glockenspiel fit` does", and both the campaign runner and — since
8.7 — the server delegated to it. The fit command never did, so it wrote a layout that predated
the run directory and `internal/server/restore.go` had nothing to read. The three parallel
pipelines the phase started with are now one.

- [x] `glockenspiel fit` runs through `fitrun.Run`. `--work-dir` is the run directory and
      carries the contract's nine files. `fit.go` went from 1447 lines to 1125 by deleting what
      the library already did: the provenance block, the polish stage, the checkpoint cadence,
      the initial-point clamp, the render and the second preset save. Four flags the command had
      and the spec did not — `--analysis`, `--resume`, and the two cadences whose zero means
      something different on a command line than in a struct — became `Spec.Analysis`,
      `Spec.Resume` and `CheckpointNever`, rather than a second code path.
- [x] `--resume` still finds a checkpoint. The run directory holds one `checkpoint.json`,
      overwritten as the search improves, so it is by construction the newest state;
      `optimizer.FindLatestCheckpoint` prefers it and falls back to the old `checkpoint_*.json`
      glob, so a work directory left by an older build still resumes. `fitted_output.wav` is
      gone, because it was a second copy of `render.wav` that nothing read.
- [x] The server follows a live run directory. The startup scan is a scan every second: a
      directory holding a `config.json` and no `result.json` is a **running** job whose progress
      is read by tailing `trace.jsonl` from a remembered byte offset, and it becomes terminal
      with the summary `result.json` records. The tail stops at the last newline, so a
      half-written line is never parsed — pinned by a test that appends a headless fragment and
      asserts it stays unread until it is completed.
- [x] `restoreFreshnessWindow` is retired. The five-minute guess existed only because the server
      could not tell "still running" from "died with its process"; a followed directory answers
      that by observation. The trade runs the other way now and is worth naming: a run that
      really did die stays `running` until something writes its `result.json`, where before a
      live run of another process was called `failed`. The old rule was wrong about the case
      that happens on purpose; the new one is wrong about the case that happens by accident.
- [x] A followed run is labelled as one the server did not start. It carries `"followed": true`
      in its snapshot and its list row, `POST /api/fit/cancel` refuses it with a 409, and the
      Optimize tab shows Cancel **disabled with the reason** rather than hidden — a missing
      button is a question, a disabled one with a sentence beside it is an answer.
- [x] A followed run is a first-class live job, not a row. `useTracedFit` polls the job and its
      trace, so the cost curve and the term bars update from the tail exactly as a served fit's
      do. The one-shot read and the fake static events a selected historical row used to get are
      gone.
- [x] The active job cannot be yanked away mid-session. `GET /api/fit` answers about the most
      recently _recorded_ job, so an adopted CLI run becomes that — which would have repainted
      somebody's own running fit with a stranger's cost under its name. `useFitEvents` now drops
      a snapshot whose `jobId` is not the watched one and closes the `EventSource`, which also
      covers an ordinary reconnect landing on the same answer. At mount there is nothing to take
      away, so the page still adopts whatever the server calls active.
- [x] The run list keeps reading after it settles. A terminal history used to be final and stop
      polling; the server now adopts new directories on its own timer, so a fit started in a
      terminal has to appear without a reload. Three seconds while a row is unsettled, ten
      once none is.
- [x] `docs/serve.md` records the arrangement: the two shapes a directory can have and what each
      now means, the retired rule kept as history rather than deleted, `followed` in both the
      snapshot and the cancel sections, and the shared-work-dir warning rewritten. Pointing a
      server at a campaign tree is now something that works rather than something that survives;
      two _servers_ over one directory is still the case to avoid, and for a different reason —
      both would accept starts into the same tree.

Two things this pass found that were not what the checkbox set out to fix:

- `serve-fit.spec.ts` recognised the fallback listener by an idle 404 on `/api/fit`, which a
  followed run permanently defeats: the endpoint answers for an adopted directory whether or not
  this server ever ran a fit. It probes for a real `jobs` array now. A test that identifies a
  server by what it has _not_ got is only correct while nothing else can supply it.
- The Playwright work directory was a fresh `mktemp` per start, and twenty-five copies of it at
  15 MB each had accumulated in `/tmp`. It is one reused directory now. This is also the real
  cause of the `ERR_INSUFFICIENT_RESOURCES` failure reported at the end of the previous session
  as a Vite module-burst limit: `/tmp` is a 7.3 G tmpfs shared with other projects and it was
  full, so Chromium could not allocate. It is still at 96%.

### Phase 8.9: An output gain, so a fit can be shipped

**Done.** A preset carries the level it renders at, and a fit writes it.

`BarParams.OutputGainDB` (`output_gain_db`, ±60 dB, omitted at zero) is a **v3** field. Every
preset written before it existed renders bit-identically.

It started a schema version rather than extending v2, which is where review put it and where the
first draft had it. Rejecting it in a v1 document protects this repository's own loader and
nothing else: a v2 reader built before the field existed accepts the document, ignores the key it
does not know, validates the resulting zero, and renders at unity — up to 60 dB from the level
the preset was calibrated to, silently. `model/` is tagged `v0.1.0` and a second module builds
against it, so such readers are not hypothetical. The rule the ladder follows from here: a field
a reader can ignore without changing the sound may extend a version, and a field it cannot must
start a new one.

- [x] The gain is solved, not searched. The objective already computes the level in closed form
      and subtracts it from every term, so a searched gain would have no gradient — it is a gauge
      dimension of exactly the kind `base_frequency` is excluded for (`params.go:11-19`). Nothing
      entered the search space: `codec.Dimension()` is unchanged.
- [x] It costs nothing per sample. `model.Bar` folds it into coefficients it already computes
      once per retune — the mode amplitudes where the chain after the bank is linear, the
      shaper's own gains where the shaper sits after the bank, the dry mix either way. The three
      cases are needed because the shaper is a nonlinearity in the middle of the chain, and
      `TestOutputGainIsExactlyAScalarOnTheOutput` pins the property the fold rests on: a bar at
      gain G renders G times what the same bar renders at unity, in every configuration.
- [x] The codec carries it (`ParamCodec.outputGainDB`). It is not searched, but a decoded
      candidate has to describe the same sound as its template; while it did not,
      `glockenspiel distance` reported a fitted preset's render peak 28 dB below where the preset
      actually plays, and said nothing, because the objective divides the level out again.
- [x] Both fit write paths measure and write it, at −3 dBFS. The target is not a new number: it
      is the rule the shipped presets already follow, asserted by
      `TestBuiltinPresetsRenderNearMinusThreeDBFS`, and the constant now has one home in
      `internal/synth/level.go` that both the fit and that assertion read. The calibration
      conditions are fixed at 44.1 kHz and velocity 100 rather than taken from the fit, because
      the peak is rate-dependent — 48 kHz runs about 0.7 dB hotter, three times the tolerance —
      so solving at the fit's own rate would have produced presets that failed the promotion rule
      for a reason that has nothing to do with the preset.
- [x] The version ladder carries it. `VersionV3` is the version new presets are written in;
      `rejectNewerFields` reads the raw JSON so that an explicit `"output_gain_db": 0` in a v2
      document is caught too, which the decoded value cannot distinguish from an omitted field;
      and `Upgrade` restamps a v1 or v2 document without changing what it renders.
      `TestV2RejectsTheOutputGain` pins the reason the version exists. A preset that needs no
      gain is left at whatever version it arrived in, so nothing that could be read before
      becomes unreadable.
- [x] Every path that hands a preset to a listener calibrates it, the browser included. The
      browser fitter decodes an optimizer point straight into a downloadable and auditionable
      preset, so for a while it was the one path that shipped the drifted level — the exact
      failure the phase exists to fix. `browserfit.Calibrate` sits at the single choke point both
      the download and the audition pass through, rather than on the progress path, which decodes
      a preset on every optimizer report.
- [x] The blocked artifact is unblocked. `out/morphagene/seed-4/c6.json` carries
      `output_gain_db +30.25` and renders at exactly −3.000 dBFS at its own note; its `gain_db`
      against the Morphagene c6 reference falls from +37.10 to +6.85 and its render peak from
      −36.07 to −5.82 dBFS.

Two things this pass found that were not what the phase set out to fix, both recorded rather than
patched:

- The waveform term cannot see a polarity flip: `cross` enters the residual squared, so negating
  a candidate leaves the score exactly where it was. The projection is now clamped at zero, but
  measured against every fixture the clamp moves no number — `BestLag` maximises the _signed_
  correlation and so never hands the residual a negative `cross`. It is a guard against a latent
  trap, not a live fix, and what actually punishes a flip is the alignment: above about 340 Hz the
  aligner slides the inversion away and it costs 0.28, below that it cannot and it costs 0.99.
  Making the first case expensive means scoring the lag itself, which is a different change.
- The spectral term's absolute magnitude floor rewarded a quiet render. It was the reason the
  level ridge sloped rather than lay flat, and it was the larger of the two. Fixed in 8.10.

---

### Phase 8.10: A floor that means the same thing on both sides

**Done.** The objective is gain-invariant, which it claimed to be and was not.

Every dB term sits on an absolute floor -- `spectralMagnitudeFloor` at −100 dBFS under the
spectral and onset terms, `envelopeFloorDBFS` under the envelope -- and the floor was applied to
the candidate in its own scale, with the solved gain added only afterwards. Where the floor sits
relative to a signal therefore depended on how loud that signal happened to be. A render far
below the reference had its low bins flattened onto the floor, and adding the gain afterwards
lifted that flat plateau up near the reference's own floor, where it scored far better than the
true spectrum would.

The bias only ever ran one way, so this was not noise: **it paid to be quiet.** That is the
missing half of 8.9's diagnosis, which called level a free ridge. It is not free and it is not
flat — it slopes, and the eight Morphagene seeds were all partly descending it.

- [x] The gain is applied before the floor rather than after it, in all three terms. It folds
      into `magScale`, which every bin is already multiplied by, so the fix costs nothing.
- [x] `TestScoreDoesNotDependOnTheCandidatesLevel` pins the property: a preset differing only in
      `output_gain_db` scores identically on every term across ±40 dB, to 1e-3 — the residual is
      float32 render rounding, four orders of magnitude below the 4.65 dB artifact it replaces.
      Reintroducing the old arithmetic makes a −20 dB candidate score `spectral_fine_db` 15.66
      where it should score 0, so the test earns its place.
- [x] The legacy `spectralErrorWithGain` is fixed the same way, since it had the same defect.

Measured on the seed-4 Morphagene fit, at the level it was fitted at:

| term               | before | after  |
| ------------------ | ------ | ------ |
| `spectral_fine_db` | 0.506  | 5.160  |
| `onset_db`         | 11.000 | 13.208 |
| `balanced`         | 0.2223 | 0.2589 |

**What moves, measured rather than assumed.** Only candidates far from their reference's level
move at all, because only they reach the floor:

- Both shipped presets are unchanged to every decimal, on every term and all three profiles.
  They render within 0.08 dB and 3.89 dB of their references, so the floor never bit.
- The eight Morphagene seeds, which all drifted about 37 dB down, move `balanced` by 0.037 to
  0.039 and `spectral_fine_db` by a factor of ten.

The second row is the one that matters, and not only because the numbers are larger. Before the
fix `spectral_fine_db` read 0.506, 0.506, 0.506, 0.506, 0.506, 0.509, 0.507 and 0.512 across the
eight seeds: the term was reporting the height of the floor plateau, which is the same whatever
the candidate does, so it was not discriminating between candidates at all. A twelfth of the
balanced weight was being spent on a constant.

**The ranking reorders.** Best of the eight was seed-6 before and is seed-4 after; seed-8 and
seed-6 swap as well. So numbers taken under the old arithmetic cannot be compared with numbers
taken after it, and a conclusion drawn from them about which arm or seed won may not survive
re-measurement. `docs/training.md`'s tables and the `engine-shape` campaign results are in that
position. Re-taking them is a campaign, not an edit.

**Re-taken 2026-09-05, and the warning was justified.** The `engine-shape` ranking inverted:
`mayfly-r16` fell from first of five to fourth, and `mayfly-single` now beats it on the recording
in eleven blocks of twelve. The default survives only because the promotion rule's second clause
refuses the challenger on the A4 reference. The clause about "the score in every committed
`preset.json` provenance block" was wrong and is struck — no tracked preset carries a provenance
block at all, and both shipped presets are hand-authored and quote no score. The 2026-09-05
sections of [docs/training.md](docs/training.md) hold all of it, and record that 8.10 was not the
only thing that had moved: the objective had also gained an eleventh term, `onset_db`, and been
reweighted for it, which is what actually moved the composite table's scores.

---

## Phase 9: Presets from the sample packs

**In progress.** The code is written and gated; the measurements are not taken. Branch
`phase-9-sample-packs`, PR #53, deliberately a draft until the numbers exist.

`testdata/reference/packs/` has held four Freesound packs -- 56 recorded notes -- since phase 8,
referenced by no Go code. The ask was threefold: fit each sample, find what depends on the MIDI
note, generalise into one preset that minimises the cost across the keyboard.

**Measured before any code was written**, read-only, with this repo's own `analyze`:

| finding                                                               | number                                                            |
| --------------------------------------------------------------------- | ----------------------------------------------------------------- |
| `hollandm` is the only chromatic run and the richest                  | 5.2 partials/note, 20 consecutive semitones                       |
| morphagene is effectively single-mode -- second partial 39-67 dB down | 1.1 partials/note                                                 |
| the second partial sits at the free-free bar ratio, near-universally  | 2.74x f0 in 18 of 20 bars                                         |
| the model's decay law (exponent 1) is wrong, and per instrument       | hollandm -1.22, morphagene -0.55, mooncube +0.04, jamieblam +0.24 |
| but on hollandm a fitted exponent buys almost nothing                 | `partial_decay_octaves` 0.410 -> 0.398                            |
| because the slope was never the problem -- bar-to-bar scatter is      | 0.33 octaves, 0.66x the term's norm                               |

So the fitter is good enough and the model's expressiveness across the keyboard is the binding
constraint -- and not where the ask assumed. Above the second partial the structure is
idiosyncratic: the 5.3x partial weakens toward the top and a different mode near 4.5-4.7x takes
over. Nothing smooth captures that. The last two rows are why the decay exponent is treated here
as a hypothesis to be measured rather than a feature to be added.

### Phase 9.0: The keyboard is a glockenspiel

**Done.** `KeyboardFirstNote`/`LastNote` were C2-C7, which is not a glockenspiel, and two
blockers fell out of that one defect:

- `ValidateAuthoredBarParams` transposes down to the bottom key and enforces
  `DecayMsValidationMax`. At MIDI 36 -- four octaves below anything a glockenspiel plays -- an
  808 ms hollandm bar becomes 13.7 s, and 13 of 20 bars could not be authored at all.
- Seven of twenty notes sat above the top key, so a keyboard sweep never reached them.

Now G5-C8, MIDI 79-108. That dissolved both, and made the planned `DecayMsValidationMax` raise
unnecessary: the worst bar needs about 1.1 s at the new bottom key. Worth recording, because
raising a validation ceiling to admit data is the move you want to not have made.

What the move did break was the frequency ceiling, which `FrequencyMaxHz` had to follow to
200000 -- consistent with its own stated rationale, that a mode above Nyquist is a wasted
oscillator rather than an invalid one. `DecayMsMin` fell to 0.01 as the exact mirror: the top key
is now 39 semitones above note 69, and `default.json`'s 0.5605 ms mode reaches 0.0589 ms there.
Neither could be fixed by re-authoring -- `max_mode x 2^((top-note)/12)` and
`min_decay / 2^((top-note)/12)` are invariant under `TransposeToNote`, so the constants are the
only lever.

One prediction made while planning was wrong and is worth keeping: the casualty was expected to
be `FrequencyMaxHz`, and the tests said `DecayMsMin`. Both had to move.

### Phase 9.1: Fit each note

**Done, run pending.** A `pack` command group in `glockenspiel-campaign` --
`plan`, `run`, `collect`, `fit-joint`, `score`, `regress` -- reusing the campaign's provenance
discipline (`O_EXCL` manifest, binary and reference SHA-256s pinned, a refusal to continue under
a different build) but not its CSV: `campaign.Row` has no note column and `checkHeader` requires
the contract exactly, so extending it would make `campaign analyze` refuse the five archived
results files in `docs/data/`. Notes are not arms.

Two traps, both found rather than avoided:

- **The note index comes from the measured fundamental, never the filename.** Freesound strips
  `#` from an upload's name, so ten of hollandm's files arrived sharing a name with their own
  sharp. A harness that trusted the name would have fitted half the pack a semitone from the
  recording it was scoring against -- silently, with plausible scores. `pack.ResolveNote`
  refuses a mismatch and records the cents residual, which for hollandm is -1 to +5 cents.
- **Each per-note fit is authored at its own note.** `PresetFromAnalysis` authors at the
  _template's_ note, so the c6 fit first wrote `note: 69` with a fundamental of 439.7 Hz instead
  of 1046.2. The preset renders correctly -- transposition puts it back -- so nothing would have
  complained, but `pack-modes.csv` would have carried note-69 equivalents and the `log2(decay)`
  regression would have come out a whole exponent off, which is the one number this phase exists
  to measure. Found by reading a finished run directory; the first pack run was killed 20 minutes
  in, `Spec.AuthoredNote` added, and the run restarted.

### Phase 9.3: The joint fit

**Done, run pending.** `ObjectiveFunction` was extended rather than forked -- `optimizer.Polish`
takes a concrete `*ObjectiveFunction` and `fitrun.preparation` holds one, so a parallel type
would have forked four packages. N=1 is the degenerate case and the four public constructors keep
their signatures.

The aggregate is **the mean of the per-note `Score`s, not the mean of the terms scored once**.
Averaging before `saturate` would let one hopeless note dominate a term, defeating the property
the saturation exists for, and would launder a term measured at 3 of 20 notes into full profile
weight. Scoring per note keeps each note's weight renormalisation inside that note.

**A joint fit had been describing itself as a single-note one.** A twenty-note run wrote its
_lowest_ note's file into `config.json`'s `reference` block and into the preset's provenance, and
filled `result.json`'s `terms` from that one note -- three well-formed records of a search that
never happened. Now `reference` is written only for N=1 and `references` for a joint fit,
provenance carries every file with the score it reached, and `terms` is explicitly NaN beside a
`note_terms` block, because the score is the mean of the per-note scores and no set of terms
reproduces it. `EvaluateMetrics` refuses a multi-note objective outright rather than returning a
twentieth of the fit.

Those per-note scores are not bookkeeping: a preset whose mean is good because it fits three
notes and abandons seventeen is a different object from one that fits all twenty adequately, and
the mean alone cannot tell them apart.

### Phase 9.4: The decay key-tracking exponent, at schema v4

**Done, unearned.** `BarParams.DecayKeytrack` is a `*float64` because the neutral value is 1.0: a
bare `float64`'s zero would mean an exponent of 0, which is legal and measured (mooncube is near
it), so every existing struct literal here and in the external VST3 module would have switched
laws with no compile error. `nil` means 1.0, and every v1/v2/v3 document renders bit-identically
to before.

It is searched, never carried, and only by a fit that can see it. At one note the exponent trades
off exactly against every `DecayMs` -- any value is absorbed by scaling the decays -- so it is a
gauge freedom of the kind `BaseFrequency` is already excluded for. The objective refuses it below
two references or an octave of span, plain `fit` has no flag for it, and only
`pack fit-joint --keytrack` can ask.

Schema v4 by the rule 8.9 wrote: a v3 reader accepts the document, ignores the key and divides by
the full ratio, so it renders correctly at exactly one note and diverges monotonically from
there. **But the version is not yet earned.** The registered rule needs all three of a
Holm-corrected `p < 0.05` on the paired ablation, a median improvement clearing a materiality
threshold, and a beta consistent across blocks. The third is the one likely to fail and the
0.410 -> 0.398 above says it may. If it fails, the preset ships at v3 and this section says so.

Three latent bugs were found on the way, each of which would have shipped:

- `preset.go` read `version != VersionV3 && OutputGainDB != nil` to reject a gain on an older
  document. The moment `CurrentVersion` became v4 that rejected **every calibrated preset a fit
  writes**. Replaced with an `OlderThan(version, introducedIn)` ladder helper;
  `synth.ApplyOutputGain` spelled the same comparison and was also pushing v3 documents to the
  current version for no reason.
- The raw version probes were `*json.RawMessage`, which resolves an explicit `null` to a nil
  pointer before `RawMessage`'s Unmarshaler runs, so `"decay_keytrack": null` slipped the ladder.
  Both are bare `json.RawMessage` now, closing the same hole for `output_gain_db`.
- `ObjectiveConfig.DecayKeytrackBounds` documented "the zero value takes the model's full range"
  and did not implement it. A zero `Range` is `Min = Max = 0`, which would have clamped the
  exponent to exactly 0 -- a legal, measurable value, and so one that would have read as a search
  result rather than an unset field.

### Phase 9.5: Verification and write-up

**Not started.** Blocked on the 20-note pack run. What it owes:

- `pack collect` and `pack regress`: per mode index, `log2(decay_ms)` and `log2(freq/f0)` on MIDI
  note, from the **fitted** decays rather than the analyzer half-lives, because only the fitted
  ones live in the model's coordinates.
- The joint fit itself, and the preset in `out/`. Promotion to `assets/presets/` stays a separate
  decision, as it does for the recorded-bar refit.
- **The transposition matrix.** Each per-note preset transposed to each note, gain solved, scored
  under the same aggregate objective: 20x20, plus the joint preset as a 21st row. The joint row's
  mean must beat every single-note row's, and **`mean(joint row) - mean(diagonal)` is the price
  of one preset covering twenty notes** -- the actual deliverable of the whole exercise.
- The beta ablation, >=12 paired blocks, under the three-part rule above. Report whether beta
  pinned on its box edge: if it sits at 1.0 in most blocks the box is binding and the honest
  conclusion is "beta cannot be evaluated here", not "beta does not help".
- The write-up in `docs/training.md` with the date, the arithmetic each table was taken under,
  the design hash, the binary SHA-256 and every reference SHA-256.

### What this phase will not do, stated up front

- **It cannot learn a loudness curve.** Each note's level is solved in closed form and divided
  out, so the joint objective is blind to relative level across notes. Correct for this pack --
  five of its files touch full scale -- but someone will later expect the preset to have learned
  one, and it cannot have.
- **It cannot capture bar-to-bar scatter.** 0.33 octaves of decay and the idiosyncratic third
  mode are properties of twenty distinct pieces of metal. A single transposed preset structurally
  cannot reach them; only a zone or multisample layer could.

---

## Deferred

- **A two-sample step through the squared rotation matrix** (Phase 2.4). The recursion costs
  eight cycles per sample per block pair and this halves it, at the cost of a second
  coefficient set and a sample-count tail. Deferred rather than open: it would have to land on
  all four kernels at once, or rule one of the numeric contract — that fused packed backends
  agree to the bit — breaks.
- **An AVX-512 oscillator kernel** (`internal/oscbank/oscbank_avx512_amd64.s` — ZMM, 16 lanes,
  masked tail). Moved out of Phase 2.3 by user decision, on the grounds that it cannot be
  verified by the CI this project has. The development machine reports `avx2 fma sse2` and no
  AVX-512 at all, so the kernel could never run locally. GitHub's `ubuntu-latest` x64 pool is
  mixed: Ice Lake runners have AVX-512, EPYC Milan runners do not, and a job is assigned one or
  the other with no way to ask. A runtime-gated kernel would therefore execute on some CI jobs
  and silently take the AVX2 path on others, and a green run would not distinguish "the AVX-512
  kernel is correct" from "the AVX-512 kernel never executed" — which is the one thing a
  differential harness exists to tell you. The layout is already prepared for it: rotor arrays
  round up to an even number of blocks precisely so a 16-lane kernel can consume two blocks at
  a time with no separate tail path, and `cpufeat.Features` now carries `HasAVX512F` and
  `HasAVX512DQ`. Revisit when a runner pool with guaranteed AVX-512 is available, or when
  forced-feature emulation can execute the kernel on hardware that has the instructions.
- Richer preset library. **The multi-note modeling half is discharged by Phase 9**, which fits
  one preset against a spread of recorded notes at once.
- Any GUI editor for the plugin, which now lives in its own repository.
- **A shaped excitation** — a short noise burst or strike filter as a fitted model feature.
  Phase 8's review (finding 15) traces the fake beat clusters in `recorded-bar.json` to the
  single-impulse strike; the objective work in 8.2 makes those clusters expensive, but only a
  model change makes a real attack cheap. 8.3's first fit of the recording added the other
  half: the excitation lowpass and the ±2 amplitude range together bound the spectral tilt the
  model can produce, so the high partials go missing and a cluster reappears to make one loud
  enough (`docs/training.md`). Model work, after 8.6 has numbers to compare against.
- **Multi-velocity fitting**. Still deferred, and still for the original reason: no pack in
  `testdata/reference/packs/` records the same bar at more than one strike strength. The
  multi-note half of this item **is discharged by Phase 9** -- the recordings turned out to
  exist, 56 of them, sitting in the tree unreferenced since phase 8.
- **go-cma-es 0.2.0.** It fixes a measured covariance defect that does not bite at this
  dimensionality, and bumping it makes every recorded CMA-ES figure incomparable. Only after
  8.6's tables exist, and then with a re-baseline. **Still deferred after 8.4**, which pinned
  the dependency at v0.1.0 deliberately: the defect needs λ above 256 separable or 1024 block,
  and this fit runs twelve by default with `--cmaes-lambda` not expected past 64, while v0.2.0
  changes the sampling trajectory and would split 8.5's campaign numbers in two.

## Resume Point

Phases 0-4, 6 and now 8 are closed and summarised above; their detail is in [docs/](docs/).
**Phases 5 and 7 are open**, and what is left in each is small and named below.

**The re-measurement is done, 2026-09-05.** It was the thing to do next and it is no longer
outstanding: `engine-shape` was re-run from a stamped build, the second reference was re-taken
over eight paired seeds, the `recorded-bar` refit was re-run at its own recipe, and every
`distance`-derived table in `docs/training.md` was re-taken or marked. What it found is worth
carrying rather than filing:

- **The campaign's ranking inverted.** `mayfly-r16` went from first of five to fourth.
  `mayfly-single` beats it on the recording in eleven blocks of twelve at p = 0.0003, where the
  two had been indistinguishable at p = 0.64. The default is retained only because 8.6's
  promotion rule refuses the challenger on the second reference, where it materially regresses
  `onset_db` and `envelope_db` over eight seeds of eight. **This is now a live open question**,
  not a settled one: the better arm on the only real recording this project has is not the one
  it ships. The design that settles the budget half of it is registered and **has now been run**,
  2026-09-05: `rounds-12k`, `rounds-24k` and `rounds-48k`, the two shapes at half the campaign
  budget, at it and at twice it, 72 jobs. `mayfly-single` wins all three — +0.0508, +0.0381,
  +0.0226, the last at p = 0.005 — so the inversion is not an artefact of the cap. But its margin
  narrows monotonically as the budget rises and its spread nearly doubles, and it is still
  improving at 97% of even the 48,000-evaluation budget, so no rung converges it. On C5 it
  materially regresses no term at 24k or 48k; `partial_level_db`, material at 12k, dissolves by
  24k. **The default is unchanged**, still refused on A4 — but that A4 block was run at 24,000
  evaluations, and the ladder has just shown a material regression dissolving with budget on this
  same comparison, so the named follow-on is an A4 rung at 48,000.
- **8.10 was not the only thing that had moved.** The objective had gained an eleventh term,
  `onset_db`, and `balanced` was reweighted for it, which is what actually moved the composite
  table's scores — every term underneath them reproduced. The campaign that promoted the current
  default predates that term entirely: its `results.csv` has 36 columns and no `onset_db`.
- **The refit's blocker is discharged.** It now renders at −2.7 dBFS against the shipped preset's
  −3.0, beats it by 47% at its own note, and is the first fit against the recording to correlate
  in the time domain at all — waveform gain +4.4 dB against the shipped preset's −47.3.
  **Whether to ship it is an open decision**, deliberately not taken; the preset is in `out/`.
- One documented number was simply an error rather than a casualty: the analysis seed's
  `balanced` 0.24 should have read 0.3085 on the day it was written.

**Phase 8, training.** Reviewed on 2026-09-02; 8.0 is done the same day. `glockenspiel distance`
prints every objective term for a written preset and `docs/training.md` holds the baseline for
both shipped presets against both references. The baseline's own finding is worth carrying: the
time-domain objective's least-squares gain against the recording is −24.6 to −64.8 dB for every
shipped preset, so nothing the current objective computes from the waveform is informative on a
real recording, and only the spectral term orders candidates at all. (The −52 to −93 dB this
line quoted until 2026-09-05 was the same finding taken before the loader peak-normalised the
recording; every figure is 27.6 dB lower for that reason alone.) 8.1 is done the same day:
`glockenspiel analyze` cuts a reference to its first strike and measures its partials, and
`docs/training.md` holds the measurement for both references. 8.2 is done the same day: the
objective is a composite of ten physical terms under a profile, `balanced` by default, every
surface prints or carries the breakdown, and the loader is in the path of every fit. Its own
finding: the shipped `recorded-bar.json` scores 0.42 on `balanced` at its best note against 0.19
for a seed written straight from the analysis, so the search space was the next thing. 8.3 is
done the same day: the base frequency is written through, mode frequencies are absolute and
sorted, the decay box is two seconds narrowed to the note's authoring ceiling, the mode count and
the seed come from the analysis, half of every warm Mayfly population starts around the seed,
and every front end reports what finished on a bound. The acceptance test recovers a six-mode
target in twelve of twelve seeds. A first 90 s fit of the C5 recording reaches `balanced` 0.18
against the shipped preset's 0.42, and its pinned report names the next model limit: the
excitation lowpass and the amplitude ceiling together bound the spectral tilt, so the high
partials go missing and the fake-beat cluster reappears at 8.2 kHz (`docs/training.md`). That
model limit belongs with the deferred excitation work unless 8.6's campaign shows the search
cannot live with it. 8.4 is done the same day: `--optimizer cmaes` is the CLI default and
restarts cold until the wall-clock budget is spent, in its own loop rather than the library's
evaluation-budgeted IPOP; Mayfly is at v0.7.1 with the `auto` variant gone; `--polish
nelder-mead|cmaes` refines the incumbent under the `polish` profile and is kept only when the
primary cost drops; one `--seed` feeds every engine, `--workers` is recorded in the checkpoint,
and `--checkpoint-interval` counts optimizer iterations. `simple` stays selectable. Dragonfly is
out on the sibling evidence, and `docs/training.md` records a three-engine smoke run on the C5
recording with the warning that every Mayfly number taken before today was measured under
v0.6.0.

8.5 is done the same day: `cmd/glockenspiel-campaign` plans, runs, collects and analyses a
designed comparison, every job leaves an `internal/fitrun` run directory, the manifest freezes the
design and the binary that planned it, and `engine-shape` and `seed-hunt` are registered.
Only the four-job `smoke` design has been run, as a wiring check.

8.6 is done 2026-09-03, and it changed the default. `engine-shape` ran from commit `4389279`:
sixty jobs, every row on `max_evaluations`, and the registered primary contrast **failed** —
block-covariance CMA-ES lost twelve blocks of twelve (p < 0.0001 after Holm) and separable
CMA-ES, the 8.4 default, lost too (p = 0.002). Nothing beat `mayfly-r16`, which is now what a
bare `glockenspiel fit` runs. The promotion rule gained a materiality threshold on the way,
because as written it could never fire. Getting there cost two campaign runs: round and restart
random streams were derived from the seed by arithmetic, so consecutive campaign blocks shared
almost all their restarts, and a coupled design understates its own spread — the first table put
separable CMA-ES at "retain" where the fixed one puts it at "reject". `seed-hunt` was not run;
it refines a winning CMA-ES arm and there is none. Both refits ran and **neither shipped**: the
`recorded-bar` refit beats the shipped preset by 62% and renders 24.5 dB quieter with no way in
the schema to fix it, which promoted "gain is searched, not solved" from a review finding to a
blocker in `## Deferred`; `default.json`'s reference holds one partial, so fitting it writes a
one-mode preset. [docs/training.md](docs/training.md) holds all of it.

**8.7 is done 2026-09-04.** The server no longer runs its own fit loop: it delegates to
`internal/fitrun`, so the run directory a served job leaves is the same one a campaign job writes.
Every job gets one under `--work-dir`, the 409 is a queue, jobs survive a restart, and the
Optimize tab now has the run history, the provenance and the comparison a campaign needs, proven
by a real short fit through `serve` under Playwright rather than by a mock.

Six defects are worth carrying forward, because all six looked like success and none would have
failed a gate. Four were the same shape, a picture that disagrees with the score: the comparison
clamped only the render to sixty seconds and left the reference at full length, it painted each
spectrogram against its own noise floor where the objective clamps both to the reference's,
selecting a historical run played the active run's audio, and, found only by the whole-phase
review, the comparison drew and played the fitted preset raw when the objective is gain-invariant
and lag-aligned. That last one is the phase's own lesson at its sharpest: a fit that scored well
appeared as a near-flat line with a near-empty spectrogram, contradicting the score by exactly the
quantity the score discards, in the feature built to prevent that. The fourth is what de-duplication is
for: collapsing the three limit tables showed the browser fit had been accepting five requests the
server refuses, having never bounded `cmaes-lambda`, `cmaes-restarts`, `mayfly-nc` or
`mayfly-nc-ratio` and never bounding `mayfly-target-cost` at all. The fifth only appeared when a
real fit ran: the run list carried `aria-pressed` on a `role="listitem"`, which is invalid ARIA,
plus a missing touch target and no focus ring, none of which a mocked test had ever exercised.

**Two things are left open on purpose.** The two Optimize screenshots are stale, and were already
stale before the Playwright item, because the run list landed without regenerating them; this
machine lacks the faces the baselines assume, so they need regenerating where those fonts are
installed. And the debt 8.6 named still stands: the CLI defaults to `mayfly` while the browser fit
still chooses its own backend. Item 4 unified the tables and the names, not the choice, because
`internal/browserfit` cannot import `internal/fitrun` without pulling the filesystem into WASM.
The output gain that used to stand between a measured refit and a shipped preset is done — see
8.9 below.

**8.8 is done 2026-09-05**, and it closes Phase 8. `glockenspiel fit` runs through
`internal/fitrun` like everything else, so a run started in a terminal leaves the same directory
a served fit does; the server rescans its work directory every second and follows a directory
still being written by tailing its trace, which retires the five-minute freshness guess; and the
Optimize tab shows such a run as a live job with its cost curve and term bars, labelled as one
the server cannot stop. Proven end to end rather than by unit test: a fit started in a second
terminal appears in `/api/fit/jobs` as running and followed, its cancel is refused with a 409,
and it goes terminal when `result.json` lands.

The debt 8.6 named still stands, and is now the only one: the CLI defaults to `mayfly` while the
browser fit still chooses its own backend, because `internal/browserfit` cannot import
`internal/fitrun` without pulling the filesystem into WASM. The stale Optimize screenshots that
8.7 left open are gone — they were retaken on 2026-09-04, once it was established that this
machine renders exactly as the CI runner does and the font story that had blocked them for two
weeks was wrong.

**Phase 5, payload size.** The one unaddressed sub-item. Adding the browser optimizer made the
raw WASM larger, so this is now splitting or lazy-loading the Go payload rather than shaving
bytes. Play already does not pay for optimizer code — `fit.worker.ts` loads a separate
`glockenspiel-fit.wasm` — so the remaining question is the Play payload itself.

**Phase 5, two unmeasured acceptance criteria.** First paint under one second, and Lighthouse
accessibility at least 90. The individual accessibility items were checked directly and the
Axe pass is in the Playwright suite, but the Lighthouse number itself has never been taken, and
there is no Lighthouse, web-vitals or perf-budget tooling in the repository at all. Taking
either number means adding a harness first, which is why they are still open while 5.1-5.6 are
done. `docs/tinygo-evaluation.md` claims the measurable first-paint cost was the wood texture
generation rather than the WASM fetch, and that claim is also unbacked by anything runnable —
though 5.4 has since moved the textures to build time, so it may no longer be true either.

**Phase 7, `out/`.** Cleared by Phase 8.0 on 2026-09-02; it held square-wave-era profiles,
checkpoints and renders that nothing could reproduce or use. It stays gitignored local scratch.
