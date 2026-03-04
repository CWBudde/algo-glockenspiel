# VST3 Evaluation

Date: 2026-03-03

This note captures the initial Phase 4 research for bringing the Go synthesis engine into a DAW plugin.

## Goal

Find the most practical path to a VST3 plugin without rewriting the DSP core in C++.

## Constraints From This Repository

- DSP and synthesis are already implemented in Go.
- The current product surface is CLI-first and headless.
- Phase 4 only needs a plugin skeleton first; GUI can stay separate.
- Real-time audio safety matters more than abstraction purity.

## Options Considered

### 1. Steinberg VST3 SDK C API via `cgo`

Primary sources:

- https://github.com/steinbergmedia/vst3sdk
- https://steinbergmedia.github.io/vst3_doc/

What it gives us:

- official VST3 interfaces and host expectations
- the lowest-risk compatibility target for DAWs that expect VST3
- direct control over factory, processor, controller, MIDI/event handling, and parameter wiring

Tradeoffs:

- more boilerplate in this repository
- native build and packaging work is fully ours
- careful `cgo` boundary design is required for real-time callbacks

Assessment:

This is the most predictable technical base if we want to own the integration layer and keep surprises low.

### 2. `vst3go`

Primary sources:

- original upstream: https://github.com/justyntemme/vst3go
- current fork in use for this repository: https://github.com/CWBudde/vst3go

What it appears to provide:

- Go bindings around the VST3 C API
- code generation and examples for building VST3 components in Go
- a path to keeping most plugin logic in Go rather than in a C++ shell
- a Linux-first, `cgo`-based plugin path with included VST3 headers

Tradeoffs:

- small ecosystem and limited adoption surface compared with the official SDK itself
- if the wrapper is incomplete for our use case, we still fall back to SDK-level work
- we still inherit `cgo` and native plugin packaging constraints
- its README currently lists MIDI support as planned rather than complete, which is a direct gap for an instrument plugin

Assessment:

This is the most relevant Go-specific starting point. It is worth prototyping first if the examples build cleanly, but it should be treated as a thin convenience layer, not as a strategic dependency we cannot replace. The current feature status suggests starting with a headless processor skeleton or fixed-strike test plugin before committing to full MIDI-driven instrument behavior.

### 3. Alternative plugin format: CLAP

Primary sources:

- https://github.com/free-audio/clap
- https://github.com/justyntemme/clapgo

What it gives us:

- simpler C ABI than classic C++ plugin SDKs
- a Go wrapper exists
- useful fallback if we decide VST3 integration complexity is too high

Tradeoffs:

- Phase 4 in this plan is explicitly VST3-oriented
- DAW support is good but not universal in the same way VST3 is expected to be

Assessment:

Worth keeping in reserve, but not the first target for this repository.

## Recommendation

Start with a minimal VST3 processor/controller prototype using the official Steinberg SDK model and evaluate `vst3go` as the implementation accelerator.

Concretely:

1. Use the Steinberg SDK documentation and C API as the source of truth.
2. Attempt the first plugin skeleton with `vst3go`.
3. If the wrapper blocks required functionality, replace only that layer with a local `cgo` bridge instead of changing the DSP code.

This keeps the DSP in Go, avoids premature GUI work, and limits lock-in to a small third-party wrapper. It also leaves room to pivot quickly if `vst3go` cannot yet supply the MIDI/event path we need for a real instrument.

## Proposed Next Tasks

1. Create a Phase 4 spike branch with a minimal plugin target that exposes one processor and one controller.
2. Define the parameter surface for a first plugin version:
   - note trigger model
   - preset selection/load
   - core bar parameters
   - output gain
3. Decide whether the first plugin is:
   - instrument-only with MIDI note events
   - effect-style test harness that renders from a fixed strike
   - a temporary no-MIDI instrument shell that exposes parameters first while event support is validated
4. Add a tiny architecture note for real-time rules at the Go boundary:
   - no allocations in audio callbacks
   - no logging on the audio thread
   - preallocate voices and buffers
   - avoid blocking calls and file I/O
