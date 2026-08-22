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

| Phase | Title                        | Status                            |
| ----- | ---------------------------- | --------------------------------- |
| 0     | Unblock                      | done                              |
| 1     | Configurable oscillator bank | done                              |
| 2     | Real SIMD on three targets   | open — 2.1-2.3 done, 2.4 partly   |
| 3     | Optimizer                    | done                              |
| 4     | Serve and the optimizer UI   | open — no code yet                |
| 5     | Web app                      | open — no code yet                |
| 6     | Split out VST3               | open — 6.1 all but the last audit |
| 7     | Documentation                | open — 7.1 and 7.2 done           |

## Status (2026-08-21)

Reviewed against the goal above. What exists and works:

- A configurable oscillator bank (`internal/oscbank`): `N` oscillators with `M` harmonic
  partials each, both ordinary runtime values, in an AoSoA float32 layout. Sixteen rotors run
  in ~1140 ns per 512-sample block against 1314-1384 ns for the four-rotor kernel it replaced.
- Exact phase-rotation recursion, correct and drift-free while the decay factor is below 1.
- Three packed oscillator kernels — AVX2, SSE2 and NEON — plus one packed AVX2 Chebyshev
  kernel (136 ns). Everything else runs the portable kernel, roughly 7x slower.
- A written numeric contract with a harness that enforces it: golden vectors, a backend
  differential grid, and `FuzzOscBankMatchesGeneric`. The portable kernel is pinned against
  compiler fusion so it is the same program at `GOAMD64=v1`, `v3`, `v4` and on arm64.
- Denormal flushing as a scope (`oscbank.FlushDenormals`): MXCSR FTZ+DAZ on amd64, FPCR FZ on
  arm64, per block and per realtime callback, with the caller's mode restored.
- Preset schema v1 and v2 side by side with a strict v1 loader, WAV note rendering, offline
  fitting with three metrics, Nelder-Mead and Mayfly v0.4.0 backends, parallel race-free
  objective evaluation, checkpoint and resume, legacy-reference regression tests.
- CI that builds, vets, race-tests on amd64 at `GOAMD64=v1` and `v3` **and** on arm64, lints,
  checks formatting, and builds the WASM target.
- A deployed browser demo and a VST3 spike.

What does not match the goal:

- Cross-voice lane packing is missing. A bank fills its lanes from one voice's oscillators and
  `internal/synth/realtime.go` renders voices serially, so voice count still costs linearly.
- The audio path still allocates, and locks once. `synth.NewVoice` builds a whole transposed
  `model.Bar` per note-on (`internal/synth/synth.go:106`), measured at 19 allocations, and
  the first block in a process serializes on `cpufeat`'s detection mutex because nothing
  warms it beforehand. See Phase 2.4.
- The NEON kernel has no measured throughput. It is exercised under qemu-user, which is
  worthless for timing, so the NEON row of the benchmark table in `docs/oscillator-bank.md` is
  still `TODO` pending a native arm64 host.
- AVX-512 is deferred rather than written, with the reason in `## Deferred`.
- `serve` and the optimizer tab have no code.
- The web app runs on `ScriptProcessorNode`, master gain never reaches a ringing note, and the
  bottom two octaves of the keyboard are over-unity with an inverted right channel.
- `go build -tags=vst3go ./plugin/...` fails with four errors, and `go.mod` still carries a
  `replace` directive that is unresolvable without a sibling checkout, which is also why
  `just check-tidy` cannot run in CI.
- The web app has no page in `docs/` (Phase 7.3).

---

## Phase 0: Unblock — DONE (2026-08-21)

Goal: make CI able to tell you when something breaks, before changing anything else.

Acceptance criteria:

- [x] `go build ./...`, `go test -race ./...`, and `golangci-lint run` pass locally, and
      `.github/workflows/` runs them on every push and pull request.
- [x] No compiled binaries tracked in git (`optimizer.test` untracked; `.gitignore` extended).
- [x] A LICENSE exists (MIT), with `web/THIRD_PARTY.md` covering the vendored `wasm_exec.js`.
- [x] JS, CSS, and HTML are covered by the formatter (`treefmt.toml` + a prettier CI job).
- [x] Dependencies are current: algo-dsp v0.7.0, algo-fft v0.8.0, mayfly v0.4.0,
      algo-vecmath v0.1.3.

Worth remembering: `golangci-lint` had never passed before this phase; `varnamelen` is now
configured for DSP names rather than disabled. algo-fft v0.8.0 made the real plans generic.
`go mod tidy` promoted `golang.org/x/sys` to a direct dependency. The `check-tidy` recipe was
left out of CI because the `vst3go` `replace` directive makes it unrunnable on a runner —
Phase 6.3 earns it back.

## Phase 1: Configurable Oscillator Bank — DONE (2026-08-21)

Goal: replace the fixed 4-mode bar model with a bank whose oscillator and harmonic counts are
runtime configuration, laid out for SIMD, without losing performance.

Acceptance criteria:

- [x] Oscillator and harmonic counts are configurable at runtime. `Bank.SetOscillators`
      takes any `N` and any per-oscillator harmonic count; `BarParams.Modes` is a slice and
      `ModeParams.Harmonics` is optional. The fixed `NumModes` constant survived this phase
      only as the default count and as what a v1 preset must carry; Phase 6.1 removed it
      outright, and what a v1 preset must carry is now `internal/preset`'s own
      `v1ModeCount`.
- [x] The 4x4 case benchmarks at or below 1392 ns per 512-sample block. 16 rotors in
      1128-1154 ns against 1314-1384 ns for the four-rotor kernel it replaces, read off one
      benchmark binary (`go test ./model -bench 'ProcessBlock32$|OscBank4x4'`) so both share
      a thermal state. Four times the oscillator work, 15% less time.
- [x] Scaling to 64 oscillators is close to linear in total work, not in voice count.
      Cost per rotor-block is 71 ns at 16 rotors, 66 ns at 64, 63 ns at 256 — flat to within
      10%, drifting down as the per-pass overhead amortizes. 64 oscillators cost 3.6x four
      oscillators rather than 16x, because four leave twelve of sixteen lanes empty.
      `TestBankPacksRotorsIntoLanes` pins the structural claim.
- [x] Every existing preset round-trips through the v1 loader and renders identically.
      `TestShippedPresetsRenderIdenticallyAfterRoundTrip` renders every file in
      `assets/presets` and `testdata/presets` as loaded, after a save/load cycle, and after
      an upgrade to v2, and requires bit-identical samples.

The design, the benchmark tables, the compatibility rules and the deliberate deferrals are all
in [docs/oscillator-bank.md](docs/oscillator-bank.md). Two of those deferrals became Phase 2
work: cross-voice lane packing (2.4) and bit-identity across backends (2.2).

## Phase 2: Real SIMD On Three Targets

Goal: one kernel shape, three packed backends, one differential test suite.

Acceptance criteria:

- [x] AVX2, SSE2, and NEON kernels exist, are packed, and use FMA where available.
      `oscbank_avx2_amd64.s`, `oscbank_sse2_amd64.s` and `oscbank_arm64.s`. AVX2 uses
      `VFMADD231PS`/`VFNMADD231PS` and NEON `FMLA`/`FMLS`; SSE2 has no FMA to use and is held
      to the portable side of the contract instead. AVX-512 moved to `## Deferred`; the reason
      is recorded there.
- [x] Differential and fuzz tests pass for every backend on amd64 and arm64 CI.
      `availableBackends()` in `internal/oscbank/contract_test.go` registers each reachable
      dispatch path and the differential, golden-vector and fuzz harnesses all iterate it.
      `.github/workflows/test-unit.yaml` runs `ubuntu-latest` at `GOAMD64=v1` and `v3` and
      `ubuntu-24.04-arm`, so every kernel is executed by CI on hardware that has it.
- [ ] A benchmark table in `docs/` records ns/op per backend against the scalar reference.
      Partly: the table in [docs/oscillator-bank.md](docs/oscillator-bank.md#measured-performance)
      has AVX2, SSE2 and portable rows, and the NEON row is `TODO`. qemu-user is a translation
      layer, so a number taken there would be fiction; this stays open until the kernel can be
      benchmarked on a native arm64 host.
- [ ] No allocation and no mutex acquisition on the audio path. **Not met, and not quietly
      reworded so that it passes.** Two things are outstanding, one on each half of the
      criterion.
      **Allocation.** `RealtimeEngine.ProcessBlock` is allocation-free, pinned by
      `TestProcessBlockDoesNotAllocateAfterFirstBlock`. Note-on is not: `synth.NewVoice`
      calls `model.NewBar` (`internal/synth/synth.go:106`), which builds a transposed bar and
      its buffers, measured at 19 allocations per note-on.
      `TestNoteOnAllocatesNothingBeyondTheVoice` measures `NoteOn` against that cost rather
      than against zero, and says so in its own comment. What remains is to build the voice
      without allocating — reusing a `Bar` the engine already owns and reconfiguring it in
      place rather than constructing a new one per note.
      **Mutex.** The steady state takes none: `cpufeat.Detect()` is an `atomic.Pointer` load
      once the feature set is published, and nothing else on the path locks. The _first_
      block in a process does take one. `current` starts nil, no eager call warms it —
      there is no `init()` anywhere that calls `Detect()` — and the first caller is
      `processRotorBlocks` (`internal/oscbank/kernel_amd64.go:60`), which is already on the
      audio thread. So a real audio callback serializes on `detectMu` exactly once per
      process. It is one lock, not a per-block one, but the criterion says "no mutex
      acquisition on the audio path" and one is one. Warming detection from
      `NewRealtimeEngine` or from an `init()` in the dispatch package closes it. Both halves
      are tracked in Phase 2.4.
- [x] Rendered output is bit-identical across every packed backend that fuses, and the
      portable fallback stays inside the documented per-operation bound. Written up as
      three rules in [docs/oscillator-bank.md](docs/oscillator-bank.md#the-numeric-contract):
      fused packed backends are the reference and agree to the bit; the lane-fold order
      is contractual, not an implementation detail; the portable kernel is held to
      `u * E * (6*g(N,d) + folds)`, where `g` is the quadrature gain of a contraction of
      rate `d` and `E` a no-cancellation envelope. A packed SSE2 kernel has no FMA and so
      sits on the bounded side, not the bit-identical one.

### Phase 2.1: Clear the ground

Goal: nothing dead, nothing unasserted, before four new kernels land on top of it.

- [x] Delete `model/cheby_osc_fused_avx2_amd64.{go,s}`, `cheby_osc_fused_avx2_other.go`,
      `mode_block4_avx2_amd64.{go,s}`, `mode_block4_avx2_other.go`, `block4x4_avx2_amd64.s`,
      `decay_osc_avx2_amd64.{go,s}`, `decay_osc_avx2_other.go` and `osc_strategy.go`, together
      with `modeBlock4Coeff` (`model/decay_osc.go:31`), `block4Coeff` (`:26`) and
      `processModeBlock4` (`:230`). Nothing on a rendering path references them: `Bar` drives
      `oscbank` (`model/bar.go:16,44,117-122`) and the rest is reachable only from tests.
      Every dead kernel is also slower than the live one — `block4x4` 37280 ns,
      `cheby_osc_fused` 49309 ns, `mode_block4` 58985 ns per 512-sample block.
- [x] Keep `processChebyshevBlockAVX2` (`model/bar.go:227` -> `model/cheby_avx2_amd64.go:9`);
      it is the one live AVX2 path in `model/`.
- [x] Decide the fate of `QuadDecayOscillator` itself. Deleted with the rest: its differential
      coverage now lives in `internal/oscbank`, against a float64 reference written in the
      test file rather than against a second production kernel nothing renders through.
- [x] Add compile-time assertions for every layout a surviving `.s` file assumes. The struct
      the original finding named went away with `block4x4_avx2_amd64.s`; neither survivor
      indexes a struct at all, so what needed pinning was the constants they hardcode as byte
      offsets. `model/cheby_avx2_amd64.go` pins the 16-byte gain array and the 32-byte vector
      step; `internal/oscbank/kernel_amd64.go` pins `sizeof(float32)`, `LaneWidth`, the block
      pair stride, `accLanes`, and the eight-frame stride of `reduceLanesAVX2`. Both use the
      unsigned-`uintptr` overflow idiom, so a mismatch in either direction fails the build.

### Phase 2.2: Numeric contract and differential harness

Goal: decide what "the backends agree" means, then build the harness that enforces it — before
writing kernels the harness has to judge.

- [x] Pick the contract and write it into `docs/oscillator-bank.md`. Decided: fused packed
      backends are the reference and must agree with each other to the bit; the lane-fold order
      is contractual; the portable kernel may differ by a derived bound rather than by a hedge.
      The section derives it — one FMA substitution costs six roundings per rotor step, a decay
      factor strictly below 1 makes the recursion contractive so that error saturates instead
      of compounding, and the tolerance that falls out is
      `u * E * (6*g(N,d) + folds)`. Halving either constant makes the fuzz harness fail within
      a second, so it is a bound rather than a rubber stamp.
- [x] Collapse the three-way Chebyshev divergence: the AVX2 kernel was float32
      (`model/cheby_avx2_amd64.s`), its tail a second float32 implementation and the fallback
      float64. Done: `model/cheby.go` now holds one definition of what the shaper computes.
      `processChebyshevBlock` dispatches to the AVX2 kernel only for the specialised gain
      count and finishes every remaining sample — tail, non-AVX2 hosts and any other gain
      count — through the single float32 `chebyshevScalar`. `Bar` keeps its gains in float32
      (`model/bar.go:28-30`) so nothing converts per sample, and
      `TestChebyshevBodyTailAndFallbackAgree` pins that the three agree. CI builds at
      `GOAMD64=v3` specifically to catch the seam reopening through compiler fusion.
- [x] Extend `internal/cpufeat.Features` with `HasSSE2`, `HasSSE3`, `HasAVX`, `HasAVX512F`,
      `HasAVX512DQ` and arm64 `HasASIMD`. `Detect()` keeps its `atomic.Pointer` publication
      unchanged; only the flag set grew. `features_arm64.go` reports ASIMD unconditionally,
      because it is mandatory in ARMv8-A and the capability word is unreadable under emulation.
      `TestSetForcedFeaturesCarriesEveryFlag` pins that every new flag survives the override,
      which is how an SSE2-only backend gets tested on an AVX2 host.
- [x] Add `FuzzOscBankMatchesGeneric` comparing each backend to the portable reference over
      random coefficients, states, inputs and lengths. It drives `processRotorBlocks` directly,
      so it can hand the kernels coefficients no `Oscillator` could produce, and its seed corpus
      is the pathology list — decay at both extremes, zero-amplitude lanes, chunk lengths that
      are not multiples of eight, a single-sample chunk — so CI covers all of it without
      running the fuzzer. The first fuzzing in this repo.
- [x] Give the differential tests non-zero initial state and tighten the `1e-5` tolerances.
      All three now seed `re`/`im` before the render and take their tolerance from the contract
      instead of a constant, so it scales with chunk length and decay rate rather than being
      flat.

Already true, do not redo:

- [x] `internal/cpufeat.Detect()` is lock-free after first use — an `atomic.Pointer[Features]`
      published once, with the mutex only serializing first detection and the test-only
      overrides (`internal/cpufeat/features.go:22-39`). 1.1 ns/op, down from ~20 ns.
- [x] `internal/optimizer/rms_avx2_amd64.s:31-54` takes its differences in float32 but widens
      with `VCVTPS2PD` and accumulates into two float64 YMM accumulators, so a fit scores the
      same on an AVX2 host and an ARM one.
- [x] Differential coverage exists for the bank: `TestPackedKernelMatchesPortableKernel`
      (`internal/oscbank/kernel_amd64_test.go:33`), `TestPortableKernelHandlesEveryChunkLength`
      (`:75`) and `TestBankMatchesScalarReference` (`internal/oscbank/oscbank_test.go:118`).
- [x] arm64 CI already runs the fallback paths (`.github/workflows/test-unit.yaml:10-20`), so
      the new kernels have somewhere to be verified the moment they exist.

### Phase 2.3: The packed backends

Goal: two more packed kernels, each green under the 2.2 harness on both CI runners. AVX-512 is
deferred — see `## Deferred` for why a green CI would not be evidence of correctness.

- [x] `internal/oscbank/oscbank_arm64.s` — `FMLA`/`FMLS` on `V*.S4`, four lanes, no runtime
      gate (Advanced SIMD is mandatory in ARMv8-A). It consumes a whole block pair rather than
      a half block, which is what keeps the lane fold in the order rule two pins. Go's arm64
      assembler has no mnemonic for vector multiply, add, subtract or pairwise-add, so the
      kernel encodes those four itself through `WORD` macros.
- [x] `internal/oscbank/oscbank_sse2_amd64.s` — XMM, four lanes, gated on `HasSSE2`. It splits
      a block pair four ways and associates exactly as `kernel_generic.go` does, which makes
      the portable kernel an exact oracle for it rather than an approximate one
      (`TestSSE2IsBitIdenticalToPortable`).
- [x] Extend the `docs/oscillator-bank.md` performance table with one row per backend against
      the scalar reference, and update "Known limits", which used to read "Only AVX2 is
      packed". Both done. One number is still outstanding rather than wrong: the NEON row
      reads `TODO`, because the kernel is only reachable here under qemu-user and a timing
      taken through a translation layer would be fiction. The doc says where to take it from
      and what to take alongside it.

### Phase 2.4: The audio path

Goal: the render loop neither allocates nor stalls.

- [x] Set FTZ/DAZ around the render and retire the branchy per-block `flushDenormals` with its
      magic `1e-300` floor. Done: `oscbank.FlushDenormals` returns a `DenormalScope` that sets
      MXCSR FTZ+DAZ on amd64 and FPCR FZ on arm64 and restores the caller's mode, opened per
      block in `Bank.ProcessBlock` and once per callback in `RealtimeEngine.ProcessBlock`.
      Scoped per block rather than once per stream as this line originally read: a stream-wide
      scope would hold the render goroutine pinned to its OS thread for the whole session,
      which is a heavier promise to make a host than the roughly forty cycles a
      save-set-restore costs against a block that takes thousands. No-op on `GOARCH=wasm`,
      which exposes no control register. See "Denormals" in `docs/oscillator-bank.md` for why
      the numeric contract is still measured unflushed.
- [x] Remove the per-NoteOn block-buffer allocation. Done: `newVoiceSlots` builds the whole
      voice bank at `maxVoices` capacity and cuts every slot's block buffer out of one backing
      array, with a full slice expression so a slot can never append into its neighbour.
      `claimSlot` only reslices within that capacity, and `ProcessBlock` retires a voice by
      swapping it past the end instead of overwriting it, so a retired voice leaves its buffer
      in the slot the next note-on picks up. Retrigger and voice stealing keep the slot's
      buffer too. `internal/synth/realtime_alloc_test.go` covers all four paths.
      What is left of this line is not the buffer but the voice: `synth.NewVoice` still builds
      a whole transposed `model.Bar` per note-on (`internal/synth/synth.go:106`), 19
      allocations, and that is why the phase's "no allocation on the audio path" criterion is
      not ticked.
- [ ] Pack `voices x oscillators` into lanes. Deferred from Phase 1: a bank currently fills its
      lanes from one voice and the render loop in `RealtimeEngine.ProcessBlock`
      (`internal/synth/realtime.go:185`) walks voices serially, so voice count costs linearly.
      This needs per-lane excitation and per-voice output separation, which is a redesign of
      the voice engine.
- [ ] Optional: step two samples at a time through the squared rotation matrix. The recursion
      costs eight cycles per sample per block pair; this halves it at the cost of a second
      coefficient set and a sample-count tail.

## Phase 3: Optimizer — DONE (2026-08-21)

Goal: use mayfly as intended, make the objectives measure what they claim to, and make the
CLI usable.

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

Worth remembering:

- **Checkpoint format is 2.0.** Version 1.0 files encoded decay linearly and are refused with
  an explanatory error rather than silently resumed at the wrong decay. `Progress` and
  `Checkpoint` carry `OptimizerIterations` alongside the progress-report count, and only that
  value is charged against `--max-iter`.
- Both backends optimize the unit cube, so they search the same space. `DecayMs` is log-encoded
  like the frequencies. `Range.Mirror` is deleted — mirroring made the objective a folded
  many-to-one map that Nelder-Mead chased across the fold.
- The objective no longer quantizes to PCM16; `ProjectToPCM16Domain` survives only as a
  reporting aid. The spectral metric is a multi-frame STFT over the whole signal.
- Bounds passed with `--bounds` are a hard constraint: `ObjectiveConfig.StrictBounds` stops the
  codec widening the box to fit the template preset, and the starting point is clamped in.
- One finding still belongs to a later phase: the shipped `assets/presets/default.json` renders
  at a peak of about 6.17, roughly +15.8 dBFS. It is scheduled as Phase 5.1.

## Phase 4: Serve And The Optimizer UI

Goal: run optimization interactively from a browser.

Acceptance criteria:

- [ ] `serve` hosts the UI and the API.
- [ ] A fit can be started, watched, auditioned, and downloaded from the browser.
- [ ] The Pages build degrades gracefully with no server.

### Phase 4.1: The server skeleton

Goal: `glockenspiel serve` puts the existing web app on a port. No fitting yet.

- [ ] `go:embed` the `web/` tree. The only embed in the repo today is
      `assets/embed_default.go:9`, so the pattern exists but the assets are not covered.
- [ ] `internal/cli/serve.go` with `--addr :8080`, registered next to `newSynthCmd`,
      `newFitCmd` and `newVersionCmd` in `internal/cli/root.go:30-34`.
- [ ] `internal/server/` serving the embedded assets plus a version endpoint, with graceful
      shutdown wired to the signal handling `fit` already threads through.

### Phase 4.2: The fit API

Goal: the CLI's fitting stack, reachable over HTTP.

- [ ] A job manager owning one fit at a time, cancellable through the `context.Context` the
      optimizer already accepts.
- [ ] JSON endpoints: start a fit, cancel it, fetch the resulting preset, render audio.
- [ ] An SSE progress stream fed from the existing `optimizer.Progress` callback — the same
      one that drives checkpointing, so no new plumbing inside the optimizer.
- [ ] Reference-WAV upload with a size limit, reusing the existing WAV loader rather than a
      second decoder.

### Phase 4.3: The Optimize tab

Goal: the browser side of the same loop.

- [ ] A tab bar in `web/index.html`: **Play** and **Optimize**. There is no tab markup today.
- [ ] Optimize: upload a reference WAV, choose metric, optimizer and bounds, start and cancel.
- [ ] A live cost curve fed from the SSE stream, an audition button for the fitted preset, and
      a download.
- [ ] The Pages build detects the missing API and explains that Optimize needs the local CLI.
      Running the optimizer in WASM stays a later option, not a blocker.

## Phase 5: Web App

Goal: make the user-facing product good.

Acceptance criteria:

- [ ] Audio runs off the main thread with no dropouts under load.
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
- [x] Resolve the default preset's level, carried over from Phase 3. The amplitudes were divided
      by 8.72, and `TestDefaultPresetRendersNearMinusThreeDBFS` pins the result at −3 dBFS at
      44.1 kHz so it cannot drift back. This closes the item as written; it does not level the
      keyboard, see below.
- [x] Un-mute the low register. `scaledParamsForNote` divides `DecayMs` by the transposition
      ratio, which is correct, but the single `DecayMsMax = 500` served both as the validation
      ceiling and as the optimizer's search bound: the shipped preset's 188.2 ms mode becomes
      1266 ms at note 36, `ValidateBarParams` refused it, and `NoteOn` discarded the error, so
      MIDI 36..52 — the bottom 17 of the 61 playable keys — were silent and left no trace. The
      constant is split into `DecayMsValidationMax` (5000 ms, clearing the search bound
      transposed to note 36 at 3364 ms) and `DecayMsSearchMax` (500 ms, unchanged), and
      `RealtimeEngine` counts refused note-ons rather than dropping them.

Still open, deliberately not fixed here: the peak level across the keyboard spans 27.8 dB with
the shipped preset — +13.8 dBFS at note 36 down to −14.0 dBFS at note 96 — because the modes
carry a fixed amplitude while the low notes' longer decays accumulate far more energy. A per-note
gain law wants calibrating against the whole range at once and is its own change.

### Phase 5.2: Audio transport

Goal: get synthesis off the main thread.

- [ ] Replace `ScriptProcessorNode` (`web/main.js:37`) with `AudioWorklet`.
- [ ] Decide explicitly between a same-thread worklet instance and a Worker plus ring buffer,
      and write the decision down: `SharedArrayBuffer` needs COOP/COEP headers, which GitHub
      Pages cannot set.

### Phase 5.3: WASM bridge and build

Goal: a smaller, cache-busted payload behind a bridge that says what it means.

- [ ] Namespace the globals. `wasmInit`, `wasmNoteOn`, `wasmSetMasterGain`, `wasmProcessBlock`
      and `wasmGetMemoryBuffer` all sit on `js.Global()` (`cmd/glockenspiel-wasm/main.go:18-22`).
- [ ] Delete `wasmGetMemoryBuffer` (`cmd/glockenspiel-wasm/main.go:74-81`): it reads
      `__algoGlockenspielWasmMemory`, which nothing sets, and no JS calls it.
- [ ] Cache the `Float32Array` view instead of allocating one per audio callback
      (`web/main.js:55-59`).
- [ ] Replace the 50 ms `setTimeout` WASM-ready race (`web/main.js:200`) with a real ready
      signal from Go.
- [ ] `scripts/build-wasm.sh`: add `-trimpath`, `-ldflags="-s -w"`, a `wasm-opt` pass and
      content-hash cache busting, and stop it overwriting the tracked `web/wasm_exec.js`.
- [ ] Document the TinyGo decision either way. Nothing in `docs/`, `README.md` or `web/README.md`
      mentions it today.

### Phase 5.4: UI quality

Goal: fast first paint, usable by keyboard, and no controls that lie.

- [ ] Bake the wood textures at build time. `web/wood-texture.js` is 611 lines — larger than the
      rest of the front end — and `createWoodTexture` (`:222-248`) fills a 1024x576 canvas pixel
      by pixel synchronously before first paint and before the WASM fetch starts
      (`web/main.js:154`), then again on every species change (`:133`). Keep the generator as a
      build tool.
- [ ] Accessibility. `web/styles.css` has zero occurrences of `focus`, `outline`, `touch-action`
      or `user-select`; bars and keys bind only `pointerdown` (`web/ui.js:228,260`) so they are
      tabbable but not activatable; black and non-C white keys get empty labels
      (`web/ui.js:249-255`) with no `aria-label`; the status element (`web/index.html:88`) has
      no `aria-live`.
- [ ] Wire or remove the inert controls: the hamburger button (`web/index.html:24-26`, no
      handler), `#preset-select` (`:32-34`, one hardcoded option, no binding) and the disabled
      Save/Load buttons (`:42-43`).
- [ ] Fix the `<h1>` (`web/index.html:20`), which still reads "Algo Glockenspiel VST3" on a page
      that is not a VST3.

## Phase 6: Split Out VST3

Goal: this repo builds cleanly from a fresh clone.

Acceptance criteria:

- [ ] The types the plugin needs are reachable from outside the module.
- [ ] No `replace` directive; `go mod tidy` is a no-op.
- [ ] CI checks module tidiness again.
- [ ] The split-out repo builds against a published version of this module.

### Phase 6.1: Get the public surface right

Goal: a public model package shaped around the runtime-configurable bank, not around four modes.

- [x] Promote the parameter and voice types out of `internal/model` into a public package. Done
      in `cb72e96`: the package is `model/` and `plugin/vst3/params.go:3` imports it normally.
      This was the blocker — Go enforces `internal/` against the module path, so a separate
      module could not have imported it even with a `replace` directive.
- [x] Unexport `NumModes`. Already done, in `54358ff` on the Phase 1 branch (PR #2) — this
      line was written against the state before that commit and had gone stale. The fixed
      constant does not survive anywhere. What `git grep NumModes` still finds is a different
      thing with the same spelling: the `Bar.NumModes()` accessor (`model/bar.go:169-170`)
      and its caller in `model/bank_test.go:58-59`, which report a runtime count rather than
      declare a compile-time size. Both
      callers that genuinely need a frozen count declare their own: `v1ModeCount = 4` in
      `internal/preset/preset.go:28`, scoped to the compatibility layer, and
      `numModes = 4`/`numChebyshevGains = 4` in `plugin/vst3/params.go:11-12`, which the same
      commit also split apart because the two counts are unrelated and a single-mode bar used
      to lose Chebyshev gains 2-4.
- [ ] Audit the rest of the exported surface against the bank. The plugin uses `Bar`, `NewBar`,
      `BarParams`, `ModeParams`, `ChebyshevParams` and the twelve `*Min`/`*Max` range constants;
      each has to make sense for a variable mode count.

### Phase 6.2: Repair the plugin and cover it in CI

Goal: `-tags=vst3go` builds, and stays building.

- [ ] Fix `plugin/vst3/processor_vst3go_linux.go` against the current `vst3go` MIDI API. Four
      errors today, all from the same change: `event.SampleOffset` undefined (`:122`),
      `midi.Event` is a struct and no longer an interface (`:138`), `midi.NoteOnEvent` undefined
      (`:139`), `midi.ControlChangeEvent` undefined (`:144`).
- [ ] Update `plugin/vst3/params.go` for the slice-based `BarParams` that Phase 1 introduced.
- [ ] Add a CI job that builds with `-tags=vst3go`. No job does today, which is why this rotted
      unnoticed; after 6.3 the job lives in the split-out repo.

### Phase 6.3: Split and clean up

Goal: the `replace` directive and the plugin leave together.

- [ ] Move `plugin/vst3/`, `cmd/glockenspiel-vst3/` and `docs/vst3*.md` to their own repository,
      depending on this module normally.
- [ ] Remove `replace github.com/cwbudde/vst3go => ../vst3go` (`go.mod:25`), which is
      unresolvable without a sibling checkout and breaks every documented `-tags=vst3go` command
      as well as `go mod tidy`.
- [ ] Restore the tidiness check in CI. `just check-tidy` exists but no workflow runs it; it was
      dropped in Phase 0 precisely because the `replace` directive makes it unrunnable on a
      runner, and removing the directive is what earns it back.
- [x] Clean the stale `justyntemme/vst3go` entries out of `go.sum`. Already done by the Phase 0
      `go mod tidy`.

## Phase 7: Documentation

Goal: make the docs describe the project that exists.

Acceptance criteria:

- [x] README describes what the repo actually contains.
- [ ] The web app is documented. `web/README.md` covers building and serving it; it has no
      page in `docs/`, which is Phase 7.3.
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
      packed-backend and denormal limits are current; cross-voice lane packing, the two-sample
      step and the optimizer's blindness to per-mode harmonic gains are all still real.

### Phase 7.3: The web app and the leftovers

Goal: document what is undocumented, retire what is finished.

- [ ] Document the web app in `docs/`; it has no page today.
- [ ] Retire `docs/vst3-evaluation.md` and `docs/vst3go-spike.md` with the 6.3 split, or mark
      them historical. They are the two documents Phase 7.2 deliberately left alone:
      `docs/vst3go-spike.md:62` still lists `internal/model` in a package list, which is the
      last stale path in `docs/` and is fixed by the move rather than by an edit.
- [ ] Clear `out/`. It is untracked and gitignored (`.gitignore:17`), so this is local scratch —
      profiles, checkpoints and rendered WAVs — not repo content to migrate. Anything in there
      worth keeping is a benchmark number that belongs in `docs/`.

## Deferred

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
- Running the optimizer itself in WASM.
- Richer preset library and multi-note modeling.
- Any GUI editor for the plugin, which now lives in its own repository.

## Resume Point

Phases 0, 1 and 3 are closed. Phases 2, 4, 5, 6 and 7 are open.

**Phase 2 has three open items: two in 2.4, which want the same thing, and one measurement
that needs hardware nobody here has.** 2.1, 2.2 and 2.3 are done as subphases: the dead
assembly is gone, every layout an `.s` file assumes is pinned at compile time,
the numeric contract is written down with a harness that enforces it, and three packed kernels
— AVX2, SSE2, NEON — are registered in `availableBackends()`
(`internal/oscbank/contract_test.go`) and green on both CI runners. 2.4 has its denormal scope
and its per-note-on block buffers. What is left of 2.4:

- **Cross-voice lane packing.** A bank fills its lanes from one voice, so four oscillators
  leave twelve of sixteen lanes empty and `RealtimeEngine.ProcessBlock` renders voices
  serially. This is a redesign of the voice engine, not a patch: it needs per-lane excitation
  and per-voice output separation.
- **The note-on allocation.** `synth.NewVoice` builds a fresh `model.Bar` per note, 19
  allocations. The fix is to reconfigure a bar the engine already owns instead of constructing
  one, which is also what lane packing needs from the model. Alongside it, the same criterion
  wants the one-off `cpufeat.Detect()` lock on the first block warmed off the audio path.
  Together they are why "no allocation and no mutex acquisition on the audio path" is
  unticked.

Both want the same thing from `model.Bar` — a bar that can be pointed at new parameters
instead of rebuilt — so doing them independently means doing that model work twice.

**The third open item, and not code:** the NEON row of the benchmark table in
`docs/oscillator-bank.md` needs a native arm64 host. Everything here runs the kernel under
qemu-user, which is trustworthy for correctness and worthless for timing, so the row stays
`TODO` rather than being filled in with a translated number. Take
`BenchmarkBank4x4Portable` in the same run — the interesting figure is the ratio, and it will
not be the amd64 ratio.

Independent of all of that, and pickable in any order: **5.1** (three level bugs, all in Go,
all unit-testable), **4.1** (the `serve` skeleton) and **7.3** (a docs page for the web app,
which has none).
