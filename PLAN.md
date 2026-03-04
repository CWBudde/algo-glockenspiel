# Glockenspiel Physical Model - Design Document

## Overview

This document describes the design for a Go implementation of a physical model glockenspiel synthesizer, based on a legacy Pascal/Delphi implementation. The core algorithm uses decaying quadrature oscillators with soft distortion to model the resonant bars of a glockenspiel.

## Goals

1. Port the legacy glockenspiel physical model to Go
2. Integrate with the existing algo-dsp library for standard DSP operations
3. Provide a CLI tool for synthesis and parameter optimization
4. Support single-note modeling initially (extensible to full MIDI range later)
5. Maintain sonic fidelity to the original implementation

## Architecture Overview

### Project Structure

```
glockenspiel/
├── cmd/
│   └── glockenspiel/        # Main CLI entry point
│       └── main.go
├── internal/
│   ├── model/               # Core synthesis model
│   │   ├── decay_osc.go    # Quadrature decay oscillator (ported)
│   │   ├── bar.go          # Complete bar model
│   │   └── params.go       # Parameter definitions
│   ├── preset/              # JSON preset handling
│   │   ├── preset.go
│   │   └── preset_test.go
│   ├── synth/               # Synthesis engine
│   │   ├── synth.go
│   │   └── synth_test.go
│   └── optimizer/           # Fitting algorithms
│       ├── optimizer.go     # Interface
│       ├── simple.go        # Nelder-Mead or similar
│       └── mayfly.go        # Mayfly algorithm (phase 2)
├── assets/
│   └── presets/
│       └── default.json     # Default parameters
├── testdata/
│   └── reference/           # Reference audio for testing
│       └── glockenspiel_a.wav
├── go.mod
└── README.md
```

### Module Dependencies

- `github.com/spf13/cobra` - CLI framework
- `github.com/cwbudde/algo-dsp` - DSP primitives (filters, distortion)
- `github.com/go-audio/wav` - WAV file I/O
- `gonum.org/v1/gonum/optimize` - Optimization algorithms
- Standard library for JSON, math, etc.

### Core Design Principles

1. **Separation of concerns**: Model, synthesis, optimization are independent
2. **Testability**: Each component can be tested in isolation
3. **Fidelity**: Core decay oscillator ported directly from legacy
4. **Integration**: Use algo-dsp for standard DSP operations
5. **Simplicity**: Single-note model, fixed 4 modes to start

## Core Components

### 1. Quadrature Decay Oscillator

**Location:** `internal/model/decay_osc.go`
**Source:** Ported from legacy Pascal `TQuadDecayOscillator`

```go
type QuadDecayOscillator struct {
    // State for 4 parallel oscillators
    realState [4]float64  // Real parts (cosine phase)
    imagState [4]float64  // Imaginary parts (sine phase)

    // Parameters (set per mode)
    amplitude [4]float64
    frequency [4]float64  // Hz
    decay     [4]float64  // decay time constant in samples

    // Computed coefficients
    decayFactor [4]float64
    cosCoeff    [4]float64
    sinCoeff    [4]float64

    sampleRate  float64
}
```

**Key Methods:**

- `ProcessSample32(input float32) float32` - Process single sample
- `ProcessBlock32(input, output []float32)` - Process block efficiently
- `Reset()` - Clear oscillator state
- `SetFrequency(mode int, freq float64)` - Update frequency and recompute coefficients
- `SetDecay(mode int, decayMs float64)` - Update decay time

**Algorithm:**

- Complex oscillator using rotation matrix to advance phase
- Applies exponential decay factor each sample
- Excitation signal kicks the oscillators, which then ring down
- Processes 4 modes simultaneously for efficiency

### 2. Bar Model

**Location:** `internal/model/bar.go`
**Purpose:** Integrates ported oscillator with algo-dsp components

```go
type Bar struct {
    oscillator  *QuadDecayOscillator
    lowpass     *biquad.Section        // from algo-dsp
    distortion  *effects.Distortion    // Chebyshev from algo-dsp

    params      *BarParams
    sampleRate  int

    // Temp buffers for processing
    excitationBuf []float32
    filteredBuf   []float32
}
```

**Processing Chain:**

1. Excitation impulse → lowpass filter (pre-emphasis)
2. Filtered signal → Chebyshev distortion (harmonic excitation)
3. Distorted signal → quad decay oscillator (modal resonance)
4. Sum all 4 modes → output

### 3. Parameters

**Location:** `internal/model/params.go`

```go
type BarParams struct {
    // Global
    InputMix        float64  // Dry/wet mix
    FilterFreq      float64  // Lowpass cutoff (Hz)
    BaseFrequency   float64  // Fundamental (Hz)

    // Per-mode (4 modes)
    Modes [4]ModeParams

    // Chebyshev distortion
    Chebyshev ChebyshevParams
}

type ModeParams struct {
    Amplitude  float64  // Linear gain
    Frequency  float64  // Hz (can be inharmonic)
    DecayTime  float64  // Milliseconds
}

type ChebyshevParams struct {
    Enabled       bool
    HarmonicGains []float64  // Gain per harmonic
}
```

### 4. Preset Handler

**Location:** `internal/preset/preset.go`

```go
type Preset struct {
    Version    string      `json:"version"`
    Name       string      `json:"name"`
    Note       int         `json:"note"`        // MIDI note this preset is for
    Parameters BarParams   `json:"parameters"`
}
```

**Functions:**

- `Load(path string) (*Preset, error)` - Load from JSON file
- `Save(preset *Preset, path string) error` - Save to JSON file
- `Validate(preset *Preset) error` - Validate parameters

## Data Flow

### Synthesis Pipeline

```
1. Excitation Generation
   ├─ Create impulse (velocity → amplitude)
   └─ Optional: delay by fractional samples for timing

2. Pre-emphasis Filter (algo-dsp biquad)
   ├─ Lowpass filter at params.FilterFreq
   └─ Shapes the excitation spectrum

3. Harmonic Excitation (algo-dsp Chebyshev)
   ├─ Apply Chebyshev polynomials
   ├─ Generate harmonics with individual gains
   └─ Creates rich excitation signal

4. Modal Resonance (ported QuadDecayOscillator)
   ├─ Excitation feeds all 4 modes
   ├─ Each mode: freq, amplitude, decay
   ├─ Complex oscillators ring down exponentially
   └─ Process in blocks for efficiency

5. Mix & Output
   ├─ Sum all 4 mode outputs
   ├─ Add InputMix * excitation (dry signal)
   └─ Write to output buffer
```

### Optimization Data Flow

```
1. Load Reference WAV
   └─ Single note recording to match

2. Initialize Optimizer
   ├─ Define parameter bounds
   ├─ Set initial guess (or from preset)
   └─ Configure objective function

3. Optimization Loop (Nelder-Mead / Mayfly)
   ├─ Generate candidate parameters
   ├─ Synthesize with candidate
   ├─ Compare to reference (distance metric)
   ├─ Update best parameters
   └─ Repeat until converged or max iterations

4. Save Best Preset
   └─ Write optimized params to JSON
```

**Distance Metric:** RMS error or spectral distance (like algo-piano)

### Block Processing Strategy

For efficiency, process audio in blocks (e.g., 128 samples):

- Amortizes function call overhead
- Better CPU cache utilization
- Matches legacy implementation style
- Follows algo-piano pattern

## CLI Structure (Cobra)

### Root Command

```bash
glockenspiel [command]
```

### Subcommands

#### 1. `synth` - Synthesize a note

```bash
glockenspiel synth [flags]

Flags:
  --preset string      Preset JSON file (default "assets/presets/default.json")
  --output string      Output WAV file (default "output.wav")
  --note int          MIDI note number (default 69, A4)
  --velocity int      MIDI velocity 0-127 (default 100)
  --duration float    Duration in seconds (default 3.0)
  --sample-rate int   Sample rate in Hz (default 48000)
  --auto-stop         Stop when sound decays below threshold
  --decay-dbfs float  Auto-stop threshold in dBFS (default -90.0)

Example:
  glockenspiel synth --preset my-glock.json --note 72 --output c5.wav
```

#### 2. `fit` - Optimize parameters to match reference

```bash
glockenspiel fit [flags]

Flags:
  --reference string     Reference WAV file (required)
  --preset string        Initial preset JSON (default "assets/presets/default.json")
  --output string        Output fitted preset JSON (required)
  --note int            MIDI note number (default 69)
  --velocity int        MIDI velocity for synthesis (default 100)
  --sample-rate int     Sample rate (default 48000)
  --optimizer string    Optimizer: simple|mayfly (default "simple")
  --max-iter int        Max iterations (default 1000)
  --time-budget float   Time budget in seconds (default 120.0)
  --report-every int    Print progress every N iterations (default 10)
  --work-dir string     Directory for temp files (default "out/fit")

Example:
  glockenspiel fit --reference samples/a4.wav --output fitted-a4.json
```

#### 3. `version` - Show version info

```bash
glockenspiel version
```

## Parameter Model & JSON Format

### JSON Preset Format

```json
{
  "version": "1.0",
  "name": "Default Glockenspiel",
  "note": 69,
  "parameters": {
    "input_mix": 0.472,
    "filter_frequency": 522.9,
    "base_frequency": 440.0,
    "modes": [
      {
        "amplitude": 0.886,
        "frequency": 1756.6,
        "decay_ms": 188.2
      },
      {
        "amplitude": 1.995,
        "frequency": 4768.1,
        "decay_ms": 1.603
      },
      {
        "amplitude": -0.465,
        "frequency": 38.24,
        "decay_ms": 5.559
      },
      {
        "amplitude": 0.364,
        "frequency": 32.63,
        "decay_ms": 8.682
      }
    ],
    "chebyshev": {
      "enabled": true,
      "harmonic_gains": [1.0, 0.5, 0.3, 0.2]
    }
  }
}
```

### Parameter Ranges (for optimization)

Based on legacy optimizer bounds:

```go
type ParamBounds struct {
    InputMix      [2]float64  // [0.0, 2.0]
    FilterFreq    [2]float64  // [20.0, 20000.0] Hz, log scale

    // Per mode
    Amplitude     [2]float64  // [-2.0, 2.0]
    FrequencyMult [2]float64  // [0.5, 10.0] * base_frequency
    DecayMs       [2]float64  // [0.1, 500.0] milliseconds

    // Chebyshev (optional, phase 2)
    HarmonicGain  [2]float64  // [0.0, 2.0] per harmonic
}
```

### Default Initialization Strategy

1. **From legacy hardcoded values** - Use the optimized values from Pascal code as defaults
2. **Frequency scaling** - For different notes, scale mode frequencies proportionally
3. **Decay scaling** - Higher notes decay faster (scale by frequency ratio)

## Optimization Strategy

### Phase 1: Simple Optimizer (Nelder-Mead)

**Why Nelder-Mead:**

- No gradient needed (objective function isn't differentiable)
- Well-tested, reliable
- Good for ~10-20 parameters
- Available in Go: `gonum.org/v1/gonum/optimize`

**Objective Function:**

```go
func ObjectiveFunction(params []float64, ref []float32) float64 {
    // 1. Convert params to BarParams
    // 2. Synthesize audio with current params
    // 3. Truncate to min(synth_len, ref_len)
    // 4. Compute distance metric
    // 5. Return cost (lower is better)
}
```

**Distance Metrics:**

1. **Time-domain RMS error** (simple, fast)

   ```
   cost = sqrt(sum((synth[i] - ref[i])^2) / N)
   ```

2. **Log-scaled error** (like legacy)

   ```
   cost = log10(1e-20 + rmsError) - costScale
   ```

3. **Spectral distance** (like algo-piano, more perceptually relevant)
   ```
   // FFT both signals
   // Compare magnitude spectra
   // Weight by frequency (more weight on fundamentals)
   ```

**Recommendation:** Start with log-scaled RMS (matches legacy), add spectral option later.

### Phase 2: Mayfly Optimizer

**Port from algo-piano (`optimizer/mayfly.go`):**

- More sophisticated than Nelder-Mead
- Better for noisy/multimodal objectives
- Proven to work well for audio synthesis fitting
- Supports multiple variants (DESMA, OLCE, etc.)

**Interface:**

```go
type Optimizer interface {
    Optimize(
        objective ObjectiveFunc,
        bounds ParamBounds,
        initial []float64,
        opts OptimizeOptions,
    ) (*Result, error)
}

type ObjectiveFunc func(params []float64) float64

type Result struct {
    BestParams []float64
    BestCost   float64
    Iterations int
    Elapsed    time.Duration
}
```

### Optimization Progress Reporting

```
Iteration 10: cost=2.345e-3, best=1.234e-3 (5.2s elapsed)
Iteration 20: cost=1.123e-3, best=9.876e-4 (10.5s elapsed)
...
Converged after 156 iterations, best cost: 8.234e-4
```

### Checkpoint Saving

- Save best preset every N iterations
- Allow resume from checkpoint
- Useful for long optimizations

## Testing Strategy

### Unit Tests

**1. Quadrature Decay Oscillator Tests** (`internal/model/decay_osc_test.go`)

- Test basic oscillation (frequency, decay)
- Test reset clears state
- Test numerical stability (no divergence, NaN, Inf)
- Benchmark block processing throughput

**2. Bar Model Tests** (`internal/model/bar_test.go`)

- Test synthesis produces non-zero output
- Test parameter updates work correctly
- Test sample rate changes
- Verify processing chain order

**3. Preset Tests** (`internal/preset/preset_test.go`)

- Test JSON load/save round-trip
- Test validation catches bad params
- Test default preset is valid

### Integration Tests

**1. Synth Command Test**

- Run: `glockenspiel synth --output test.wav`
- Verify: WAV file created, correct length, non-silent

**2. Fit Command Test**

- Generate synthetic reference with known params
- Run optimizer
- Verify recovered params are close to ground truth

### Verification Against Legacy

**Golden Reference Approach:**

1. **Export from Pascal:** Use legacy VST to render test notes, save as WAV
2. **Render in Go:** Use same parameters in Go implementation
3. **Compare:** Waveforms should be very similar (not bit-exact due to floating-point, but close)

```go
func TestVsLegacy(t *testing.T) {
    legacyWav := loadWAV("testdata/legacy_a4.wav")
    goWav := synthesizeWithParams(legacyParams, len(legacyWav))

    // Compute correlation or RMS difference
    similarity := computeSimilarity(legacyWav, goWav)
    assert.Greater(t, similarity, 0.95) // 95% similar
}
```

### Test Data

```
testdata/
├── reference/
│   ├── glockenspiel_a4.wav      # Real glockenspiel recording
│   ├── legacy_synth_a4.wav      # Legacy Pascal output
│   └── params_legacy_a4.json     # Legacy parameters
└── presets/
    ├── minimal.json              # Minimal valid preset
    └── edge_cases.json           # Edge case parameters
```

## Implementation Phases

## Current Status (2026-03-02)

The repository is beyond the original Phase 1 checkpoint:

- `go test ./...` passes.
- The synth path is implemented end-to-end:
  - parameter model
  - preset loading/saving
  - quadrature decay oscillator
  - bar model
  - synthesis engine
  - `glockenspiel synth`
- The optimizer stack is implemented:
  - optimizer interface and progress/result types
  - parameter encoding/decoding and bounds handling
  - RMS, log-RMS, and spectral objective metrics
  - Nelder-Mead and Mayfly optimizers
  - checkpoint save/load and resume support
- The `fit` command is implemented end-to-end:
  - reference WAV loading
  - optimizer execution
  - checkpoint writing
  - fitted preset export
  - rendered comparison WAV output
- Automated optimizer coverage exists for synthetic recovery, bounds handling, checkpoint/resume, and legacy-reference fitting.
- Legacy comparison support also exists as an opt-in synth regression test (`internal/synth/legacy_compare_test.go`), but it requires:
  - `GLOCKENSPIEL_STRICT_LEGACY_COMPARE=1`
  - a rendered Go reference file at `testdata/output/go_synth_a4.wav`

### Recommended Resume Point

Resume at **Phase 2.4 Manual Fit Testing**. The remaining gap is validating the implemented `fit` workflow manually against synthetic and recorded references, then recording the results in this plan.

Recommended order:

1. Create a synthetic reference WAV from a known preset.
2. Run `glockenspiel fit` against that synthetic reference.
3. Verify the recovered preset is materially close to the source preset.
4. Run `glockenspiel fit` against a recorded/legacy reference WAV in `testdata/reference/`.
5. Compare the rendered fit output against the reference and capture listening notes.

### Notes Before Resuming

- The plan below still reflects the original target architecture; use this status section as the source of truth for what is already done.
- Manual listening checks are still open until they are run and documented.
- The strict legacy comparison test currently skips unless the external reference artifact has been rendered and placed in `testdata/output/`.

### Phase 0: Project Setup

- [x] **Initialize Go module**
  - [x] Run `go mod init github.com/cwbudde/glockenspiel`
  - [x] Add dependencies: cobra, algo-dsp, wav, gonum
  - [x] Set up basic project structure (cmd/, internal/, assets/, testdata/)

- [x] **Create basic cobra CLI skeleton**
  - [x] Implement root command
  - [x] Add placeholder `synth` subcommand
  - [x] Add placeholder `fit` subcommand
  - [x] Add `version` subcommand
  - [x] Verify CLI structure works (`go run cmd/glockenspiel/main.go --help`)

- [x] **Set up test infrastructure**
  - [x] Create testdata directory structure
  - [x] Add sample WAV file for testing
  - [x] Set up basic test runner

### Phase 1: Core Model & Synthesis

#### 1.1 Parameter Structures

- [x] **Define parameter types** (`internal/model/params.go`)
  - [x] Implement `ModeParams` struct (amplitude, frequency, decay)
  - [x] Implement `ChebyshevParams` struct (enabled, harmonic gains)
  - [x] Implement `BarParams` struct (input mix, filter freq, base freq, modes, chebyshev)
  - [x] Add parameter validation functions
  - [x] Add parameter bounds constants
  - [x] Write unit tests for validation

#### 1.2 Preset Handling

- [x] **Implement preset loader/saver** (`internal/preset/preset.go`)
  - [x] Define `Preset` struct with JSON tags
  - [x] Implement `Load(path string) (*Preset, error)`
  - [x] Implement `Save(preset *Preset, path string) error`
  - [x] Implement `Validate(preset *Preset) error`
  - [x] Add JSON marshaling/unmarshaling tests
  - [x] Create default preset JSON file (`assets/presets/default.json`)
    - [x] Extract parameters from legacy Pascal code
    - [x] Convert to JSON format
    - [x] Test loading default preset

#### 1.3 Quadrature Decay Oscillator (Core Algorithm)

- [x] **Port QuadDecayOscillator** (`internal/model/decay_osc.go`)
  - [x] Define `QuadDecayOscillator` struct
    - [x] State arrays: realState[4], imagState[4]
    - [x] Parameter arrays: amplitude[4], frequency[4], decay[4]
    - [x] Coefficient arrays: decayFactor[4], cosCoeff[4], sinCoeff[4]
    - [x] Sample rate field

  - [x] Implement coefficient calculation
    - [x] `calculateCoefficients()` - compute decay, cos, sin coefficients
    - [x] Handle sample rate changes

  - [x] Implement core processing
    - [x] `ProcessSample32(input float32) float32` - single sample processing
    - [x] Port rotation matrix algorithm from Pascal
    - [x] Apply decay factor
    - [x] Sum all 4 modes

  - [x] Implement block processing
    - [x] `ProcessBlock32(input, output []float32)` - efficient block processing
    - [x] Optimize for 4-mode SIMD-friendly layout

  - [x] Implement parameter setters
    - [x] `SetFrequency(mode int, freq float64)`
    - [x] `SetAmplitude(mode int, amp float64)`
    - [x] `SetDecay(mode int, decayMs float64)`
    - [x] `SetSampleRate(sr float64)`

  - [x] Implement utility methods
    - [x] `Reset()` - clear all state
    - [x] `MaxDecayFactor() float64` - for auto-stop calculation

  - [x] Write unit tests
    - [x] Test single mode oscillation (verify frequency)
    - [x] Test decay envelope (verify exponential decay)
    - [x] Test reset clears state
    - [x] Test numerical stability (long runs, no divergence)
    - [x] Benchmark single-sample vs block processing
    - [x] Test edge cases (zero frequency, very long/short decay)

#### 1.4 Bar Model (Integration Layer)

- [x] **Implement Bar model** (`internal/model/bar.go`)
  - [x] Define `Bar` struct
    - [x] Add QuadDecayOscillator field
    - [x] Add algo-dsp lowpass filter field
    - [x] Add algo-dsp distortion field (Chebyshev)
    - [x] Add BarParams field
    - [x] Add temp buffers (excitationBuf, filteredBuf)

  - [x] Implement constructor
    - [x] `NewBar(params *BarParams, sampleRate int) *Bar`
    - [x] Initialize oscillator with parameters
    - [x] Initialize lowpass filter from algo-dsp
    - [x] Initialize Chebyshev distortion from algo-dsp
    - [x] Allocate processing buffers

  - [x] Implement synthesis pipeline
    - [x] `Synthesize(velocity int, numSamples int) []float32`
      - [x] Generate excitation impulse (velocity -> amplitude)
      - [x] Apply lowpass filter (pre-emphasis)
      - [x] Apply Chebyshev distortion (harmonic generation)
      - [x] Feed to decay oscillator
      - [x] Mix with dry signal (input_mix parameter)
      - [x] Return output buffer

  - [x] Implement parameter updates
    - [x] `UpdateParams(params *BarParams)` - update all parameters
    - [x] Handle filter frequency changes
    - [x] Handle oscillator parameter changes

  - [x] Write unit tests
    - [x] Test synthesis produces non-zero output
    - [x] Test parameter updates work
    - [x] Test sample rate changes
    - [x] Test processing chain order (disable stages individually)
    - [x] Test with various velocities

#### 1.5 Synthesis Engine

- [x] **Implement synthesis engine** (`internal/synth/synth.go`)
  - [x] Define `Synthesizer` struct
    - [x] Add Bar field
    - [x] Add sample rate, block size fields

  - [x] Implement `NewSynthesizer(preset *Preset, sampleRate int) *Synthesizer`

  - [x] Implement `RenderNote(note, velocity int, duration float64) []float32`
    - [x] Calculate number of samples from duration
    - [x] Process in blocks (e.g., 128 samples)
    - [x] Handle auto-stop if enabled (RMS decay detection)

  - [x] Implement auto-stop logic
    - [x] `shouldStop(block []float32, threshold float64) bool`
    - [x] Track consecutive below-threshold blocks

  - [x] Write integration tests
    - [x] Test rendering produces correct length
    - [x] Test auto-stop works
    - [x] Test different durations

#### 1.6 Synth Command Implementation

- [x] **Implement synth command** (`cmd/glockenspiel/cmd_synth.go`)
  - [x] Define all CLI flags (preset, output, note, velocity, duration, sample-rate, auto-stop, decay-dbfs)
  - [x] Implement `runSynth()` function
    - [x] Load preset from file
    - [x] Create synthesizer
    - [x] Render note
    - [x] Convert float32 to WAV format
    - [x] Write WAV file
    - [x] Print summary (duration, samples, file size)

  - [x] Add error handling
    - [x] Handle missing preset file
    - [x] Handle invalid parameters
    - [x] Handle WAV write errors

  - [x] Manual testing
    - [x] Run `glockenspiel synth --output test.wav`
    - [ ] Listen to output WAV
    - [ ] Verify parameters affect sound as expected
    - [x] Test auto-stop functionality

#### 1.7 Legacy Verification

- [ ] **Verify against legacy implementation**
  - [x] Export test WAV from legacy Pascal VST
    - [x] Use exact parameters from legacy code
    - [x] Render A4 (440 Hz) note
    - [x] Save as `testdata/reference/legacy_synth_a4.wav`

  - [x] Render same note in Go implementation
    - [x] Use same parameters in JSON preset
    - [x] Save as `testdata/output/go_synth_a4.wav`

  - [ ] Implement comparison test
    - [x] Load both WAVs
    - [x] Compute RMS difference
    - [x] Compute correlation coefficient
    - [ ] Assert similarity > 95%

  - [ ] Debug any differences
    - [ ] Plot waveforms side-by-side
    - [ ] Check coefficient calculations
    - [ ] Verify processing chain matches

### Phase 2: Simple Optimization

#### 2.1 Optimizer Infrastructure

- [x] **Define optimizer interface** (`internal/optimizer/optimizer.go`)
  - [x] Define `Optimizer` interface
  - [x] Define `ObjectiveFunc` type
  - [x] Define `Result` struct (best params, cost, iterations, elapsed time)
  - [x] Define `OptimizeOptions` struct (max iterations, time budget, report interval)

- [x] **Define parameter encoding/decoding** (`internal/optimizer/params.go`)
  - [x] Implement parameter encoding for `BarParams`
    - [x] Flatten BarParams to float64 slice
    - [x] Apply log scaling for frequency parameters
    - [x] Apply appropriate scaling for all params

  - [x] Implement parameter decoding back to `BarParams`
    - [x] Convert flat array back to BarParams
    - [x] Apply inverse scaling
    - [x] Clamp to valid ranges

  - [x] Define `ParamBounds` struct
  - [x] Implement bounds checking and mirroring

  - [x] Write unit tests
    - [x] Test encode/decode round-trip
    - [x] Test bounds enforcement

#### 2.2 Objective Function

- [x] **Implement objective function** (`internal/optimizer/objective.go`)
  - [x] Define `ObjectiveFunction` struct
    - [x] Reference audio field
    - [x] Template preset/codec fields
    - [x] Sample rate, note, velocity fields

  - [x] Implement RMS error metric
    - [x] `ComputeRMSError(synth, ref []float32) float64`
    - [x] Handle length mismatch (truncate to shorter)
    - [x] Compute sample-by-sample squared error
    - [x] Return sqrt(mean(errors))

  - [x] Implement log-scaled error (like legacy)
    - [x] `ComputeLogError(synth, ref []float32) float64`
    - [x] Apply log10 to RMS error
    - [x] Subtract cost scale factor

  - [x] Implement objective wrapper
    - [x] `Evaluate(encoded []float64) float64`
    - [x] Decode parameters
    - [x] Synthesize audio
    - [x] Compute error metric
    - [x] Return cost

  - [x] Write unit tests
    - [x] Test with identical signals (cost should be near zero)
    - [x] Test with known different signals
    - [x] Test edge cases (silence, very loud)

#### 2.3 Nelder-Mead Optimizer

- [x] **Implement Nelder-Mead wrapper** (`internal/optimizer/simple.go`)
  - [x] Import `gonum.org/v1/gonum/optimize`

  - [x] Implement `SimpleOptimizer` struct
    - [x] Implement `Optimize()` method
    - [x] Configure Nelder-Mead settings
    - [x] Set up progress reporting
    - [x] Run optimization loop
    - [x] Return result

  - [x] Add progress callback
    - [x] Print iteration, current cost, best cost
    - [x] Track elapsed time
    - [x] Respect report interval

  - [x] Add convergence detection
    - [x] Check if improvement < tolerance
    - [x] Check max iterations
    - [x] Check time budget

  - [x] Write tests
    - [x] Test on synthetic problem (known optimum)
    - [x] Test progress reporting
    - [x] Test early stopping

#### 2.4 Fit Command Implementation

- [x] **Implement fit command** (`internal/cli/fit.go`)
  - [x] Define all CLI flags (reference, preset, output, note, velocity, sample-rate, optimizer, max-iter, time-budget, report-every, work-dir)

  - [x] Implement `runFit()` function
    - [x] Load reference WAV
    - [x] Load initial preset (or create default)
    - [x] Encode initial parameters
    - [x] Create objective function
    - [x] Create optimizer
    - [x] Run optimization
    - [x] Decode best parameters
    - [x] Save fitted preset to JSON
    - [x] Print final results (best cost, iterations, time)

  - [x] Add intermediate checkpoints
    - [x] Save best preset every N iterations
    - [x] Save to `<work-dir>/checkpoint_<iter>.json`

  - [x] Add final comparison
    - [x] Synthesize with best params
    - [x] Save as `<work-dir>/fitted_output.wav`
    - [x] Print similarity metrics

  - [ ] Manual testing
    - [x] Create synthetic reference (render with known params)
    - [x] Run fit on synthetic reference
    - [x] Verify recovered params are close
    - [x] Test on real glockenspiel recording
    - [x] Listen to fitted vs reference
    - Note: manual CLI runs were started on 2026-03-02 under `out/manual-fit/`. Compare each fitted WAV against the exact reference it was fit to:
      `out/manual-fit/default_reference.wav` vs `out/manual-fit/default-fit-after-fix/fitted_output.wav`,
      `testdata/reference/legacy_synth_a4.wav` vs `out/manual-fit/legacy-fit/fitted_output.wav`,
      `testdata/reference/glockenspiel_a4.wav` vs `out/manual-fit/recorded-fit/fitted_output.wav`.
      The shortest synthetic files were generated as fast diagnostics for the fit path; the most meaningful listening check is the recorded-reference pair. Manual listening on 2026-03-02 found the fitted output pretty close to the reference.

#### 2.5 Testing & Validation

- [x] **End-to-end optimization tests**
  - [x] Create synthetic test case
    - [x] Generate reference with known params
    - [x] Add small amount of noise
    - [x] Run optimizer
    - [x] Verify recovery within tolerance

  - [x] Test on real recordings
    - [x] Use legacy Pascal exported WAV
    - [x] Run optimizer
    - [x] Compare optimized params to legacy params
    - [x] Verify sonic similarity

  - [x] Test parameter bounds
    - [x] Verify optimizer respects bounds
    - [x] Test with edge case initial conditions

  - [x] Performance testing
    - [x] Add benchmark coverage for objective evaluation throughput
    - [x] Measure iterations per second
    - [x] Measure time to convergence
    - [x] Profile for bottlenecks
    - Note: benchmark measurements recorded on 2026-03-02 in `internal/optimizer/perf_test.go`:
      objective RMS `313.1 eval/s`, log `416.6 eval/s`, spectral `374.8 eval/s`;
      short legacy optimization `77.63 iter/s`, `200.5 eval/s`, `154.6 convergence-ms`.
      CPU profiling on 2026-03-02 (`BenchmarkSimpleOptimizeLegacyShort`) identified the main hot paths as
      AVX2 biquad block processing in `algo-dsp` (~67% flat CPU),
      `(*Bar).ProcessExcitation` cumulative work (~88%),
      `applyChebyshev` (~6%),
      and oscillator sample/block processing (~7% combined).

### Phase 3: Advanced Features

#### 3.1 Mayfly Optimizer

- [x] **Port Mayfly optimizer from algo-piano** (`internal/optimizer/mayfly.go`)
  - [x] Study algo-piano's Mayfly implementation
  - [x] Port core Mayfly algorithm
  - [x] Implement DESMA variant (recommended)
  - [x] Add other variants (OLCE, MA, etc.)
  - [x] Integrate with optimizer interface
  - [x] Write unit tests
  - [x] Compare performance vs Nelder-Mead
  - Note: short legacy benchmark on 2026-03-02 in `internal/optimizer/perf_test.go` measured
    `simple` at `85.47 iter/s`, `220.8 eval/s`, `140.4 convergence-ms`, `3.56 MB/op`,
    and `mayfly` (`desma`, population 10) at `19.98 iter/s`, `939.9 eval/s`, `1001 convergence-ms`, `38.4 MB/op`.
    `mayfly` explores more candidates per second but is materially heavier and slower to converge in this local-refinement benchmark.

- [x] **Update fit command**
  - [x] Add `--optimizer mayfly` option
  - [x] Add Mayfly-specific flags (population size, variant)
  - [x] Test on same problems as Nelder-Mead
  - [x] Document when to use each optimizer

#### 3.2 Spectral Distance Metric

- [x] **Implement spectral comparison** (`internal/optimizer/spectral.go`)
  - [x] Add FFT dependency (algo-fft)
  - [x] Implement magnitude spectrum extraction
  - [x] Implement spectral distance metric
    - [x] Convert to dB scale
    - [x] Weight by frequency (emphasize fundamentals)
    - [x] Compute weighted difference

  - [x] Add to objective function options
  - [x] Add `--metric` flag to fit command (rms|log|spectral)
  - [x] Test spectral metric vs time-domain
  - [x] Compare perceptual quality of results
  - Note: recorded-reference comparison on 2026-03-02 (`testdata/reference/glockenspiel_a4.wav`) using
    `simple` + `rms`, `log`, and `spectral` is saved under `out/phase3-metric-compare/`.
    `rms` and `log` converged to the same fitted preset/output.
    `spectral` converged to a different fit with worse time-domain error on that problem and a slightly lower first-mode frequency than the `rms`/`log` fit, so it remains an alternate metric rather than the default recommendation.

#### 3.3 Checkpoint & Resume

- [x] **Implement checkpoint system** (`internal/optimizer/checkpoint.go`)
  - [x] Define checkpoint file format (JSON)
    - [x] Current iteration
    - [x] Best parameters so far
    - [x] Best cost
    - [x] Optimizer state (if applicable)
    - [x] Timestamp

  - [x] Implement checkpoint save
    - [x] Save every N iterations or N seconds
    - [x] Atomic write (temp file + rename)

  - [x] Implement checkpoint load
    - [x] Resume from checkpoint file
    - [x] Restore optimizer state
    - [x] Continue from iteration N
    - Note: checkpoints now persist coarse optimizer resume state.
      Resume restores optimizer identity, metric, best encoded parameters, remaining iteration budget,
      and Mayfly variant/population/seed when not explicitly overridden.
      Full internal simplex/population snapshots are still not persisted.

  - [x] Add to fit command
    - [x] Add `--resume` flag
    - [x] Add `--checkpoint-interval` flag
    - [x] Test resume works correctly

#### 3.4 Performance Optimization

- [x] **Profile and optimize hot paths**
  - [x] Run CPU profiler on fit command
  - [x] Identify bottlenecks
  - [x] Optimize oscillator processing
    - Note: `QuadDecayOscillator.ProcessBlock32` was specialized as a fixed 4-mode block loop on 2026-03-02 to remove per-sample call overhead and repeated field indexing.
    - Note: first AVX2 SIMD path added on 2026-03-02 for the 4-mode oscillator block loop, using runtime CPU feature detection and an amd64 assembly kernel that vectorizes across modes.
    - Note: AVX2 dispatch was also added for the common 4-harmonic Chebyshev waveshaper path, so the hot `Chebyshev(4) -> oscillator` chain now has SIMD coverage in both stages on amd64.
    - Note: an experimental fused AVX2 path was added on 2026-03-02 for the common `Chebyshev(4 harmonics) + InputMix==0` case, but current benchmarks show it is much slower than the separate AVX2 Chebyshev + AVX2 oscillator path (`~29-39us/op` fused vs `~2.0-2.3us/op` separate on a 512-sample block), so it is currently left out of the runtime dispatch and kept only as a benchmarked prototype.
    - [x] Optimize coefficient recalculation
    - Note: `Bar.UpdateParams` now uses a batched oscillator mode setter so each mode recomputes coefficients once instead of separately in `SetFrequency` and `SetDecay`.
    - Note: further SIMD experimentation remains possible, but the current amd64 runtime path already includes hand-written AVX2 kernels for the main oscillator block loop.

  - [x] Optimize objective function
    - [x] Minimize allocations
    - [x] Reuse buffers
    - [x] Add SIMD dispatch for the RMS error reduction on amd64 (`sumSquaredDiffAVX2Asm`)
    - Note: parallel evaluation is still future work; the current optimizers evaluate candidates sequentially.

  - [x] Benchmark improvements
    - [x] Measure samples/second for synthesis
    - [x] Measure evaluations/second for optimization
    - [x] Compare before/after
    - Note: profiled `glockenspiel fit` on `testdata/reference/glockenspiel_a4.wav` showed the same dominant hotspot before and after optimization:
      AVX2 biquad block filtering in `algo-dsp`.
      After specializing `ProcessBlock32`, the same `simple`+`rms` fit run dropped from about `391ms` to `341ms` by iteration 40 and from about `818ms` to `477ms` by iteration 80, with unchanged fit quality (`best=0.570441`).
      After adding AVX2 SIMD for the oscillator block loop and the common 4-harmonic Chebyshev path, the same profiled run dropped further to about `220ms` by iteration 40 and `330ms` by iteration 80, still with unchanged fit quality (`best=0.570442`).
      After batching oscillator mode updates to avoid redundant coefficient recalculation, the same profiled run reached about `166ms` by iteration 40 and `325ms` by iteration 80, again with unchanged fit quality (`best=0.570442`).
      A later experimental fused AVX2 Chebyshev+oscillator prototype regressed badly in isolation (`~29-39us/op` vs `~2.0-2.3us/op` for the separate path) and also changed the recorded-reference fit trajectory (`~98ms` by iteration 40, `~0.69s` wall time for 80 iterations, `best=0.606879`), so it is currently not dispatched in the main synthesis path.
      In the latest profile, `algo-dsp` biquad filtering remains the dominant cost (~65% flat CPU), while the new AVX2 oscillator kernel accounts for ~9% flat CPU.

#### 3.5 Documentation & Examples

- [ ] **Write comprehensive documentation**
  - [x] Update README.md
    - [x] Project overview
    - [x] Installation instructions
    - [x] Quick start guide
    - [x] CLI reference

  - [x] Write user guide
    - [x] How to use synth command
    - [x] How to use fit command
    - [x] Parameter guide
    - [x] Troubleshooting

  - [x] Write developer guide
    - [x] Architecture overview
    - [x] Adding new optimizers
    - [x] Adding new distance metrics
    - Note: the current developer guide lives in `README.md` (project layout, development, architecture) and `docs/user-guide.md` (optimizer/metric behavior and fit workflow), rather than a separate standalone developer-guide document.

  - [ ] Create examples
    - [ ] Example presets for different sounds
    - [ ] Example optimization workflows
    - [ ] Example scripts

- [ ] **Code documentation**
  - [ ] Add godoc comments to all public APIs
  - [ ] Add package-level documentation
  - [ ] Generate and review godoc output

### Phase 4: VST Plugin (Future - Not in Initial Scope)

This phase is for future work after the CLI tool is complete and stable.

- [ ] **VST3 Wrapper**
  - [x] Evaluate Go VST3 libraries
    - Note: initial research completed on 2026-03-03 in `docs/vst3-evaluation.md`.
    - Note: current recommendation is to use the Steinberg VST3 SDK/C API as the source of truth and prototype the first Go plugin skeleton with `vst3go`, falling back to a local `cgo` bridge if the wrapper blocks required functionality.
    - Note: `vst3go` is now the selected first spike target; see `docs/vst3go-spike.md`. Its current README still lists MIDI support as planned, so the first milestone should validate processor/controller and parameter plumbing before committing to full instrument behavior.
  - [x] Implement VST3 plugin skeleton
    - Note: initial package skeleton created in `plugin/vst3/` with stable parameter IDs, parameter specs, and `BarParams` mapping helpers.
    - Note: first `vst3go` scaffold added on 2026-03-03 in `plugin/vst3/` and `cmd/glockenspiel-vst3/`, but it is currently guarded behind a `vst3go` build tag because the original published `github.com/justyntemme/vst3go v0.1.1` module is missing the VST3 C headers referenced by its `cgo` sources.
    - Note: upstream repository inspection on 2026-03-03 indicates those headers live under `include/vst3` as a Git submodule pointing at Steinberg's `vst3_c_api` repository, which explains why `go get` did not fetch them. The likely next step is a local fork/`replace` or separate vendoring of the header tree.
    - Note: on 2026-03-04 the project was switched to `replace github.com/cwbudde/vst3go => ../vst3go`, the fork submodule `include/vst3` was initialized successfully, and the current Linux/`cgo` spike now passes `go test -tags=vst3go ./plugin/vst3` and `go build -tags=vst3go ./cmd/glockenspiel-vst3`.
  - [x] Integrate synthesis engine
    - Note: on 2026-03-04 the Linux/`cgo` `vst3go` spike was connected to the existing Go synthesis engine. The current processor builds persistent per-note `model.Bar` resonators from plugin parameters and streams summed mono output to the plugin's stereo buses.
  - [x] Handle MIDI input
    - Note: the current spike handles VST3 MIDI note-on plus all-notes-off/all-sound-off control changes, with sample-offset-aware block segmentation inside the processor. Note-off currently does not silence a resonator; bars continue decaying naturally after the initial strike.
  - [x] Implement parameter mapping
    - Note: stable VST-facing parameter IDs/specs plus `Snapshot <-> model.BarParams` mapping are implemented in `plugin/vst3/params.go`, and the processor rebuilds resonators from that mapped state.
  - [ ] Test in DAW (Reaper, etc.)

- [ ] **GUI**
  - [ ] Choose GUI framework (web/WASM vs native)
  - [ ] Design parameter editor interface
  - [ ] Implement preset browser
  - [ ] Add visualization (waveform, spectrum)

- [ ] **Polyphony**
  - [x] Implement voice allocation
    - Note: the current processor uses a simple plugin-local polyphony cap (`16`) with oldest-voice stealing.
  - [x] Handle multiple simultaneous notes
    - Note: active per-note `model.Bar` resonators are mixed together into the stereo output buffers.
  - [x] Implement note-off handling
    - Note: note-off is intentionally non-gating in the current model; a struck bar continues to decay after the impulse, and the processor only retires a resonator after repeated quiet blocks.
  - [ ] Optimize for real-time performance

- [ ] **Multi-note Support**
  - [ ] Extend to full MIDI range
  - [ ] Per-note presets vs frequency scaling
  - [ ] Smooth parameter interpolation

## Open Questions & Future Work

1. **Multi-note support:** How to extend to full MIDI range?
   - Option A: Per-note presets (most flexible)
   - Option B: Shared params with frequency scaling (simpler)

2. **Real-time performance:** Can we optimize for low-latency audio?
   - SIMD optimizations for oscillator
   - Investigate Go audio libraries for real-time callback

3. **Polyphony:** How to handle multiple simultaneous notes?
   - Voice allocation strategy
   - Mixing multiple bars

4. **GUI:** Future GUI for preset editing?
   - Web-based (WASM)?
   - Native (fyne, gio)?

## References

- Legacy Pascal implementation: `./legacy/Source/GlockenspielDSP.pas`
- Legacy optimizer: `./legacy/Optimizer/MainUnit.pas`
- algo-piano project: `../algo-piano`
- algo-dsp library: `../algo-dsp`

## Approval

This design has been approved and is ready for implementation planning.
