# Glockenspiel Plan

## Status

Core CLI synthesis and fitting are implemented and tested.
Current remaining work is mostly validation, documentation, and next-step plugin hardening.

## Phase 1: Validate The Existing CLI/Fit Stack

Goal: prove the current synth/fit implementation is reliable enough to treat as the stable baseline.

### Phase 1.1: Manual Fit Validation

Tasks:

- Re-run synthetic-reference fit checks from a known preset.
- Re-run recorded-reference fit checks, especially the recorded A4 path.
- Listen to reference vs fitted output for the meaningful cases, not just short diagnostics.
- Capture a short written summary of what matched, what did not, and which metric/optimizer was used.

Acceptance criteria:

- [ ] Synthetic fit run completed and results captured.
- [ ] Recorded-reference fit run completed and results captured.
- [ ] Listening notes written down in repo docs.
- [ ] A recommended default fit workflow is stated clearly enough for reuse.

### Phase 1.2: Legacy Verification

Tasks:

- Finish the strict legacy comparison path.
- Decide the actual numeric acceptance threshold for similarity.
- Verify the reference artifact setup needed by the strict test is reproducible.
- If the strict check still fails, isolate whether the mismatch is in coefficients, excitation chain, or test/reference setup.

Acceptance criteria:

- [ ] Strict legacy comparison test has a defined pass condition.
- [ ] Required reference artifacts and env vars are documented.
- [ ] Legacy comparison is either passing or has a narrowed, documented blocker.

### Phase 1.3: Fit Workflow Robustness

Tasks:

- Re-check checkpoint/resume on a longer fit run.
- Confirm optimizer/metric combinations behave as documented.
- Decide which combinations are recommended defaults vs experimental alternatives.

Acceptance criteria:

- [ ] Checkpoint/resume verified on a non-trivial run.
- [ ] Recommended optimizer/metric combinations documented.
- [ ] Any still-risky combinations are explicitly called out.

## Phase 2: Documentation Cleanup

Goal: make the implemented system understandable and usable without digging through commit history or scratch notes.

### Phase 2.1: API Documentation

Tasks:

- Add missing godoc comments on exported types and functions.
- Add package-level docs where the public surface is non-trivial.
- Review public packages for inconsistent or stale comments.

Acceptance criteria:

- [ ] Exported public APIs have usable godoc comments.
- [ ] Public-facing packages have package docs where needed.
- [ ] Comments reflect current behavior rather than earlier design intent.

### Phase 2.2: User-Facing Examples

Tasks:

- Add a few example presets with clear intent.
- Add at least one documented synth workflow example.
- Add at least one documented fit workflow example.
- Add helper scripts only where they reduce repeated manual setup.

Acceptance criteria:

- [ ] Example presets exist and are explained briefly.
- [ ] Synth example workflow exists.
- [ ] Fit example workflow exists.
- [ ] Any added scripts are small, repo-appropriate, and documented.

### Phase 2.3: Results Documentation

Tasks:

- Move important findings out of ad hoc `out/` notes into durable docs.
- Summarize current fit quality, legacy-comparison status, and recommended usage.
- Keep the write-up short and operational, not historical.

Acceptance criteria:

- [ ] Important validation findings are stored in repo docs.
- [ ] The docs identify what is proven, what is still approximate, and what remains open.

## Phase 3: Performance Follow-Up

Goal: only spend more time on optimization if profiling shows real headroom on current workloads.

### Phase 3.1: Re-Profile Current Fit Path

Tasks:

- Re-run profiling on the current `fit` path.
- Confirm whether the main hotspots are still the same.
- Compare against earlier benchmark notes before starting new low-level work.

Acceptance criteria:

- [ ] Current profile captured.
- [ ] Dominant hotspots identified.
- [ ] Decision made: stop here or continue optimization.

### Phase 3.2: Targeted Optimization If Justified

Tasks:

- Pursue more SIMD or parallel evaluation only if profiling justifies it.
- Keep optimization scoped to measured bottlenecks.
- Re-benchmark after each material change.

Acceptance criteria:

- [ ] Each optimization change is tied to a measured hotspot.
- [ ] Before/after benchmark evidence exists.
- [ ] No speculative optimization work is left undocumented.

## Phase 4: VST3 Spike Validation

Goal: determine whether the current plugin spike is viable enough to become a real product path.

### Phase 4.1: DAW Validation

Tasks:

- Test the current plugin in at least one DAW.
- Verify plugin loading, parameter updates, note triggering, and audio output stability.
- Check whether current note handling and quiet-voice retirement behave acceptably in practice.

Acceptance criteria:

- [ ] Plugin loads in a DAW.
- [ ] Basic note playback works.
- [ ] Parameter changes work during use.
- [ ] Major blocking integration issues are documented.

### Phase 4.2: Runtime Viability

Tasks:

- Measure whether current real-time performance is acceptable.
- Identify xruns, latency issues, or parameter-update glitches.
- Decide whether `vst3go` remains the right base or whether a local bridge is needed.

Acceptance criteria:

- [ ] Real-time performance has been evaluated.
- [ ] The main runtime risks are documented.
- [ ] A concrete direction for the VST3 implementation has been chosen.

### Phase 4.3: Post-Spike Expansion

Tasks:

- Extend behavior beyond the current spike only after DAW validation is solid.
- Prioritize full MIDI-range handling, parameter smoothing, and additional plugin polish.
- Defer GUI/editor work until the processing path is stable.

Acceptance criteria:

- [ ] Expansion work is gated on successful DAW validation.
- [ ] Next plugin milestones are ordered clearly.

## Deferred

- richer preset library
- broader multi-note modeling strategy outside the current plugin spike
- polished plugin GUI/editor
- any major architectural expansion before Phases 1 and 4 are closed

## Resume Point

Start with Phase 1.1 if the focus is the CLI/model path.
Start with Phase 4.1 if the focus is the VST/plugin path.
