# Repository Guidelines

## Project Structure & Module Organization

This repository is a Go project for a glockenspiel synthesizer built on a runtime-configurable oscillator bank. It is not a physical model. There are two entry points: `cmd/glockenspiel` (the CLI) and `cmd/glockenspiel-wasm` (the WebAssembly build behind the web app). The VST3 plugin is a separate module, [algo-glockenspiel-vst3](https://github.com/CWBudde/algo-glockenspiel-vst3), and consumes `model` as a dependency.

One package is public, because a separate module has to import it:

- `model`: `Bar`, the parameter types and the Chebyshev shaper. It moved out of `internal/model` in Phase 6.1 — Go enforces `internal/` against the module path, so the plugin could not have imported it there.

The rest is under `internal/`:

- `internal/oscbank`: the synthesis core. A bank of `N` decaying quadrature oscillators with `M` harmonic partials each, in an AoSoA `float32` layout, with packed AVX2, SSE2 and NEON kernels alongside the portable one, plus the denormal-flushing scope the render path opens.
- `internal/cpufeat`: runtime CPU feature detection, published through an `atomic.Pointer` and overridable in tests so a backend can be exercised on a host that would not otherwise dispatch to it.
- `internal/synth`: note rendering and the realtime voice engine.
- `internal/preset`: preset JSON schema v1/v2, load, save, validation.
- `internal/cli`: Cobra commands.
- `internal/optimizer`: objectives, optimizer backends, checkpoints.

The web front end lives in `web/`. Static assets live in `assets/presets` and are embedded into the binary. Regression fixtures and sample inputs live in `testdata/`. Helper scripts are in `scripts/`. Design notes are in `docs/`, and [PLAN.md](PLAN.md) tracks phase state.

Assembly under `internal/oscbank` and `model` is Go Plan 9 syntax and is held to a written numeric contract; read [docs/oscillator-bank.md](docs/oscillator-bank.md) before touching a `.s` file or the portable kernel it is measured against.

## Build, Test, and Development Commands

- `just build`: build `bin/glockenspiel`
- `just install`: install the CLI with `go install`
- `just test`: run the main Go test suite with a local cache
- `just test-race`: run tests with the race detector
- `just bench`: run benchmarks
- `just bench-arm64`: run the `oscbank` benchmarks on a remote native arm64 host through
  `scripts/bench-remote.sh`; set `GLOCKENSPIEL_ARM64_HOST=user@host` first
- `just lint`: run `golangci-lint`
- `just fmt`: format the repo through `treefmt`
- `just ci`: run formatting checks, tests, lint, and module tidiness
- `just build-web`: build the WebAssembly demo through `scripts/build-wasm.sh`

Direct equivalents are available, for example `go test ./...` and `go build ./cmd/glockenspiel`.

`just ci` ends in `check-tidy`, which runs `go mod tidy -diff`: it reports what tidying would change and exits non-zero without rewriting the tree. CI runs the same command in `test-can-build`.

## Coding Style & Naming Conventions

Use standard Go formatting and keep files `gofmt`-clean. Prefer tabs as Go tooling emits them. Package names should stay short and lowercase. Exported identifiers use `CamelCase`; unexported helpers use `camelCase`. Test files should follow `*_test.go`. Keep new code in the existing package boundaries rather than creating broad utility packages.

Run `just fmt` and `just lint` before opening a PR.

## Testing Guidelines

Tests use Go’s built-in `testing` package. Place unit tests next to the code they cover, for example `model/params_test.go`. Name tests `TestXxx` and benchmarks `BenchmarkXxx`. Add table-driven tests where inputs vary. For synthesis or preset changes, include coverage for both happy-path behavior and validation failures. A new SIMD backend has one more obligation: register it in `availableBackends()` in `internal/oscbank/contract_test.go`, which is what the differential, golden-vector and fuzz harnesses iterate.

## Commit & Pull Request Guidelines

The visible history is sparse, so use simple imperative commit subjects like `add fit checkpoint tests` or `refine synth auto-stop`. Keep subjects concise and focused on one change.

Pull requests should include:

- a short description of what changed and why
- linked issue or task reference when applicable
- test evidence (`just test`, `just lint`, relevant benchmarks)
- sample output details for CLI or audio-affecting changes

## Configuration Tips

Do not commit generated WAVs or temporary outputs outside approved fixture paths. Keep large test artifacts under `testdata/` only when they are required for repeatable regression coverage.
