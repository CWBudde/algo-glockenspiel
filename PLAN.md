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

## Current Status (2026-03-01)

The repository is in a stable Phase 1 state:

- `go test ./...` passes.
- The synth path is implemented end-to-end:
  - parameter model
  - preset loading/saving
  - quadrature decay oscillator
  - bar model
  - synthesis engine
  - `glockenspiel synth`
- The `fit` command is still a placeholder that returns "not implemented yet".
- `internal/optimizer` only contains package docs; optimization infrastructure has not started.
- Legacy comparison support exists as an opt-in test (`internal/synth/legacy_compare_test.go`), but it requires:
  - `GLOCKENSPIEL_STRICT_LEGACY_COMPARE=1`
  - a rendered Go reference file at `testdata/output/go_synth_a4.wav`

### Recommended Resume Point

Resume at **Phase 2.1 Optimizer Infrastructure**. That is the first clearly unfinished implementation block and it unblocks the remaining `fit` command work.

Recommended order:

1. Add `internal/optimizer/optimizer.go` with the core interface and result/options types.
2. Add `internal/optimizer/params.go` for parameter encoding/decoding and bounds handling.
3. Add `internal/optimizer/objective.go` with RMS/log error metrics and the evaluation wrapper.
4. Implement a first `SimpleOptimizer` using Gonum Nelder-Mead.
5. Replace the `fit` CLI placeholder with a minimal working flow.

### Notes Before Resuming

- The plan below still reflects the original target architecture; use this status section as the source of truth for what is already done.
- Phase 1 manual listening checks and strict legacy similarity verification are still open.
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

  - [ ] Manual testing
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

- [ ] **Implement Nelder-Mead wrapper** (`internal/optimizer/simple.go`)
  - [ ] Import `gonum.org/v1/gonum/optimize`

  - [ ] Implement `SimpleOptimizer` struct
    - [ ] Implement `Optimize()` method
    - [ ] Configure Nelder-Mead settings
    - [ ] Set up progress reporting
    - [ ] Run optimization loop
    - [ ] Return result

  - [ ] Add progress callback
    - [ ] Print iteration, current cost, best cost
    - [ ] Track elapsed time
    - [ ] Respect report interval

  - [ ] Add convergence detection
    - [ ] Check if improvement < tolerance
    - [ ] Check max iterations
    - [ ] Check time budget

  - [ ] Write tests
    - [ ] Test on synthetic problem (known optimum)
    - [ ] Test progress reporting
    - [ ] Test early stopping

#### 2.4 Fit Command Implementation

- [ ] **Implement fit command** (`cmd/glockenspiel/cmd_fit.go`)
  - [ ] Define all CLI flags (reference, preset, output, note, velocity, sample-rate, optimizer, max-iter, time-budget, report-every, work-dir)

  - [ ] Implement `runFit()` function
    - [ ] Load reference WAV
    - [ ] Load initial preset (or create default)
    - [ ] Encode initial parameters
    - [ ] Create objective function
    - [ ] Create optimizer
    - [ ] Run optimization
    - [ ] Decode best parameters
    - [ ] Save fitted preset to JSON
    - [ ] Print final results (best cost, iterations, time)

  - [ ] Add intermediate checkpoints
    - [ ] Save best preset every N iterations
    - [ ] Save to `<work-dir>/checkpoint_<iter>.json`

  - [ ] Add final comparison
    - [ ] Synthesize with best params
    - [ ] Save as `<work-dir>/fitted_output.wav`
    - [ ] Print similarity metrics

  - [ ] Manual testing
    - [ ] Create synthetic reference (render with known params)
    - [ ] Run fit on synthetic reference
    - [ ] Verify recovered params are close
    - [ ] Test on real glockenspiel recording
    - [ ] Listen to fitted vs reference

#### 2.5 Testing & Validation

- [ ] **End-to-end optimization tests**
  - [ ] Create synthetic test case
    - [ ] Generate reference with known params
    - [ ] Add small amount of noise
    - [ ] Run optimizer
    - [ ] Verify recovery within tolerance

  - [ ] Test on real recordings
    - [ ] Use legacy Pascal exported WAV
    - [ ] Run optimizer
    - [ ] Compare optimized params to legacy params
    - [ ] Verify sonic similarity

  - [ ] Test parameter bounds
    - [ ] Verify optimizer respects bounds
    - [ ] Test with edge case initial conditions

  - [ ] Performance testing
    - [ ] Measure iterations per second
    - [ ] Measure time to convergence
    - [ ] Profile for bottlenecks

### Phase 3: Advanced Features

#### 3.1 Mayfly Optimizer

- [ ] **Port Mayfly optimizer from algo-piano** (`internal/optimizer/mayfly.go`)
  - [ ] Study algo-piano's Mayfly implementation
  - [ ] Port core Mayfly algorithm
  - [ ] Implement DESMA variant (recommended)
  - [ ] Add other variants (OLCE, MA, etc.)
  - [ ] Integrate with optimizer interface
  - [ ] Write unit tests
  - [ ] Compare performance vs Nelder-Mead

- [ ] **Update fit command**
  - [ ] Add `--optimizer mayfly` option
  - [ ] Add Mayfly-specific flags (population size, variant)
  - [ ] Test on same problems as Nelder-Mead
  - [ ] Document when to use each optimizer

#### 3.2 Spectral Distance Metric

- [ ] **Implement spectral comparison** (`internal/optimizer/spectral.go`)
  - [ ] Add FFT dependency (algo-fft)
  - [ ] Implement magnitude spectrum extraction
  - [ ] Implement spectral distance metric
    - [ ] Convert to dB scale
    - [ ] Weight by frequency (emphasize fundamentals)
    - [ ] Compute weighted difference

  - [ ] Add to objective function options
  - [ ] Add `--metric` flag to fit command (rms|log-rms|spectral)
  - [ ] Test spectral metric vs time-domain
  - [ ] Compare perceptual quality of results

#### 3.3 Checkpoint & Resume

- [ ] **Implement checkpoint system** (`internal/optimizer/checkpoint.go`)
  - [ ] Define checkpoint file format (JSON)
    - [ ] Current iteration
    - [ ] Best parameters so far
    - [ ] Best cost
    - [ ] Optimizer state (if applicable)
    - [ ] Timestamp

  - [ ] Implement checkpoint save
    - [ ] Save every N iterations or N seconds
    - [ ] Atomic write (temp file + rename)

  - [ ] Implement checkpoint load
    - [ ] Resume from checkpoint file
    - [ ] Restore optimizer state
    - [ ] Continue from iteration N

  - [ ] Add to fit command
    - [ ] Add `--resume` flag
    - [ ] Add `--checkpoint-interval` flag
    - [ ] Test resume works correctly

#### 3.4 Performance Optimization

- [ ] **Profile and optimize hot paths**
  - [ ] Run CPU profiler on fit command
  - [ ] Identify bottlenecks
  - [ ] Optimize oscillator processing
    - [ ] Consider SIMD (assembly or compiler hints)
    - [ ] Optimize coefficient recalculation

  - [ ] Optimize objective function
    - [ ] Minimize allocations
    - [ ] Reuse buffers
    - [ ] Consider parallel evaluation (if optimizer supports)

  - [ ] Benchmark improvements
    - [ ] Measure samples/second for synthesis
    - [ ] Measure evaluations/second for optimization
    - [ ] Compare before/after

#### 3.5 Documentation & Examples

- [ ] **Write comprehensive documentation**
  - [ ] Update README.md
    - [ ] Project overview
    - [ ] Installation instructions
    - [ ] Quick start guide
    - [ ] CLI reference

  - [ ] Write user guide
    - [ ] How to use synth command
    - [ ] How to use fit command
    - [ ] Parameter guide
    - [ ] Troubleshooting

  - [ ] Write developer guide
    - [ ] Architecture overview
    - [ ] Adding new optimizers
    - [ ] Adding new distance metrics

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
  - [ ] Evaluate Go VST3 libraries
  - [ ] Implement VST3 plugin skeleton
  - [ ] Integrate synthesis engine
  - [ ] Handle MIDI input
  - [ ] Implement parameter mapping
  - [ ] Test in DAW (Reaper, etc.)

- [ ] **GUI**
  - [ ] Choose GUI framework (web/WASM vs native)
  - [ ] Design parameter editor interface
  - [ ] Implement preset browser
  - [ ] Add visualization (waveform, spectrum)

- [ ] **Polyphony**
  - [ ] Implement voice allocation
  - [ ] Handle multiple simultaneous notes
  - [ ] Implement note-off handling
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
