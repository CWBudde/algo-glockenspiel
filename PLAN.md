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
| 2     | Real SIMD on three targets   | done                              |
| 3     | Optimizer                    | done                              |
| 4     | Serve and the optimizer UI   | done                              |
| 5     | Web app                      | open — 5.1–5.3 done; 5.4–5.5 open |
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
- An audio path that neither allocates nor locks. `RealtimeEngine` pools a `Voice` per slot
  and restrikes it in place on note-on, so `NoteOn` costs 0 allocations rather than the 18
  `synth.NewVoice` used to; and `internal/cpufeat` warms its detection cache from an `init()`,
  so the one-off first-block acquisition of `detectMu` can no longer land on the audio thread.

What does not match the goal:

- AVX-512 is deferred rather than written, with the reason in `## Deferred`.
- The optimizer tab has no code. `serve` does: it hosts the app and the fit API
  (Phase 4.1 and 4.2), but nothing in `web/` calls the API yet, so fitting from the browser
  still means fitting with `curl`.
- The optimizer tab is done (4.3) and so is the audio transport (5.2); what is left in Phase 5
  is the baked wood textures and printed key hints (5.4), plus a visual and responsive redesign
  of the Play and Optimize views (5.5).
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
- [x] A benchmark table in `docs/` records ns/op per backend against the scalar reference.
      The table in [docs/oscillator-bank.md](docs/oscillator-bank.md#measured-performance) has
      AVX2, SSE2 and portable rows from an i7-1255U and NEON and portable rows from a native
      Apple M5, the last two medians of ten iterations of one run: 1319 ns/block packed
      against 7009 portable, a ratio of 5.3x. `scripts/bench-remote.sh` is how that host is
      reached, so the row can be re-taken rather than only trusted. The doc records what the
      number does not cover: macOS has no CPU pinning and the host was somebody's laptop, so
      the ratio is the result and the absolute nanoseconds are an upper bound.
- [x] No allocation and no mutex acquisition on the audio path. Both halves are now closed.
      **Allocation — closed.** `RealtimeEngine.ProcessBlock` is allocation-free, pinned by
      `TestProcessBlockDoesNotAllocateAfterFirstBlock`, and so is note-on. `NoteOn` used to
      call `synth.NewVoice`, which built a transposed `model.Bar` and its buffers: 18
      allocations per note-on by `testing.AllocsPerRun` (the 19 this line carried before was
      the figure taken when it was written). The engine now builds one `Voice` per slot at
      construction, warms its bar's buffers there, and restrikes it in place on note-on
      through `Synthesizer.ResetVoice`, which transposes into the voice's own scratch
      `BarParams` and hands it to `Bar.UpdateParams` — reusing every slice, per PR #13.
      Measured at 0 allocations on all three arms of `NoteOn` — retrigger, a fresh slot and
      voice stealing — by `TestNoteOnAllocatesNothing`, which is now an absolute assertion
      rather than the relative one it replaced. Reusing a bar for another note is only correct
      because `ResetVoice` calls `Bar.Reset`: `Bar.setLowpass` deliberately retunes the
      excitation filter without clearing its delay line, so the previous note's tail would
      otherwise leak into the new strike. `internal/synth/realtime_pooling_test.go` pins that
      a reused slot renders a note bit-identically to a freshly built voice.
      **Mutex — closed.** The steady state never took one: `cpufeat.Detect()` is an
      `atomic.Pointer` load once the feature set is published, and nothing else on the path
      locks. The _first_ block used to. `current` starts nil, nothing warmed it, and the
      first caller was `processRotorBlocks` (`internal/oscbank/kernel_amd64.go:60`) — already
      on the audio thread — so exactly one real audio callback per process serialized on
      `detectMu`. One is one, so it counted. `internal/cpufeat` now warms the cache from its
      own `init()`, and package initialisation is guaranteed to complete before `main` runs,
      so the audio thread can no longer be the first caller. The warm-up is a plain `Detect()`
      rather than a `sync.Once`, because `ResetDetection()` has to be able to force a real
      hardware re-detect — that is what lets tests force the portable and SSE2-only kernels,
      the numeric oracle the packed kernels are validated against — and
      `TestDetectReDetectsAfterReset` pins it.
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
      packed". Both done, and the NEON row that read `TODO` while the only arm64 here was
      qemu-user now carries a native Apple M5 measurement with its portable reference from the
      same run. "Known limits" no longer says the portable kernel is about 7x slower without
      qualification: that is the amd64 figure, and the arm64 one is 5.3x against a portable
      reference that has no FMA to give up.

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
      The voice itself followed: the slots carry a pooled `*Voice` too, built and warmed by
      `newVoiceSlots` at engine construction, and `NoteOn` restrikes it through
      `Synthesizer.ResetVoice` instead of calling `NewVoice`. A note-on now allocates nothing
      at all, down from 18, which is what let the phase's "no allocation on the audio path"
      criterion be ticked.
- [x] Pack `voices x oscillators` into lanes. Done end to end. The bank is
      `oscbank.VoiceBank` (`internal/oscbank/voicebank.go`): voice-major, so the lane index is
      the voice index, the rotor arrays are `[rotor][voice]`, the excitation is
      `[samples][LaneWidth]` interleaved instead of a broadcast scalar, and there is no
      horizontal fold at all, because summing over rotors already yields per-voice output. All
      three packed kernels are landed — `oscVoiceRotorsAVX2`, `oscVoiceRotorsSSE2`,
      `oscVoiceRotorsNEON` — alongside the portable oracle, and rule four of the numeric
      contract fixes the accumulation order.
      The caller is `RealtimeEngine.ProcessBlock` (`internal/synth/realtime.go`), which now
      gathers every sounding voice's excitation into one interleaved buffer, runs one
      `VoiceBank.ProcessBlock` per bank of `LaneWidth` lanes, and deinterleaves straight into
      the per-voice gain and mix. A voice takes a lane at note-on and holds it until it
      retires; the lane cannot be tied to the slot, because voice stealing rotates slots and
      retirement swaps them. A retiring voice hands its lane back and the next note-on takes
      the lowest free one, so a block walks `ceil(polyphony / LaneWidth)` banks rather than one
      per slot ever used.
      Only the rotors are shared. The excitation lowpass, the Chebyshev shaper at either stage
      and the dry mix stay per voice inside `model.Bar`, split into `StartBankInput` and
      `FinishBankOutput`; `ProcessExcitation` is still their single-voice composition and the
      rotor-major path is untouched, so offline rendering does not move a bit.
      `TestPolyphonicRenderIsBitIdenticalPerVoice` requires every voice of a ten-note chord
      across two banks to equal, with no tolerance, what the same note renders alone in lane 0.
      `TestPolyphonicRenderMatchesTheSerialPath` bounds the whole render against the previous
      serial implementation at one part in 100000 of the block peak — a bound rather than an
      equality because the two layouts associate a voice's rotor sum differently.
      **What it measured is smaller than what the counting predicts, and the reason is worth
      writing down.** The rotor share of a block at eight voices fell from 25% to 6%, which is
      the fourfold saving the lane arithmetic promises. Two thirds of it goes straight back out
      on the scalar interleave, and the rest is dwarfed by the per-voice excitation lowpass,
      which is 31% of a block and does not pack. Net on the shipped preset: -12% at eight
      sounding voices on both amd64 and a native Apple M5, +24% at one voice, and about +1% on
      the end-to-end `BenchmarkRealtimeEnginePolyphonicPattern`. Polyphony no longer costs
      linearly in rotors; it still costs linearly in everything else a voice does. Numbers and
      profiles in [docs/oscillator-bank.md](docs/oscillator-bank.md#the-realtime-render-path).
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

- [x] `serve` hosts the UI and the API. The static app from Phase 4.1 and the fit endpoints
      from Phase 4.2 are registered by the same `Handler()`, so one process on one port is
      the whole story. What the browser does with the API is 4.3.
- [x] A fit can be started, watched, auditioned, and downloaded from the browser.
      All four, from the Optimize tab of a React front end: `POST api/fit/start` from a
      validated form, `api/fit/events` for the live curve, `api/fit/audio` behind a Render
      button and `api/fit/preset` behind a link. Driven end to end against
      `testdata/reference/legacy_synth_a4.wav`; the downloaded JSON renders with
      `glockenspiel synth --preset`.
- [x] The Pages build degrades gracefully with no server. The tab probes `GET api/version`
      once on mount — which 404s through the static handler where there is no API, since
      there is no `/api/` catch-all — and renders the `glockenspiel serve` command and the
      CLI equivalent instead of a form that would fail on submit. Confirmed against
      `npx serve web/dist` with no Go process: Play works completely, Optimize explains
      itself. Running the optimizer in WASM stays deferred.

### Phase 4.1: The server skeleton — DONE (2026-08-21)

Goal: `glockenspiel serve` puts the existing web app on a port. No fitting yet.

- [x] `go:embed` the `web/` tree. `web/embed.go:28` embeds `index.html`, the three scripts,
      the stylesheet, `wasm_exec.js` and `assets`, file by file rather than as a directory:
      a directory pattern would pull in the gitignored `web/dist` whenever it happened to
      exist and make the binary's contents depend on the state of an ignored directory.
      `web/dist` is served from disk instead.
- [x] `internal/cli/serve.go` with `--addr :8080` (`:45`) and a `--dist` alongside it,
      registered next to `newSynthCmd`, `newFitCmd` and `newVersionCmd` in
      `internal/cli/root.go:33`.
- [x] `internal/server/` serving the embedded assets plus a version endpoint
      (`server.go:227`), with graceful shutdown wired to the signal handling `fit` already
      threads through: `serve.go:82` owns the `signal.NotifyContext` and `Server.Run`
      shuts down under `context.WithoutCancel`, so in-flight requests keep their grace
      period rather than being cut off by the signal that started the shutdown.

### Phase 4.2: The fit API — DONE (2026-08-22)

Goal: the CLI's fitting stack, reachable over HTTP.

- [x] A job manager owning one fit at a time, cancellable through the `context.Context` the
      optimizer already accepts. `internal/server/job.go`: one slot, a second start is a
      409, and `POST /api/fit/cancel` waits for the run to actually stop before it answers,
      so cancel-then-start needs no polling. The context is rooted in `context.Background`
      rather than in the request that started the fit, which returns immediately.
- [x] JSON endpoints: start a fit (`POST /api/fit/start`), cancel it
      (`POST /api/fit/cancel`), read status (`GET /api/fit`), fetch the resulting preset
      (`GET /api/fit/preset`) and render audio (`GET /api/fit/audio`). Nothing a client
      sends is ever used to build a filesystem path.
- [x] An SSE progress stream (`GET /api/fit/events`) fed from the existing
      `optimizer.Progress` callback — the same one that drives checkpointing.
      `internal/optimizer` needed no change at all. The streams are closed before
      `http.Server.Shutdown` runs, because an SSE response is an active connection forever
      and would otherwise burn the whole shutdown timeout on every Ctrl-C.
- [x] Reference-WAV upload with a size limit (16 MiB, `http.MaxBytesReader`), reusing the
      existing WAV loader rather than a second decoder. There was no shared loader to
      reuse — `internal/cli/fit.go`, `internal/cli/synth.go` and
      `internal/optimizer/legacy_validation_test.go` each carried their own copy — so it
      was extracted into `internal/wavio` first and all three migrated onto it.

### Phase 4.3: The Optimize tab — DONE (2026-08-22)

Goal: the browser side of the same loop.

- [x] A tab bar in `web/index.html`: **Play** and **Optimize**. It is not in `index.html` any
      more: the whole front end was rewritten as Vite + React 19 + TypeScript, and the tab
      bar is `src/components/Topbar.tsx` over a hash router in `src/App.tsx`. The rewrite was
      the decision this sub-phase turned on. An Optimize tab is a file upload, a dozen
      validated controls, an SSE subscription, a live chart, an audio player and a download —
      a stateful form on top of a stateful stream — which is where 40 KB of hand-written DOM
      builders with every piece of state as a module-level `let` stops paying for itself.
      TypeScript earned its place separately: the fit API's wire shapes are eleven Go structs,
      and `src/api/types.ts` transcribes them by hand for the same reason
      `internal/server/fit_test.go` re-declares `fitSnapshot` locally — a field renamed in the
      server should be a failing build, not a silently undefined value at runtime.
- [x] Optimize: upload a reference WAV, choose metric, optimizer and bounds, start and cancel.
      `src/features/optimize/FitForm.tsx`. Bounds were not on the API at all — `--bounds` was
      CLI-only and `buildObjective` always took `DefaultParamBounds` — so the optional
      `bounds` multipart field was added on its own branch, which must land before the front
      end does. Every scalar is held client-side to the range `internal/server/params.go`
      holds it to, because a 400 that arrives after a 16 MiB upload is a slow way to learn
      that `note` was 200, and the 409 is surfaced as "a fit is already running" rather than
      as a generic failure — and the job holding the slot is read once and adopted, so the
      Cancel button that message points at is reachable rather than disabled.
- [x] A live cost curve fed from the SSE stream, an audition button for the fitted preset, and
      a download. `useFitEvents.ts`, `CostChart.tsx`, `FitStatus.tsx` and `Audition.tsx`.
      Three things about the stream shaped the code: it carries no `id:` and no `retry:`, so a
      source left open past a terminal event is reconnected and re-handed the same snapshot
      forever and must be closed; every `data:` is a whole snapshot rather than a delta, which
      is what makes attaching mid-run correct immediately; and `hasPreset` is not
      `state === "succeeded"`, because a run cancelled after its first report still has a best
      preset — so the audition gates on the former. There is no polling anywhere in the tab:
      the one non-stream read is a single `GET api/fit` on mount, so a reload lands back on a
      running fit with Cancel reachable instead of stranding the server's single slot. At
      `reportEvery: 1` a run produces up to 100,000 samples, so neither the hook nor the chart
      walks the history per event: the points array is grown in place beside a sample counter,
      and the chart folds in only the samples it has not drawn, one redraw per animation frame.
      The `maxIterations` the panel reads "n of m" against is stamped with the job it was sent
      for, because only a fit this page started has a known limit.
- [x] The Pages build detects the missing API and explains that Optimize needs the local CLI.
      `useApiAvailable.ts`, and the served root moved with it: a Vite bundle is a build
      artifact with hashed names and cannot be `go:embed`ed without making the binary's
      contents depend on whether someone ran a build step, so `DistDir` became the served root
      and the embedded tree shrank to one placeholder page naming `just build-web`. Pages now
      uploads `web/dist`. Running the optimizer in WASM stays deferred.

The architecture, the WASM bridge, the two-step build and the Optimize loop are written up in
[docs/web-app.md](docs/web-app.md), which also closes Phase 7.3's first bullet.

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
- [x] Fix the `<h1>`, which still read "Algo Glockenspiel VST3" on a page that is not a VST3.
      It reads "Algo Glockenspiel".

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

- [ ] At 390px wide, every bar keeps a usable hit target and readable note label; the rack and
      keyboard share one pitch-aligned horizontal viewport rather than overlapping or scrolling
      independently.
- [ ] At 1440px wide, the rack is the clear Play-view focal point, the performance controls form
      one balanced control deck, and the keyboard reads as a supporting input aid.
- [ ] Wood appears on structural instrument surfaces rather than every panel. Bars and hardware
      have a distinct restrained metal treatment, and text does not rely on textured backgrounds
      for contrast.
- [ ] Optimize presents setup, execution and results as a clear sequence, does not render a blank
      chart as though it contained data, and keeps primary job actions near current job state.
- [ ] Playwright reference screenshots cover Play and Optimize at 1440px, Play at 1024px, and Play
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
- [ ] **5.5.6 — Refine the physical instrument.** Give bars a restrained metal finish, simplify
      rails and fasteners, reduce glossy highlights, and bring the mallet inside the composition.
      Define consistent hover, pressed and sounding-note states without changing note activation.
- [ ] **5.5.7 — Make the keyboard supporting, not competing.** Reduce its height and contrast while
      keeping every piano key clickable, named and visibly focusable. Preserve its C2–C7 range and
      active-note synchronization with the bars.
- [ ] **5.5.8 — Fix the mobile playfield.** Put the rack and keyboard in one horizontal pitch
      viewport with a shared minimum instrument width. Keep the control deck stationary, retain
      touch-action behavior, and verify the first and last notes can be reached at 390px without
      label collisions.
- [ ] **5.5.9 — Clarify Optimize setup.** Present Reference, Note and Fit Setup as numbered or
      otherwise ordered sections. Move bounds and specialist optimizer settings behind an
      accessible Advanced disclosure, shorten helper copy, and show API connectivity as a compact
      status element.
- [ ] **5.5.10 — Clarify Optimize execution and results.** Keep Start/Cancel with current job
      status, replace the pre-run chart with an intentional empty state, and group progress, chart,
      audition and download into one results area. Preserve reconnect-to-running-job behavior and
      all existing validation messages.
- [ ] **5.5.11 — Responsive and accessibility polish.** Check 390, 760, 1024 and 1440px layouts;
      add `prefers-reduced-motion`; verify contrast, touch targets, full keyboard traversal and
      visible focus. Run `npm --prefix web run typecheck`, `npm --prefix web run lint`, the UI tests
      and the screenshot suite, then record the final evidence here.

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

**Moved to the split-out repository.** Every bullet here is work on the plugin, and the plugin
now lives in [algo-glockenspiel-vst3](https://github.com/CWBudde/algo-glockenspiel-vst3); the
copy still sitting in `plugin/vst3/` is deleted by 6.3 below. Repairing it in both places means
repairing it twice and then reconciling the two, so it is repaired there. See that repository's
`PLAN.md`, Phase 1 (the MIDI port and the parameter layer) and Phase 2 (the CI that keeps them
building).

One thing this phase did produce, and it is worth knowing on this side: `plugin/vst3/params.go`
here is _ahead_ of the split-out copy — `numModes`/`numChebyshevGains`, the `DecayMsSearchMax`
knob ranges, the `model.TransposeToNote` delegation and the preset-drift test all landed against
this copy after the split. The other repository's Phase 1.1 pulls them across, so do not delete
`plugin/vst3/` in 6.3 before that has happened.

### Phase 6.3: Split and clean up

Goal: the `replace` directive and the plugin leave together.

- [ ] Move `plugin/vst3/`, `cmd/glockenspiel-vst3/` and `docs/vst3*.md` to their own repository,
      depending on this module normally. Half done: all three are copied into
      [algo-glockenspiel-vst3](https://github.com/CWBudde/algo-glockenspiel-vst3), and what is
      left here is the deletion. Sequence it after that repository's Phase 1.1, which pulls the
      6.2 fixes out of this copy before it goes.
- [ ] Reconcile the module path and tag a version. The module is `github.com/cwbudde/glockenspiel`
      while the repository is `CWBudde/algo-glockenspiel`, so `go get` cannot resolve it without
      a rename or a `go-import` meta tag, and there are no tags at all. Until both are fixed the
      split-out repository cannot drop its `replace github.com/cwbudde/glockenspiel =>
  ../algo-glockenspiel` or its `v0.0.0` placeholder require, which is this phase's fourth
      acceptance criterion measured from the other side.
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
      routing is hash-based, the Optimize loop, and why Pages cannot fit. `web/README.md`
      gained an Optimize section, and `docs/serve.md`'s "nothing under `web/` calls any of
      this yet" is no longer true and no longer says so.
- [ ] Retire `docs/vst3-evaluation.md` and `docs/vst3go-spike.md` with the 6.3 split. They are
      the two documents Phase 7.2 deliberately left alone: `docs/vst3go-spike.md:62` still lists
      `internal/model` in a package list, which is the last stale path in `docs/` and is fixed
      by the move rather than by an edit. Here that is a deletion — both files are already
      copied into the split-out repository, whose Phase 5 marks them historical rather than
      repairing them.
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

Phases 0, 1, 2 and 3 are closed. Phases 4, 5, 6 and 7 are open.

**Phase 2 is closed.** 2.1, 2.2, 2.3 and 2.4 are all done. The dead assembly is gone, every
layout an `.s` file assumes is pinned at compile time, the numeric contract is written down
with a harness that enforces it, three packed kernels — AVX2, SSE2, NEON — are registered in
`availableBackends()` (`internal/oscbank/contract_test.go`) and green on both CI runners, and
2.4 has its denormal scope, its per-note-on block buffers, its pooled voices and now its
cross-voice lane packing: `RealtimeEngine.ProcessBlock` renders every sounding voice through
`oscbank.VoiceBank`, one bank per `LaneWidth` lanes, with the lane held by the note rather
than by the slot. The one bullet still unticked under 2.4 is the optional two-sample step, and
it stays deferred rather than open: it would have to land on all four kernels at once or rule
one's bit-identity breaks.

The model work both closed items wanted — a bar that can be pointed at new parameters instead
of being rebuilt — is in place and exercised on the audio path: `Bar.UpdateParams`,
`BarParams.CopyInto`.

What lane packing did not fix, and Phase 2 does not cover: the per-voice excitation lowpass is
now the largest single term in a realtime block at 31%, the scalar 8-lane interleave is another
16%, and neither packs across voices. Both are follow-on work, not Phase 2 work.

The NEON row of the benchmark table in `docs/oscillator-bank.md` is closed too.
It is measured on a native Apple M5 rather than under qemu-user, with
`BenchmarkBank4x4Portable` from the same binary and the same run: 1319 ns/block against 7009,
a ratio of 5.3x rather than the amd64 7x, because the portable reference has no FMA on arm64.
`scripts/bench-remote.sh` (`just bench-arm64`, with `GLOCKENSPIEL_ARM64_HOST=user@host`)
rsyncs the tree to that host and runs the benchmark set there, so re-taking the row is one
command. It is worth re-taking on a quieter machine: macOS has no `taskset`, the host was in
use, and the doc says so.

Three defects behind the web demo's buzzing were fixed after Phase 2 closed, and two of them
were not where they looked. `RealtimeEngine.ProcessBlock` rendered only the first
`blockSize` frames of a wider callback, so the demo's 512-frame `ScriptProcessor` got 128
samples of note and 384 of silence — a 93.75 Hz gate. What was being gated was a DC offset:
the Chebyshev shaper summed `gains[k] * T_(k+1)(x)` without subtracting its own value at
zero, which is −0.3 for the shipped gains, and at the excitation stage that drove the
oscillator bank forever. Both are closed, and Phase 5.1's two level bullets are annotated
with what the DC had been hiding.

The third was `internal/wavio`: `go-audio/wav` decodes every sample format as an integer, so
a 32-bit IEEE float WAV came back as its own bit patterns divided by 2^31, which is a square
wave at about ±0.5 for any recording at a sane level. `glockenspiel fit --reference` reads
user references through that function, so every fit against a float WAV — what a DAW exports
by default — was fitting a square wave, and so was every legacy-reference regression test.
Fixed, with the fixture documented in `testdata/reference/README.md`.

Two things they left behind, both worth a look and neither blocking: the re-fitted preset
sits on three parameter bounds (`input_mix` at 2.0 and three amplitudes at ±2), which says
the model has no gain of its own and the fit spends the amplitude bound on level; and the
per-parameter recovery assertions in `TestOptimizationImprovesFitAgainstLegacyReference` had
to go, because a time-domain objective over a preset whose modes actually carry the signal is
sharp enough in mode frequency that a local search cannot walk a perturbation back.

Independent of all of that: **5.4** (baked wood textures and the printed key hints), then
**5.5** (the visual and responsive redesign that consumes those baked textures), and **7.3**
(a docs page for the web app, which now exists as `docs/web-app.md`, so what is left there is
the leftovers). Phase 5.5 is internally ordered so its screenshot baseline and design tokens
land before layout and material changes.

Phase 5.2 is closed: the Go module runs in a Web Worker, an `AudioWorkletNode` drains what it
renders, and `docs/audio-transport.md` records why that split and not one of the other three.
The one thing it costs is note-on latency — a message hop plus the queue, on the order of
15 ms — and the one thing it exposed is worth remembering: dropout counters measure the
harness as much as the transport unless the graph is connected after the producer starts.
