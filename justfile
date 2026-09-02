set shell := ["bash", "-uc"]

export GOPRIVATE := "github.com/cwbudde"

# Default recipe - show available commands
default:
    @just --list

# Format all code using treefmt
fmt:
    treefmt --allow-missing-formatter

# Check if code is formatted correctly
check-formatted:
    treefmt --allow-missing-formatter --fail-on-change

# Run linters
lint:
    GOCACHE="${GOCACHE:-/tmp/gocache}" GOMODCACHE="${GOMODCACHE:-/tmp/gomodcache}" GOLANGCI_LINT_CACHE="${GOLANGCI_LINT_CACHE:-/tmp/golangci-lint-cache}" golangci-lint run --timeout=2m ./...

# Run linters with auto-fix
lint-fix:
    GOCACHE="${GOCACHE:-/tmp/gocache}" GOMODCACHE="${GOMODCACHE:-/tmp/gomodcache}" GOLANGCI_LINT_CACHE="${GOLANGCI_LINT_CACHE:-/tmp/golangci-lint-cache}" golangci-lint run --fix --timeout=2m ./...

# Ensure go.mod is tidy
check-tidy:
    go mod tidy -diff

# Run all tests
test:
    mkdir -p .tmp/go-cache
    GOCACHE="$(pwd)/.tmp/go-cache" go test -v ./...

# Run tests with race detector
test-race:
    go test -race ./...

# Run tests with coverage
test-coverage:
    go test -v -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html

# Run benchmarks
bench:
    go test -run=^$ -bench=. -benchmem ./...

# Run the oscbank benchmarks on a remote native arm64 host (see scripts/bench-remote.sh)
bench-arm64 *ARGS:
    ./scripts/bench-remote.sh {{ ARGS }}

# This is how the shipped preset was produced, not a way to reproduce its bytes: the
# run stops on its time budget, so a rerun sees a different number of evaluations.
#
# It writes to out/refit/ rather than over assets/presets/default.json because the
# result has to be measured before it replaces the preset the whole suite is
# calibrated against -- peak level, keyboard slope, and correlation against the
# reference, which the objective does not measure because it time-aligns candidates
# before scoring. Two runs an evaluation apart in objective value can differ entirely
# on the last two.
#
# base_frequency is not searched since Phase 8.3: the fit writes the starting
# preset's 440 through, because the value never reaches the audio -- pinned by
# TestBaseFrequencyDoesNotReachTheAudio in internal/synth/transposition_test.go.
# The modes come from the reference's partials rather than the starting preset,
# so the result is a v2 preset with as many modes as the analysis lists.

# Re-fit the shipped default preset against its reference recording
#
# This is the default pipeline as of Phase 8.4: CMA-ES restarting until the time
# budget is spent, followed by the local polish stage. --optimizer is left out
# on purpose, so the recipe follows the CLI default rather than pinning a
# backend of its own.
refit-default *ARGS:
    go run ./cmd/glockenspiel fit \
        --reference testdata/reference/legacy_synth_a4.wav \
        --output out/refit/default.json \
        --max-iter 100000 --time-budget 8m \
        --polish cmaes \
        --sample-rate 44100 --note 69 --velocity 100 \
        --work-dir out/refit {{ ARGS }}

# Re-fit the shipped default preset with Mayfly, the pre-8.4 recipe
#
# Kept as a Mayfly recipe so the figures recorded under it stay reproducible.
refit-default-mayfly *ARGS:
    go run ./cmd/glockenspiel fit \
        --reference testdata/reference/legacy_synth_a4.wav \
        --output out/refit/default-mayfly.json \
        --optimizer mayfly --mayfly-pop 30 --seed 1 \
        --max-iter 100000 --time-budget 8m \
        --sample-rate 44100 --note 69 --velocity 100 \
        --work-dir out/refit-mayfly {{ ARGS }}

# Run the web app's checks: typecheck, lint, unit tests
test-web:
    npm --prefix web run typecheck
    npm --prefix web run lint
    npm --prefix web run test

# Run the Chromium visual regression suite (install with: npx playwright install chromium)
test-web-visual:
    npm --prefix web run test:visual

# Build the glockenspiel CLI binary
build:
    go build -o bin/glockenspiel ./cmd/glockenspiel

# Build the whole web app into web/dist: the React bundle and the WASM module
build-web *ARGS:
    ./scripts/build-web.sh {{ ARGS }}

# Build only the WASM module (pass --refresh-wasm-exec to update web/wasm_exec.js)
build-wasm *ARGS:
    ./scripts/build-wasm.sh {{ ARGS }}

# Install the glockenspiel CLI binary
install:
    go install ./cmd/glockenspiel

# Regenerate the TypeScript mirror of the Mayfly tuning knob table
gen-mayfly-tuning:
    go run ./cmd/gen-mayfly-tuning

# Verify the generated Mayfly tuning table matches internal/optimizer
check-mayfly-tuning:
    go run ./cmd/gen-mayfly-tuning --check

# Regenerate the TypeScript mirror of the built-in sound list
gen-presets:
    go run ./cmd/gen-presets

# Verify the generated sound list matches assets/presets
check-presets:
    go run ./cmd/gen-presets --check

# Run all checks (formatting, linting, tests, tidiness, generated files)
ci: check-formatted test lint check-tidy check-mayfly-tuning check-presets

# Clean build artifacts
clean:
    rm -rf bin/ coverage.out coverage.html .tmp/

fix:
    just lint-fix
    just fmt

# Measure both shipped references: cut, level, partials (see docs/training.md)
analyze:
    #!/usr/bin/env bash
    set -euo pipefail
    for reference in legacy_synth_a4 glockenspiel_c5; do
        go run ./cmd/glockenspiel analyze --reference testdata/reference/$reference.wav
        echo
    done

# Score both shipped presets against both references through the fit objective (see docs/training.md)
baseline:
    #!/usr/bin/env bash
    set -euo pipefail
    while read -r preset reference note; do
        echo "== assets/presets/$preset.json vs testdata/reference/$reference.wav at note $note"
        go run ./cmd/glockenspiel distance \
            --reference testdata/reference/$reference.wav \
            --preset assets/presets/$preset.json \
            --sample-rate 44100 --note $note --velocity 100
        echo
    done <<'ROWS'
    default legacy_synth_a4 69
    recorded-bar legacy_synth_a4 69
    default glockenspiel_c5 69
    recorded-bar glockenspiel_c5 69
    recorded-bar glockenspiel_c5 60
    ROWS
