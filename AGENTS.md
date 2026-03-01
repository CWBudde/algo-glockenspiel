# Repository Guidelines

## Project Structure & Module Organization

This repository is a Go project for a physical-model glockenspiel synthesizer. The CLI entry point lives in `cmd/glockenspiel`. Core implementation is under `internal/`:

- `internal/model`: oscillator, bar model, and parameter types
- `internal/synth`: note rendering pipeline
- `internal/preset`: preset JSON load/save/validation
- `internal/cli`: Cobra commands
- `internal/optimizer`: optimization interfaces and related code

Static assets live in `assets/presets`. Regression fixtures and sample inputs live in `testdata/`. Helper scripts are in `scripts/`.

## Build, Test, and Development Commands

- `just build`: build `bin/glockenspiel`
- `just install`: install the CLI with `go install`
- `just test`: run the main Go test suite with a local cache
- `just test-race`: run tests with the race detector
- `just bench`: run benchmarks
- `just lint`: run `golangci-lint`
- `just fmt`: format the repo through `treefmt`
- `just ci`: run formatting checks, tests, lint, and module tidiness

Direct equivalents are available, for example `go test ./...` and `go build ./cmd/glockenspiel`.

## Coding Style & Naming Conventions

Use standard Go formatting and keep files `gofmt`-clean. Prefer tabs as Go tooling emits them. Package names should stay short and lowercase. Exported identifiers use `CamelCase`; unexported helpers use `camelCase`. Test files should follow `*_test.go`. Keep new code in the existing package boundaries rather than creating broad utility packages.

Run `just fmt` and `just lint` before opening a PR.

## Testing Guidelines

Tests use Go’s built-in `testing` package. Place unit tests next to the code they cover, for example `internal/model/params_test.go`. Name tests `TestXxx` and benchmarks `BenchmarkXxx`. Add table-driven tests where inputs vary. For synthesis or preset changes, include coverage for both happy-path behavior and validation failures.

## Commit & Pull Request Guidelines

The visible history is sparse, so use simple imperative commit subjects like `add fit checkpoint tests` or `refine synth auto-stop`. Keep subjects concise and focused on one change.

Pull requests should include:

- a short description of what changed and why
- linked issue or task reference when applicable
- test evidence (`just test`, `just lint`, relevant benchmarks)
- sample output details for CLI or audio-affecting changes

## Configuration Tips

Do not commit generated WAVs or temporary outputs outside approved fixture paths. Keep large test artifacts under `testdata/` only when they are required for repeatable regression coverage.
