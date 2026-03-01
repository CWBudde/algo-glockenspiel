# Glockenspiel

`glockenspiel` is a physical-model glockenspiel instrument implemented in Go. It combines a modal bar resonator, nonlinear excitation, offline parameter fitting, and a VST3 plugin layer for DAW use.

The project started as a port of a legacy Pascal/Delphi glockenspiel model and now ships as both a command-line tool and a plugin-oriented synthesis engine. It supports note rendering, preset authoring, parameter optimization against recorded references, checkpointed fitting workflows, and polyphonic playback across the MIDI range.

## Features

- Four-mode physical bar model built around a quadrature decay oscillator.
- Nonlinear excitation with lowpass shaping and Chebyshev harmonic generation.
- CLI rendering to mono WAV with note scaling, velocity control, and auto-stop.
- Preset loading, validation, editing, and JSON round-tripping.
- Parameter fitting from reference WAV material.
- Multiple optimizers: Nelder-Mead for fast local refinement and Mayfly for broader search.
- Multiple distance metrics: RMS, log-RMS, and spectral distance.
- Checkpoint and resume support for long optimization runs.
- Legacy comparison coverage against exported reference renders.
- Polyphonic engine and full MIDI-range note support.
- VST3 wrapper with GUI parameter editing, preset browsing, and analysis views.

## Project Layout

```text
.
├── cmd/glockenspiel        # CLI entry point
├── assets/presets          # Built-in presets
├── internal/cli            # Cobra commands
├── internal/model          # Physical model and bar synthesis
├── internal/optimizer      # Objective functions, optimizers, checkpoints
├── internal/preset         # JSON preset I/O and validation
├── internal/synth          # Rendering engine and note orchestration
├── testdata                # Reference audio and preset fixtures
└── justfile                # Common development tasks
```

## Installation

Requirements:

- Go 1.25+
- `just` for the convenience tasks in [`justfile`](/mnt/projekte/Code/algo-glockenspiel/justfile)

Build the CLI:

```bash
just build
```

Install it into your Go bin directory:

```bash
just install
```

Build directly with Go:

```bash
go build -o bin/glockenspiel ./cmd/glockenspiel
```

## Quick Start

Render a note from the default preset:

```bash
glockenspiel synth \
  --preset assets/presets/default.json \
  --note 69 \
  --velocity 100 \
  --duration 3.0 \
  --output out/a4.wav
```

Fit a preset against a recorded note:

```bash
glockenspiel fit \
  --reference samples/a4.wav \
  --preset assets/presets/default.json \
  --output out/fitted-a4.json \
  --optimizer mayfly \
  --metric spectral \
  --max-iter 2000 \
  --checkpoint-interval 30s \
  --work-dir out/fit-a4
```

Resume a stopped optimization:

```bash
glockenspiel fit \
  --resume out/fit-a4/checkpoint_latest.json \
  --output out/fitted-a4.json
```

Print the version:

```bash
glockenspiel version
```

## CLI Reference

### `glockenspiel synth`

Render a single note to WAV.

Common flags:

- `--preset`: preset JSON file.
- `--output`: output WAV path.
- `--note`: MIDI note number.
- `--velocity`: MIDI velocity `0..127`.
- `--duration`: target render duration in seconds.
- `--sample-rate`: output sample rate in Hz.
- `--auto-stop`: stop once the tail decays below the configured threshold.
- `--decay-dbfs`: auto-stop threshold in dBFS.

### `glockenspiel fit`

Optimize model parameters to match a reference recording.

Common flags:

- `--reference`: input WAV to fit.
- `--preset`: starting preset.
- `--output`: destination preset JSON.
- `--note`: MIDI note number used during synthesis.
- `--velocity`: strike velocity used during synthesis.
- `--sample-rate`: synthesis sample rate.
- `--optimizer`: `simple` or `mayfly`.
- `--metric`: `rms`, `log-rms`, or `spectral`.
- `--max-iter`: iteration cap.
- `--time-budget`: wall-clock limit.
- `--report-every`: progress report interval.
- `--work-dir`: artifacts, checkpoints, and rendered comparisons.
- `--resume`: resume from a saved checkpoint.
- `--checkpoint-interval`: periodic checkpoint save interval.

Outputs:

- fitted preset JSON
- rendered fitted WAV
- progress/checkpoint artifacts in the work directory
- final similarity summary

### `glockenspiel version`

Print the build version.

## Presets

Presets are stored as JSON and validated on load and save. A preset contains metadata, the reference MIDI note, and the full bar parameter set.

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

Built-in presets live in [`assets/presets/default.json`](/mnt/projekte/Code/algo-glockenspiel/assets/presets/default.json).

## Optimization Workflow

The fitting pipeline is designed for both quick iteration and long-running searches:

1. Load a preset or checkpoint.
2. Encode model parameters into an optimization vector.
3. Render a candidate note through the synthesis engine.
4. Compare it to the reference using RMS, log-RMS, or spectral distance.
5. Update the search state with Nelder-Mead or Mayfly.
6. Persist checkpoints and the current best preset on schedule.
7. Export the final preset and a comparison render.

Use `simple` when you already have a strong initial preset and want faster convergence. Use `mayfly` when the search space is rough, the initial preset is weak, or spectral matching matters more than raw speed.

## Plugin and GUI

The VST3 build exposes the same synthesis engine to a DAW environment with:

- MIDI-triggered polyphonic playback
- parameter automation
- preset browser integration
- waveform and spectrum views
- note-range support across the full instrument

The GUI is intended for sound design and fit-result inspection rather than command-line batch work.

## Development

Common tasks from [`justfile`](/mnt/projekte/Code/algo-glockenspiel/justfile):

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

## Testing and Validation

The test suite covers:

- parameter validation
- preset round-trips
- oscillator correctness and numerical stability
- bar synthesis behavior
- synth integration
- CLI command behavior
- optimization infrastructure
- checkpoint/resume flows
- legacy render comparisons

Reference assets for regression coverage live under [`testdata/reference`](/mnt/projekte/Code/algo-glockenspiel/testdata/reference).

## Architecture Notes

The synthesis chain is:

1. Excitation impulse from note velocity.
2. Lowpass pre-emphasis filtering.
3. Chebyshev harmonic excitation.
4. Four parallel decaying resonant modes.
5. Dry/wet mix and final output.

The optimizer stack is separate from the audio model, which keeps synthesis deterministic and makes new search strategies or metrics straightforward to add.
