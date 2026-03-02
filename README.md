# Glockenspiel

`glockenspiel` is a Go implementation of a physical-model glockenspiel synthesizer. It started as a port of a legacy Pascal/Delphi model and currently ships as a CLI for note rendering and offline preset fitting.

## Status

Implemented today:

- four-mode bar model with quadrature decay oscillators
- preset load/save/validation
- note rendering to mono WAV
- offline fitting against reference WAVs
- RMS, log-RMS, and spectral objective metrics
- Nelder-Mead and Mayfly optimizer backends
- checkpoint and resume support
- legacy-reference regression tests

Not implemented in this repository:

- VST or DAW plugin support
- GUI tooling
- full internal optimizer-state resume

## Project Layout

```text
.
├── cmd/glockenspiel        # CLI entry point
├── assets/presets          # Built-in presets
├── internal/cli            # Cobra commands
├── internal/model          # Physical model and bar synthesis
├── internal/optimizer      # Objectives, optimizers, checkpoints
├── internal/preset         # JSON preset I/O and validation
├── internal/synth          # Rendering engine
├── testdata                # Reference audio and preset fixtures
└── justfile                # Common development tasks
```

## Requirements

- Go 1.25+
- optional: `just`
- optional: `treefmt`, `golangci-lint`

## Build

With `just`:

```bash
just build
```

Directly:

```bash
go build -o bin/glockenspiel ./cmd/glockenspiel
```

## Quick Start

Render a note:

```bash
glockenspiel synth \
  --preset assets/presets/default.json \
  --note 69 \
  --velocity 100 \
  --duration 2.0 \
  --sample-rate 44100 \
  --output out/a4.wav
```

Fit a preset against a reference note:

```bash
glockenspiel fit \
  --reference testdata/reference/legacy_synth_a4.wav \
  --preset assets/presets/default.json \
  --output out/fitted-a4.json \
  --optimizer simple \
  --metric spectral \
  --max-iter 100 \
  --time-budget 30 \
  --work-dir out/fit-a4
```

Resume from the latest checkpoint in a work directory:

```bash
glockenspiel fit \
  --reference testdata/reference/legacy_synth_a4.wav \
  --preset assets/presets/default.json \
  --output out/fitted-a4.json \
  --work-dir out/fit-a4 \
  --resume
```

Resume restores the saved best parameter vector, optimizer/metric selection, remaining iteration budget, and Mayfly settings when present. It does not restore a full internal simplex or full Mayfly population snapshot.

For a more complete walkthrough, see [docs/user-guide.md](/mnt/projekte/Code/algo-glockenspiel/docs/user-guide.md).

## CLI

### `glockenspiel synth`

Renders a single note to a mono WAV file.

Important flags:

- `--preset`: preset JSON path
- `--output`: output WAV path
- `--note`: MIDI note number
- `--velocity`: MIDI velocity `0..127`
- `--duration`: render duration in seconds
- `--sample-rate`: output sample rate
- `--auto-stop`: stop when the tail decays below threshold
- `--decay-dbfs`: auto-stop threshold in dBFS

### `glockenspiel fit`

Optimizes bar parameters against a mono reference WAV.

Important flags:

- `--reference`: input WAV to match
- `--preset`: starting preset JSON
- `--output`: fitted preset output path
- `--note`: MIDI note used during synthesis
- `--velocity`: strike velocity used during synthesis
- `--sample-rate`: reference/render sample rate
- `--optimizer`: `simple` or `mayfly`
- `--metric`: `rms`, `log`, or `spectral`
- `--max-iter`: iteration limit
- `--time-budget`: time limit in seconds
- `--report-every`: progress print interval
- `--checkpoint-interval`: checkpoint write interval in progress iterations, `0` disables intermediate checkpoint writes
- `--work-dir`: directory for checkpoints and rendered comparison output
- `--resume`: resume from the latest `checkpoint_*.json` in `work-dir`
- `--mayfly-variant`: `ma|desma|olce|eobbma|gsasma|mpma|aoblmoa`
- `--mayfly-pop`: Mayfly population size
- `--mayfly-seed`: Mayfly random seed

Outputs:

- fitted preset JSON at `--output`
- rendered best-fit WAV at `<work-dir>/fitted_output.wav`
- checkpoint files at `<work-dir>/checkpoint_*.json`

### `glockenspiel version`

Prints the build version.

## Presets

Presets are JSON files with metadata plus the full bar parameter set. The reference note stored in the preset is used as the scaling origin for rendering other MIDI notes.

Example:

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
      { "amplitude": 0.886, "frequency": 1756.6, "decay_ms": 188.2 },
      { "amplitude": 1.995, "frequency": 4768.1, "decay_ms": 1.603 },
      { "amplitude": -0.465, "frequency": 38.24, "decay_ms": 5.559 },
      { "amplitude": 0.364, "frequency": 32.63, "decay_ms": 8.682 }
    ],
    "chebyshev": {
      "enabled": true,
      "harmonic_gains": [1.0, 0.5, 0.3, 0.2]
    }
  }
}
```

See [default.json](/mnt/projekte/Code/algo-glockenspiel/assets/presets/default.json).

## Fitting Workflow

The fitting loop is:

1. Load a preset and reference WAV.
2. Encode bar parameters into an optimization vector.
3. Render a candidate note.
4. Compare the candidate against the reference with the selected metric.
5. Update the search with Nelder-Mead or Mayfly.
6. Persist checkpoints and the current best preset.
7. Write the final preset and a rendered comparison WAV.

Use `simple` when you already have a reasonable starting preset and want predictable local refinement. Use `mayfly` when the initial preset is weak or the search surface is rough.

## Development

Common tasks:

```bash
just fmt
just test
just lint
just bench
just ci
```

Direct Go commands:

```bash
go test ./...
go test -race ./...
go test -run=^$ -bench=. -benchmem ./...
```

## Testing

The repository includes tests for:

- parameter validation
- preset round-trips
- oscillator stability
- bar and synth integration
- CLI behavior
- optimizer infrastructure
- checkpoint/resume flows
- synthetic and legacy reference fitting

Reference audio lives in [testdata/reference](/mnt/projekte/Code/algo-glockenspiel/testdata/reference).

## Architecture

Synthesis chain:

1. excitation impulse from velocity
2. lowpass pre-emphasis
3. Chebyshev harmonic excitation
4. four decaying resonant modes
5. dry/wet mix and output

The optimizer layer is kept separate from the synthesis engine so new metrics and search strategies can be added without changing the core model.
