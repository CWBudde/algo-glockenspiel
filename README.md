# Glockenspiel

A browser-playable glockenspiel and the Go toolchain behind it.

The instrument is not a physical model. Its core is a runtime-configurable oscillator bank (`internal/oscbank`): `N` independent decaying quadrature oscillators, each carrying up to `M` integer-multiple harmonic partials, in an AoSoA `float32` layout with packed SIMD kernels for AVX2, SSE2 and NEON. Both counts are ordinary runtime configuration, not compile-time constants. `M` is a ceiling rather than a quota: every mode chooses its own partial count, and a mode with no harmonic list is a single fundamental. There is no `sin()` in the inner loop.

**Play it: <https://cwbudde.github.io/algo-glockenspiel/>**

The same engine also ships as a CLI for rendering notes to WAV and for fitting preset parameters against a reference recording.

## The Web App

The browser build compiles the Go engine to WebAssembly and drives it from a small JavaScript mixer. It gives you a piano-aligned instrument view spanning MIDI 36 to 96, playable by pointer or by computer keyboard, with a volume dial and a wood-species selector.

Run it locally:

```bash
./scripts/build-wasm.sh
python3 -m http.server -d web 8080
```

Then open `http://localhost:8080`.

`scripts/build-wasm.sh` compiles `cmd/glockenspiel-wasm` to `web/dist/glockenspiel.wasm` and copies the toolchain's `wasm_exec.js` into `web/`, next to `index.html` rather than next to the `.wasm` file. The GitHub Pages deployment runs the same script from `.github/workflows/deploy-pages.yml`.

See [web/README.md](web/README.md) for the front-end details.

Known rough edges, tracked in [PLAN.md](PLAN.md) as Phase 5: audio runs on a `ScriptProcessorNode` rather than an `AudioWorklet`, the volume dial does not affect an already-ringing note, and the bottom two octaves of the keyboard are over-unity with an inverted right channel.

## The CLI

### Build

```bash
just build
```

Or directly:

```bash
go build -o bin/glockenspiel ./cmd/glockenspiel
```

All flags of both commands are documented in [docs/user-guide.md](docs/user-guide.md); what follows is the short version.

### `glockenspiel synth`

Renders a single note to a mono WAV file.

```bash
glockenspiel synth \
  --preset assets/presets/default.json \
  --note 69 \
  --velocity 100 \
  --duration 2.0 \
  --sample-rate 44100 \
  --output out/a4.wav
```

Flags: `--preset` (omit it to use the preset embedded in the binary), `--output`, `--note`, `--velocity`, `--duration`, `--sample-rate`, `--auto-stop`, `--decay-dbfs`.

### `glockenspiel fit`

Optimizes bar parameters against a mono reference WAV.

```bash
glockenspiel fit \
  --reference testdata/reference/legacy_synth_a4.wav \
  --preset assets/presets/default.json \
  --output out/fitted-a4.json \
  --optimizer simple \
  --metric spectral \
  --max-iter 100 \
  --time-budget 30s \
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

Flags: `--reference`, `--preset`, `--bounds`, `--output`, `--note`, `--velocity`, `--sample-rate`, `--optimizer` (`simple` or `mayfly`), `--metric` (`rms`, `log` or `spectral`), `--max-iter`, `--time-budget` (a Go duration such as `30s` or `10m`; a bare number is read as seconds), `--align`, `--normalize-gain`, `--report-every`, `--checkpoint-interval`, `--work-dir`, `--resume`, `--mayfly-variant`, `--mayfly-pop`, `--mayfly-seed`.

It writes the fitted preset to `--output`, the best-fit render to `<work-dir>/fitted_output.wav` and checkpoints to `<work-dir>/checkpoint_*.json`.

Resume restores the saved best parameter vector, the optimizer and metric selection, the remaining iteration budget and the Mayfly settings when present. It does not restore a full internal simplex or a full Mayfly population snapshot.

### `glockenspiel version`

Prints the build version.

## Presets

Presets are JSON files holding metadata plus the full bar parameter set. The reference note stored in the preset is the scaling origin for rendering any other MIDI note.

Two schema versions exist side by side. **v1** (`"version": "1.0"`) carries exactly four modes, no per-mode harmonics and a Chebyshev shaper that always sits on the excitation. **v2** (`"version": "2.0"`) adds a variable-length mode array — one to 512 modes — per-mode harmonic partials of up to 64 per mode, and an explicit shaper stage. A v1 document is held to the v1 rules, so a file that quietly grew v2 fields is reported rather than rendered differently than its version claims. Saving preserves the version a preset was loaded with; converting is explicit, through `preset.Upgrade`.

A v2 preset:

```json
{
  "version": "2.0",
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
        "decay_ms": 188.2,
        "harmonics": [1.0, 0.4, 0.1]
      },
      { "amplitude": 1.995, "frequency": 4768.1, "decay_ms": 1.603 }
    ],
    "chebyshev": {
      "enabled": true,
      "stage": "excitation",
      "harmonic_gains": [1.0, 0.5, 0.3, 0.2]
    }
  }
}
```

The shipped preset is [assets/presets/default.json](assets/presets/default.json), a v1 document.

## Project Layout

```text
.
├── cmd/glockenspiel        # CLI entry point
├── cmd/glockenspiel-wasm   # WebAssembly entry point for the web app
├── cmd/glockenspiel-vst3   # VST3 entry point (linux + cgo + tag vst3go)
├── web/                    # The web app: HTML, CSS, JS mixer, wasm_exec.js
├── model/                  # Public bar model: parameters, Chebyshev shaper, Bar
├── internal/oscbank        # The oscillator bank and its SIMD kernels
├── internal/cpufeat        # Runtime CPU feature detection
├── internal/synth          # Note rendering and the realtime voice engine
├── internal/preset         # Preset JSON schema v1/v2, load, save, validate
├── internal/optimizer      # Objectives, optimizers, checkpoints
├── internal/cli            # Cobra commands
├── plugin/vst3             # VST3 plugin layer (see docs/vst3-evaluation.md)
├── assets/presets          # Built-in presets, embedded into the binary
├── docs/                   # Design notes and the user guide
├── scripts/                # WASM build and test helpers
├── testdata/               # Reference audio and preset fixtures
└── justfile                # Common development tasks
```

## Requirements

- Go 1.25+
- optional: `just`
- optional: `treefmt` and `prettier` for formatting, `golangci-lint` for linting
- optional: `python3` to serve the web app locally

## Development

```bash
just fmt      # format everything through treefmt, Markdown and JS included
just test     # go test ./...
just lint     # golangci-lint
just bench    # go test -run=^$ -bench=. -benchmem ./...
just ci       # check-formatted, test, lint, check-tidy
```

`just check-tidy` currently fails on its own: `go.mod` carries a `replace` directive for `github.com/cwbudde/vst3go` that is unresolvable without a sibling checkout. Retiring it is Phase 6.3. CI therefore runs the other three checks, plus a WASM build and unit tests on amd64 at `GOAMD64=v1` and `v3` and on arm64.

The `-tags=vst3go` plugin build does not compile today. Without the sibling checkout the `replace` directive points at, it fails to resolve the dependency at all; with it, `plugin/vst3/processor_vst3go_linux.go` is out of date against the current `vst3go` MIDI API. Repairing it and giving it a CI job is Phase 6.2.

## Architecture

The signal chain for one note:

1. an excitation impulse scaled by velocity
2. lowpass pre-emphasis
3. an optional Chebyshev waveshaper, on the excitation or on the output
4. the oscillator bank: every mode expands into one rotor per harmonic partial
5. dry/wet mix, and hard clipping on the realtime path

The optimizer layer is kept separate from the synthesis engine so new metrics and search strategies can be added without touching the core.

Two documents go deeper:

- [docs/oscillator-bank.md](docs/oscillator-bank.md) — the recursion, the AoSoA layout, the three packed kernels, the numeric contract they are held to, and measured performance.
- [docs/user-guide.md](docs/user-guide.md) — the full CLI walkthrough, including `--bounds` and how to choose an optimizer and a metric.

[PLAN.md](PLAN.md) tracks what is done and what is not.

## Testing

```bash
go test ./...
go test -race ./...
go test -run=^$ -bench=. -benchmem ./...
```

Coverage spans parameter validation, preset round-trips and schema rules, rotor stability, the numeric contract between SIMD backends (including a fuzz target and cross-architecture golden vectors), allocation behaviour on the realtime path, bar and synth integration, CLI behaviour, optimizer infrastructure, checkpoint and resume flows, and synthetic and legacy reference fitting.

Reference audio lives in [testdata/reference](testdata/reference).

## License

MIT — see [LICENSE](LICENSE). Third-party notices for the vendored `wasm_exec.js` are in [web/THIRD_PARTY.md](web/THIRD_PARTY.md).
