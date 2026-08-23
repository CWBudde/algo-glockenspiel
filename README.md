# Glockenspiel

A browser-playable glockenspiel and the Go toolchain behind it.

The instrument is not a physical model. Its core is a runtime-configurable oscillator bank (`internal/oscbank`): `N` independent decaying quadrature oscillators, each carrying up to `M` integer-multiple harmonic partials, in an AoSoA `float32` layout with packed SIMD kernels for AVX2, SSE2 and NEON. Both counts are ordinary runtime configuration, not compile-time constants. `M` is a ceiling rather than a quota: every mode chooses its own partial count, and a mode with no harmonic list is a single fundamental. There is no `sin()` in the inner loop.

**Play it: <https://cwbudde.github.io/algo-glockenspiel/>**

The same engine also ships as a CLI for rendering notes to WAV and for fitting preset parameters against a reference recording.

## The Web App

The browser build compiles the Go engine to WebAssembly and drives it from a small JavaScript mixer. It gives you a piano-aligned instrument view spanning MIDI 36 to 96, playable by pointer or by computer keyboard, with a sound picker, a volume dial, a velocity dial and a reverb dial. The sounds are the presets in `assets/presets`, embedded in the module: adding a file there adds an option -- see [that directory's notes](assets/presets/README.md) for what a preset has to satisfy.

Run it locally:

```bash
just build-web
go run ./cmd/glockenspiel serve
```

Then open `http://localhost:8080`. Any static file server over `web/dist` works
too -- `npx serve web/dist` -- it just has no fit API for the Optimize tab.

`scripts/build-web.sh` builds both halves into `web/dist`: the React front end through Vite, then `scripts/build-wasm.sh`, which compiles `cmd/glockenspiel-wasm` to `web/dist/glockenspiel.wasm` and writes `web/dist/manifest.json`, whose content hash the page appends to the fetch URL so a rebuilt module is never served from cache. `web/wasm_exec.js` is tracked in git and copied into the bundle verbatim rather than bundled, because it shares an ABI with the module; the build only warns when it no longer matches the toolchain, and refreshes it on `./scripts/build-wasm.sh --refresh-wasm-exec`. If `wasm-opt` (binaryen) is on `PATH` the module is optimized as well, otherwise that step is skipped with a note. The GitHub Pages deployment runs the same script from `.github/workflows/deploy-pages.yml` and publishes `web/dist`.

The web build uses the standard Go toolchain; [docs/tinygo-evaluation.md](docs/tinygo-evaluation.md) records why, and what would change that.

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

Flags: `--reference`, `--preset`, `--bounds`, `--output`, `--note`, `--velocity`, `--sample-rate`, `--optimizer` (`simple` or `mayfly`), `--metric` (`rms`, `log` or `spectral`), `--max-iter`, `--time-budget` (a Go duration such as `30s` or `10m`; a bare number is read as seconds), `--align`, `--normalize-gain`, `--report-every`, `--checkpoint-interval`, `--work-dir`, `--resume`, `--mayfly-variant` (one of seven dialects, or `auto`), `--mayfly-pop`, `--mayfly-seed`, `--mayfly-preset`, `--mayfly-tuning`, `--mayfly-epochs`, `--mayfly-restarts`, `--mayfly-stagnation`, `--mayfly-target-cost`, `--mayfly-nc`, `--mayfly-nc-ratio`, `--mayfly-selection`. See [docs/mayfly-tuning.md](docs/mayfly-tuning.md) for the tuning document.

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
├── web/                    # The web app: HTML, CSS, JS mixer, wasm_exec.js
├── model/                  # Public bar model: parameters, Chebyshev shaper, Bar
├── internal/oscbank        # The oscillator bank and its SIMD kernels
├── internal/cpufeat        # Runtime CPU feature detection
├── internal/synth          # Note rendering and the realtime voice engine
├── internal/preset         # Preset JSON schema v1/v2, load, save, validate
├── internal/optimizer      # Objectives, optimizers, checkpoints
├── internal/cli            # Cobra commands
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

`just check-tidy` runs `go mod tidy -diff`, and CI runs it too, alongside a WASM build and unit tests on amd64 at `GOAMD64=v1` and `v3` and on arm64.

The VST3 plugin is no longer built here. It lives in [algo-glockenspiel-vst3](https://github.com/CWBudde/algo-glockenspiel-vst3) and consumes this module's `model` package as an ordinary dependency.

## Architecture

The signal chain for one note:

1. an excitation impulse scaled by velocity
2. lowpass pre-emphasis
3. an optional Chebyshev waveshaper, on the excitation or on the output
4. the oscillator bank: every mode expands into one rotor per harmonic partial
5. dry/wet mix, and hard clipping on the realtime path

The optimizer layer is kept separate from the synthesis engine so new metrics and search strategies can be added without touching the core.

`docs/` goes deeper:

- [docs/oscillator-bank.md](docs/oscillator-bank.md) — the recursion, the AoSoA layout, the three packed kernels, the numeric contract they are held to, the realtime render path, and measured performance.
- [docs/optimizer.md](docs/optimizer.md) — parameter encoding, objective evaluation, reading references, and checkpoint contracts.
- [docs/user-guide.md](docs/user-guide.md) — the full CLI walkthrough, including `--bounds` and how to choose an optimizer and a metric.
- [docs/web-app.md](docs/web-app.md) — the front end's architecture, the WASM bridge, the two-step build, and the Optimize loop.
- [docs/serve.md](docs/serve.md) — the `serve` command and the fit API it exposes.
- [docs/audio-transport.md](docs/audio-transport.md) — why synthesis runs in a Web Worker behind an `AudioWorkletNode`, and the three alternatives that were rejected.
- [docs/public-api.md](docs/public-api.md) — what the public `model/` package promises to the plugin repository, and the rules that keep it usable from outside this module.
- [docs/tinygo-evaluation.md](docs/tinygo-evaluation.md) — why the WASM build uses the standard toolchain.

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
