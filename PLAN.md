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

Closed phases are summarised here and documented in full under [docs/](docs/); the detail that
was in this file has moved there rather than been dropped.

## Status (2026-08-23)

Reviewed against the goal above. What exists and works:

- A configurable oscillator bank (`internal/oscbank`): `N` oscillators with `M` harmonic
  partials each, both ordinary runtime values, in an AoSoA float32 layout.
- Three packed oscillator kernels — AVX2, SSE2 and NEON — plus one packed AVX2 Chebyshev
  kernel, under a written numeric contract with a harness that enforces it. Everything else
  runs the portable kernel, roughly 7x slower on amd64 and 5.3x on arm64.
- An audio path that neither allocates nor locks, note-on included.
- Preset schema v1 and v2 side by side, WAV note rendering, offline fitting with three metrics,
  Nelder-Mead and Mayfly v0.5.1 backends, checkpoint and resume, legacy-reference regression
  tests.
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
      at −38.07 dBFS, its oscillator bank 18 dB *below* the dry `input_mix` path, and no
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
- Richer preset library and multi-note modeling.
- Any GUI editor for the plugin, which now lives in its own repository.

## Resume Point

Phases 0-4 and 6 are closed and summarised above; their detail is in [docs/](docs/). **Phases
5 and 7 are open**, and between them there are four things left.

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

**Phase 7, `out/`.** Local scratch: profiles, checkpoints and rendered WAVs, gitignored,
about 1 MB. Anything worth keeping in there is a benchmark number that belongs in `docs/`.

**Two findings from the 5.1 re-fit, worth a look and neither blocking.** The re-fitted default
preset sits on three parameter bounds — `input_mix` at 2.0 and three amplitudes at ±2 — which
says the model has no gain of its own and the fit spends the amplitude bound on level. And the
per-parameter recovery assertions in `TestOptimizationImprovesFitAgainstLegacyReference` had to
go: a time-domain objective over a preset whose modes actually carry the signal is sharp enough
in mode frequency that a local search cannot walk a perturbation back.
