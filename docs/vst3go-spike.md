# vst3go Spike Plan

Date: 2026-03-03

## Objective

Use `justyntemme/vst3go` for the first VST3 integration spike, while keeping the scope small enough to validate the wrapper before we commit the plugin architecture around it.

## Current Read

Upstream repository:

- https://github.com/justyntemme/vst3go

Important constraints from upstream documentation:

- `vst3go` is a thin Go wrapper around the Steinberg VST3 SDK via `cgo`.
- The project positions itself as Linux-focused today.
- The README indicates MIDI support is planned, which means the wrapper may not yet cover the event path needed for a full instrument plugin.

Important constraint from the actual `v0.1.1` module fetch:

- the published Go module currently does not include the `include/vst3/vst3_c_api.h` header tree referenced by its `cgo` sources, so importing `vst3go` directly fails to compile unless that packaging issue is worked around
- root cause: `vst3go` tracks `include/vst3` as a Git submodule that points to Steinberg's `vst3_c_api` repository, and Go module downloads do not include Git submodule contents

## Recommended Spike Scope

Build the first `vst3go` milestone as a minimal processor/controller shell, not as a full playable instrument.

That first milestone should prove:

1. the plugin can be built and loaded by a DAW or validator
2. parameter definitions and parameter changes can cross the host boundary
3. audio processing callbacks can call into Go-owned DSP safely
4. plugin packaging and discovery work on the target platform

## Deferred Until Wrapper Validation

Do not commit to these in the first spike:

- MIDI note-on/note-off handling
- polyphony
- preset browser UI
- full editor GUI
- cross-platform plugin packaging

## Proposed Repository Shape

When implementation begins, prefer a small dedicated package tree:

```text
plugin/
  vst3/
    README.md
    processor.go
    controller.go
    params.go
```

Keep the DSP reusable by adapting existing packages rather than moving them:

- `internal/model`
- `internal/synth`
- `internal/preset`

## Audio-Thread Rules

The plugin layer must follow stricter runtime rules than the CLI:

- no allocations in the real-time processing callback
- no file I/O in the processing callback
- no logging in the processing callback
- parameter updates must be lock-free or staged outside the audio callback
- voice state and work buffers must be preallocated

## Next Implementation Step

Create the `plugin/vst3` package skeleton and wire only enough host-facing metadata and parameter plumbing to test whether `vst3go` is sufficient for this repository.

Status:

- completed for the repository-owned parameter layer and Linux/`cgo` scaffold
- kept behind the `vst3go` build tag until the upstream header packaging issue is fixed or replaced with a local fork/replace

Most likely next integration fix:

- vendor or otherwise fetch the Steinberg `vst3_c_api` headers separately into a local fork or local replacement of `vst3go`
