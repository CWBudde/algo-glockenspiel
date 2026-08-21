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

## Status (2026-08-21)

Reviewed against the goal above. What exists and works:

- Exact phase-rotation recursion (`internal/model/decay_osc.go:137-141`), correct and
  drift-free while the decay factor is below 1.
- One genuinely packed AVX2 oscillator kernel (1392 ns per 512-sample block, 6.5× the
  scalar fallback) and one packed AVX2 Chebyshev kernel (136 ns).
- Preset load/save/validation, WAV note rendering, offline fitting with three metrics,
  Nelder-Mead and Mayfly backends, checkpointing, legacy-reference regression tests.
- A deployed browser demo and a VST3 spike.

What does not match the goal:

- The core is a fixed 4-mode bar model. `NumModes = 4` is a `const` materialized as
  `[NumModes]ModeParams` arrays, a hand-unrolled scalar loop, and five `.s` files.
- The Chebyshev shaper runs on the excitation _before_ the resonators
  (`internal/model/bar.go:110-115`), so harmonics are not computed on top of the oscillators.
- 76% of the assembly is dead, and every dead kernel is slower than the live one:
  `block4x4` 37280 ns, `cheby_osc_fused` 49309 ns, `mode_block4` 58985 ns per 512-block.
  `cheby_osc_fused_avx2_amd64.s:34-81` is scalar `MULSS`/`ADDSS` inside a `.s` file.
- No NEON, no SSE, no AVX-512, no FMA. ARM and WASM run `processBlock32Generic`, which is
  1.8× slower per sample than the naive loop it replaced.
- Parallelism is across the 4 modes of one voice; voices render serially
  (`internal/synth/realtime.go:105`), so oscillator count costs linearly.
- mayfly is pinned at v0.1.0 (upstream v0.4.0) behind a wrapper that discards the initial
  guess, so `--preset` has no effect on a mayfly search and `--resume` is a no-op.
- `serve` and the optimizer tab have no code.
- CI runs one job: copy static files to Pages. `go build ./...` fails on `main`.

---

## Phase 0: Unblock — DONE (2026-08-21)

Goal: make CI able to tell you when something breaks, before changing anything else.

Tasks:

- Add `.github/workflows/ci.yml` on `pull_request` and `push`: `go build ./...`,
  `go vet ./...`, `go test -race ./...`, `just check-formatted`, `just lint`,
  `just check-tidy`. Matrix `ubuntu-latest`, `ubuntu-24.04-arm`, and a `GOOS=js GOARCH=wasm`
  build.
- Fix `go build ./...`. `cmd/glockenspiel-vst3/doc.go` declares `package main` with no build
  tag while the only `func main()` sits behind `//go:build linux && cgo && vst3go`.
- Untrack `optimizer.test` (7.1 MB, unstripped, over half of `.git`). Add `*.test`, `bin/`,
  `coverage.*`, `*.prof`, `*.pprof` to `.gitignore`.
- Add a `LICENSE` file and attribute the vendored BSD-licensed `web/wasm_exec.js`.
- Add `*.js`, `*.css`, `*.html` to the prettier `includes` in `treefmt.toml`; 2401 lines of
  the user-facing product are outside every formatter and linter. Fix the file header, which
  still says "algo-dsp". Commit `.trunk/` or delete it — it is currently hidden by a
  local-only `.git/info/exclude`.
- `.golangci.yml`: `issues.exclude-use-default` is v1 syntax under `version: "2"`, and its
  `gofmt` conflicts with `gofumpt` in `treefmt.toml`. Pick one.
- Bump dependencies: mayfly v0.1.0 to v0.4.0, algo-dsp v0.4.0 to v0.7.0, algo-fft v0.6.10 to
  v0.8.0, algo-vecmath v0.1.0 to v0.1.3. Run `go mod tidy`; note that
  `internal/cpufeat/features_amd64.go` imports `golang.org/x/sys/cpu` directly, so it should
  not be marked indirect.

Acceptance criteria:

- [x] `go build ./...`, `go test -race ./...`, and `golangci-lint run` pass locally, and
      `.github/workflows/ci.yml` runs them on every push and pull request.
- [x] No compiled binaries tracked in git (`optimizer.test` untracked; `.gitignore` extended).
- [x] A LICENSE exists (MIT), with `web/THIRD_PARTY.md` covering the vendored `wasm_exec.js`.
- [x] JS, CSS, and HTML are covered by the formatter (`treefmt.toml` + a prettier CI job).
- [x] Dependencies are current: algo-dsp v0.7.0, algo-fft v0.8.0, mayfly v0.4.0,
      algo-vecmath v0.1.3.

Notes:

- `go build ./...` was failing because `cmd/glockenspiel-vst3/doc.go` declared `package main`
  with no build tag. It now carries the same `//go:build linux && cgo && vst3go` tag as
  `main_linux.go`.
- `golangci-lint` had never passed: 94 findings, of which 41 `wsl_v5` were auto-fixable, 50
  `varnamelen` were the linter objecting to conventional DSP names (`t0..t2`, `c1..c3`,
  `re`/`im`, `aw`/`bw`), and 3 were real. `varnamelen` is now configured for the domain
  rather than disabled; the real findings — an empty `else if` in `spectral.go` that swallowed
  an FFT-plan error, and two missing `b.Helper()` calls — are fixed.
- algo-fft v0.8.0 made the real plans generic; `spectralFFTPlan` now holds
  `*algofft.FastPlanReal[float64, complex128]` and `*algofft.PlanReal[float64, complex128]`.
- mayfly v0.4.0 is a drop-in at the call site. Actually _using_ its new API is Phase 3.
- `go mod tidy` promoted `golang.org/x/sys` to a direct dependency and dropped the stale
  `justyntemme/vst3go` entries from `go.sum`.
- `.trunk/` is deleted and gitignored; golangci-lint and the prettier job cover its useful
  overlap and actually run.

## Phase 1: Configurable Oscillator Bank

Goal: replace the fixed 4-mode bar model with a bank whose oscillator and harmonic counts are
runtime configuration, laid out for SIMD, without losing performance.

Tasks:

- New package `internal/oscbank`. AoSoA layout, float32 state: `re`, `im`, `cosCoeff`,
  `sinCoeff`, `amp` as `[]float32` in blocks of the target's vector width. `N` and `M` are
  struct fields.
- Keep the existing phase-rotation recursion verbatim. If a sustained oscillator
  (decay factor 1) is ever added, magnitude renormalization becomes mandatory.
- Compute harmonics on top of the oscillators, either as integer-multiple rotors sharing a
  decay or by keeping the Chebyshev shaper as an optional post-oscillator stage.
- Pack `voices x oscillators` into lanes so oscillator count scales sublinearly rather than
  adding serial voice cost.
- Remove the per-sample horizontal reduction; accumulate in-lane across the block and reduce
  once at the end.
- Preset schema v2 with a variable-length oscillator array, plus a v1 loader so the shipped
  presets keep working.
- Retire `internal/model` only after the benchmark gate passes.

Acceptance criteria:

- [ ] Oscillator and harmonic counts are configurable at runtime.
- [ ] The 4x4 case benchmarks at or below 1392 ns per 512-sample block.
- [ ] Scaling to 64 oscillators is close to linear in total work, not in voice count.
- [ ] Every existing preset round-trips through the v1 loader and renders identically.

## Phase 2: Real SIMD On Four Targets

Goal: one kernel shape, four packed backends, one differential test suite.

Tasks:

- Delete the dead assembly first: `cheby_osc_fused_avx2_amd64.{go,s}`,
  `mode_block4_avx2_amd64.{go,s}`, `block4x4_avx2_amd64.s`, `osc_strategy.go`, their
  `_other.go` stubs, and `modeBlock4Coeff` / `processModeBlock4` / `block4Coeff`.
  `block4x4_avx2_amd64.s` hardcodes `unsafe.Sizeof(modeBlock4Coeff) == 208` with no
  compile-time assertion.
- Extend `internal/cpufeat` with `HasSSE2`, `HasSSE3`, `HasAVX`, `HasFMA`, `HasAVX2`,
  `HasAVX512F`/`HasAVX512DQ`, and arm64 `HasASIMD`. Make `Detect()` lock-free after first
  use; it currently takes an `RWMutex` and a `Mutex` on every audio block.
- Write packed float32 kernels with FMA where available:
  - `oscbank_avx2_amd64.s` (`VFMADD231PS`, YMM, 8 lanes, unrolled for ILP)
  - `oscbank_avx512_amd64.s` (ZMM, 16 lanes, masked tail)
  - `oscbank_sse2_amd64.s` (XMM, 4 lanes)
  - `oscbank_arm64.s` (`FMLA V*.S4`, 4 lanes; NEON is arm64 baseline, no runtime gate)
- Make every backend agree numerically. Today AVX2 Chebyshev is float32, its tail is a third
  float32 implementation, and the fallback is float64, so output differs by machine and even
  within one buffer. `internal/optimizer/rms_avx2_amd64.s` accumulates in float32 while
  `rms.go` accumulates in float64, so optimizer fitness is not reproducible across machines.
- Add `unsafe.Offsetof`/`Sizeof` compile-time assertions for every struct an `.s` file indexes.
- Add `FuzzOscBankMatchesGeneric` comparing each backend to the scalar reference over random
  coefficients, states, inputs, and lengths. There is currently no fuzzing at all.
- Tighten the existing `approxEqual(..., 1e-5)` tolerances and give the kernel tests non-zero
  initial state; `decay_osc_test.go:389` and `:475` currently run with zero state, so half the
  packed kernel multiplies by zero.
- Remove audio-thread allocations (`internal/synth/realtime.go:72,98,108`,
  `internal/model/bar.go:149-157`). Set MXCSR FTZ/DAZ once per stream instead of the branchy
  per-block `flushDenormals` with its magic `1e-300` floor.

Acceptance criteria:

- [ ] AVX2, AVX-512, SSE2, and NEON kernels exist, are packed, and use FMA where available.
- [ ] Differential and fuzz tests pass for every backend on amd64 and arm64 CI.
- [ ] A benchmark table in `docs/` records ns/op per backend against the scalar reference.
- [ ] No allocation and no mutex acquisition on the audio path.
- [ ] Rendered output is bit-identical across backends for a given precision.

## Phase 3: Optimizer — DONE (2026-08-21)

Goal: use mayfly as intended, make the objectives measure what they claim to, and make the
CLI usable.

Tasks:

- Upgrade to mayfly v0.4.0 and delete the hand-rolled machinery it replaces:
  `OptimizeContext` for cancellation, `WithProgressObserver` for progress,
  `WithInitialPopulation` for seeding, `Result.TerminationReason`, and
  `EnableParallel`/`MaxWorkers`.
- Make the objective safe for concurrent use before enabling parallelism.
  `ObjectiveFunction.Evaluate` mutates shared render state and the mayfly closure mutates
  `evals`/`bestCost`/`bestParams` without synchronization. Give each worker its own scratch
  state and drop the process-wide FFT plan mutex.
- Objectives:
  - Add onset alignment and gain normalization to `ComputeRMSError`. A 7-sample offset at
    1756 Hz is a full phase inversion.
  - Replace the 4096-sample cap in `spectral.go` with a multi-frame STFT; the current cap
    ignores about 95% of a two-second reference, including all of the decay.
  - Reweight `spectralBinWeight`; it currently weights sub-500 Hz highest for an instrument
    whose fundamental is above 1 kHz.
  - Fix the PCM16 round trip (32767 versus 32768), stop quantizing the reference, and stop
    quantizing every candidate, which makes the objective piecewise constant.
  - De-duplicate `projectToPCM16Domain` and the WAV loader.
- Checkpoints: count progress callbacks rather than reusing mayfly's evaluation counter as an
  iteration count, which currently breaks both the resumed budget and the checkpoint modulo.
  Sort checkpoints numerically. Warn on a dimension-mismatched checkpoint instead of silently
  ignoring it. `fsync` before rename.
- CLI: print errors (`cmd/glockenspiel/main.go` discards them while `root.go` sets
  `SilenceErrors`); use the embedded default preset instead of a CWD-relative path; add a
  bounds flag; `cobra.NoArgs`; `DurationVar` for the time budget; signal handling and a
  `context.Context` threaded through; fix the `--optimizer` help text.
- Fix `SimpleOptimizer` returning best-params that do not correspond to its reported best
  cost, and replace mirror-based bound handling with normalized-space optimization so both
  backends search the same space.

Acceptance criteria:

- [x] Failures print a real error message. Verified: a bad `--reference` now prints
      `glockenspiel: open wav "...": no such file or directory` and exits 1; previously it
      exited 1 with completely empty output.
- [x] Starting a mayfly fit from the exact optimum yields a cost near zero (regression test).
- [x] `--time-budget` stops the run within its budget. Verified: `--time-budget 10s` returned
      after 10.06 s with `stop=time_budget`.
- [x] `--resume` continues with the remaining budget. Verified: resuming picks up at the
      checkpoint's exact cost (0.244404) instead of restarting from a random population.
- [x] Objective evaluation is parallel and race-free under `-race`. Verified: a fit run
      sustains ~561% CPU; the whole suite passes `go test -race`.
- [x] Fitting a reference with a leading offset converges
      (`TestObjectiveFitsReferenceWithLeadingSilence`: aligned cost < 1e-6, unaligned > 100x).

Notes:

- mayfly now uses the real v0.4.0 API: `OptimizeContext`, `WithInitialPopulation`,
  `WithProgressObserver`, `Result.TerminationReason`, `NewVariant`, and
  `EnableParallel`/`MaxWorkers`. The hand-rolled deadline poison, progress hook, variant
  switch and `recover()` are gone.
- Both backends now optimize the unit cube, so they finally search the same space.
  `DecayMs` is log-encoded like the frequencies. `Range.Mirror` is deleted — mirroring made
  the objective a folded many-to-one map that Nelder-Mead chased across the fold.
- **Checkpoint format is now version 2.0.** Version 1.0 files encoded decay linearly and are
  refused with an explanatory error rather than silently resumed at the wrong decay.
- The objective no longer quantizes to PCM16. That destroyed 24-bit reference precision and
  made the cost piecewise constant, so Nelder-Mead's 1e-8 tolerance was comparing values
  below the quantization step. `ProjectToPCM16Domain` survives as a reporting aid for the
  figures `fit` prints about the rendered WAV; the duplicate in `fit.go`, which had the
  32767-in/32768-out asymmetry, is deleted.
- The spectral metric is a multi-frame STFT over the whole signal. It previously analysed
  only the first 4096 samples, ignoring ~95% of a two-second reference and therefore
  essentially all of the decay it was supposed to be fitting.
- `rms_avx2_amd64.s` accumulates into float64 YMM accumulators, so a fit is reproducible
  between AVX2 and non-AVX2 hosts. Its reduction tail is fully VEX-encoded, removing an
  AVX-to-SSE transition per call.
- `internal/cpufeat.Detect()` is now lock-free after first use: 1.1 ns/op, down from ~20 ns
  with an RWMutex plus a Mutex on every audio block.
- **`simd_dispatch_test_amd64.go` did not end in `_test.go`**, so Go compiled it as
  production code: its eight SIMD dispatch tests had never run anywhere, and `testing` was
  linked into the shipped binary. Renamed to `simd_dispatch_amd64_test.go`; all eight pass.

## Phase 4: Serve And The Optimizer UI

Goal: run optimization interactively from a browser.

Tasks:

- Add `glockenspiel serve --addr :8080` (`internal/cli/serve.go`, `internal/server/`)
  serving the embedded web assets plus a JSON API: start a fit, stream progress over SSE,
  fetch the result preset, render audio.
- Add a tab bar to the web UI: **Play** and **Optimize**.
- The Optimize tab uploads a reference WAV, selects metric, optimizer and bounds, shows a
  live cost curve, auditions the fitted preset, and downloads it.
- The Pages build ships Play; Optimize explains that it needs the local CLI. Running the
  optimizer in WASM is a later option, not a blocker.

Acceptance criteria:

- [ ] `serve` hosts the UI and the API.
- [ ] A fit can be started, watched, auditioned, and downloaded from the browser.
- [ ] The Pages build degrades gracefully with no server.

## Phase 5: Web App

Goal: make the user-facing product good.

Tasks:

- Replace `ScriptProcessorNode` with `AudioWorklet`. Decide explicitly between a
  same-thread worklet instance and a Worker plus ring buffer; `SharedArrayBuffer` needs
  COOP/COEP headers, which GitHub Pages cannot set.
- Fix master gain, which never reaches sounding notes because gain is baked per voice at
  NoteOn while `ProcessBlock` never consults it.
- Fix `gainsForNote`, which hardcodes `firstNote = 72` over 24 semitones while the UI spans
  MIDI 36 to 96, producing over-unity clipped gain and an inverted right channel below C4.
- Bake the wood textures at build time. `wood-texture.js` is 577 lines, larger than the rest
  of the front end, and runs on the order of 10^8 operations synchronously before first paint
  and before the WASM fetch starts, then again on every species change. Keep the generator as
  a build tool.
- Replace the 50 ms `setTimeout` WASM-ready race with a real ready signal from Go.
- Accessibility: add focus styles (there are none), make bars and keys keyboard-activatable
  (they bind only `pointerdown`), give piano keys accessible names, and add `aria-live` to
  the status element. Add `touch-action: manipulation` and `user-select: none`.
- Wire or remove the inert hamburger menu, preset select, and Save/Load buttons. Fix the
  `<h1>`, which says "VST3" on a page that is not a VST3.
- WASM bridge: namespace the globals, delete `wasmGetMemoryBuffer` (it reads a global nothing
  sets), cache the `Float32Array` view instead of allocating one per callback, add
  `-trimpath -ldflags="-s -w"` and a `wasm-opt` pass, add content-hash cache busting, and
  stop overwriting the tracked `wasm_exec.js` during the build.
- Document the TinyGo decision either way.

Acceptance criteria:

- [ ] Audio runs off the main thread with no dropouts under load.
- [ ] Volume affects a ringing note; low keys are in phase and unclipped.
- [ ] First paint under one second on a mid-range device.
- [ ] Full keyboard traversal with visible focus; Lighthouse accessibility at least 90.
- [ ] The WASM payload is materially smaller and cache-busted.

## Phase 6: Split Out VST3

Goal: this repo builds cleanly from a fresh clone.

Tasks:

- Move `plugin/vst3/`, `cmd/glockenspiel-vst3/`, and `docs/vst3*.md` to their own repository
  depending on this module normally.
- Remove `replace github.com/cwbudde/vst3go => ../vst3go`, which is unresolvable without a
  sibling checkout and breaks every documented `-tags=vst3go` command as well as
  `go mod tidy`.
- Clean the stale `justyntemme/vst3go` entries out of `go.sum`.

Acceptance criteria:

- [ ] No `replace` directive; `go mod tidy` is a no-op.
- [ ] The split-out repo builds against a published version of this module.

## Phase 7: Documentation

Goal: make the docs describe the project that exists.

Tasks:

- Rewrite `README.md` around the web app as the primary product with the CLI second. It
  currently claims VST/DAW plugin support and GUI tooling are "not implemented" while both
  exist, contradicts its own "Implemented" list five lines above, and omits `web/`,
  `plugin/`, `docs/`, `scripts/`, and `cmd/glockenspiel-wasm` from the layout tree.
- Document the web app in `docs/`; it is currently undocumented.
- Migrate the findings in `out/` into `docs/` and delete the directory.
- Retire `docs/vst3-evaluation.md` and `docs/vst3go-spike.md` with the split, or mark them
  historical.

Acceptance criteria:

- [ ] README describes what the repo actually contains.
- [ ] The web app is documented.
- [ ] `out/` is migrated and removed.

## Deferred

- Running the optimizer itself in WASM.
- Richer preset library and multi-note modeling.
- Any GUI editor for the plugin, which now lives in its own repository.

## Resume Point

Phases 0 and 3 are closed. Phases 1, 2, 4, 5, 6 and 7 are open.

Phase 1 (the configurable oscillator bank) is the natural next step: Phase 2's SIMD work
targets the bank's layout, and Phase 4's `serve` builds on the now-context-aware optimizer.

Two findings from Phase 3 that belong to later phases:

- The shipped `assets/presets/default.json` renders at a peak of about 6.17, roughly
  +15.8 dBFS. Any fit against a normalized recording has to travel ~16 dB of amplitude before
  modal structure starts to matter, and writing that render to a 16-bit WAV clips it beyond
  recognition. Either the preset amplitudes need rescaling or `fit` should default to
  `--normalize-gain`.
- `fit` computes its resumed budget as `max-iter - checkpoint.Iteration`, but those are
  different units: `Progress.Iteration` counts progress reports while `max-iter` bounds
  optimizer iterations. It warns when the subtraction exhausts the budget, but making it
  exact needs a report count on `Result`.
