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
# The fit drifts base_frequency, which is harmless and worth normalising back to 440
# by hand: it never reaches the audio, only the optimizer's frequency encoding, where
# it is the anchor mode frequencies are expressed against. That is pinned by
# TestBaseFrequencyDoesNotReachTheAudio in internal/synth/transposition_test.go.

# Re-fit the shipped default preset against its reference recording
refit-default *ARGS:
    go run ./cmd/glockenspiel fit \
        --reference testdata/reference/legacy_synth_a4.wav \
        --output out/refit/default.json \
        --optimizer mayfly --mayfly-pop 30 --mayfly-seed 1 \
        --max-iter 100000 --time-budget 8m \
        --sample-rate 44100 --note 69 --velocity 100 \
        --work-dir out/refit {{ ARGS }}

# Run the web app's checks: typecheck, lint, unit tests
test-web:
    npm --prefix web run typecheck
    npm --prefix web run lint
    npm --prefix web run test

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

# Run all checks (formatting, linting, tests, tidiness)
ci: check-formatted test lint check-tidy

# Clean build artifacts
clean:
    rm -rf bin/ coverage.out coverage.html .tmp/

fix:
    just lint-fix
    just fmt
