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

# The refit recipes name the arm Phase 8.6's engine-shape campaign promoted:
# Mayfly in one warm round from the reference's own partials plus fifteen cold
# restarts. It beat separable CMA-ES by 0.040 of score over twelve paired blocks
# on the C5 recording (p = 0.002 after Holm) and its spread across seeds is a
# fraction of CMA-ES's on both references. docs/training.md holds the tables.
#
# Since it is also the CLI default now, --optimizer is left out on purpose: the
# recipe follows the default rather than pinning a backend of its own, so a
# later measured change to the default reaches these recipes too. The round
# schedule and population are written out anyway, because a recipe that ships a
# preset has to say what produced it.
#
# --time-budget 0 removes the clock, so --max-evals is what ends the run and a
# rerun at a fixed seed reproduces the fit bit for bit at one worker width. That
# is the whole point: the shipped recorded-bar.json had no recorded command at
# all. --max-iter sizes the sixteen rounds against the budget, at the measured
# 43.05 evaluations an iteration costs, with a tenth of headroom so the
# evaluation cap is what stops the run rather than the iteration cap.
#
# 120,000 evaluations is five times the campaign's budget, spent on more cold
# restarts: the campaign's best came from a cold round in eleven blocks of
# twelve, so more budget is worth more restarts.
#
# One run is not the best a seed sweep reaches -- on the C5 recording the spread
# across seeds is wider than the gap between engines. Pass --seed N and keep the
# lowest score if a preset is going to be shipped.

# Re-fit the shipped default preset against its reference recording
refit-default *ARGS: build
    ./bin/glockenspiel fit \
        --reference testdata/reference/legacy_synth_a4.wav \
        --output out/refit/default.json \
        --mayfly-pop 10 --mayfly-epochs 1 --mayfly-restarts 15 \
        --time-budget 0 --max-evals 120000 --max-iter 3067 \
        --polish cmaes --seed 1 \
        --sample-rate 44100 --note 69 --velocity 100 \
        --work-dir out/refit {{ ARGS }}

# The recorded-bar refit runs at note 72, the recording's own pitch, so nothing
# needs a hand retune afterwards. The shipped recorded-bar.json was fitted at
# note 69 and then multiplied by 1.667 by hand, which is the step Phase 8.6
# exists to stop repeating.

# Re-fit the recorded-bar preset against the C5 room recording
refit-recorded *ARGS: build
    ./bin/glockenspiel fit \
        --reference testdata/reference/glockenspiel_c5.wav \
        --output out/refit/recorded-bar.json \
        --mayfly-pop 10 --mayfly-epochs 1 --mayfly-restarts 15 \
        --time-budget 0 --max-evals 120000 --max-iter 3067 \
        --polish cmaes --seed 1 \
        --sample-rate 44100 --note 72 --velocity 100 \
        --work-dir out/refit-recorded {{ ARGS }}

# The pre-8.4 Mayfly recipe: one round of a thirty-strong swarm on a wall-clock
# budget. It is kept as the shape, not as a reproduction -- the figures recorded
# under it do not come back, because Mayfly moved to v0.7.1 and Phase 8.6
# changed how a round's random streams are derived. Use refit-default for
# anything that will be shipped.

# Re-fit the default preset with the pre-8.4 single-round Mayfly shape
refit-default-mayfly *ARGS:
    go run ./cmd/glockenspiel fit \
        --reference testdata/reference/legacy_synth_a4.wav \
        --output out/refit/default-mayfly.json \
        --optimizer mayfly --mayfly-pop 30 --mayfly-restarts 0 --seed 1 \
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

# A campaign is identified by the binary that planned it: the manifest records the
# file's SHA-256 and `run` refuses a different one. So the recipes below drive one
# built file rather than `go run`, which would rebuild between plan and run and
# strand the campaign on a binary that no longer exists. They resolve their paths
# against the repository root, so run them from there.

# Build the campaign harness into out/campaign/bin (see docs/campaign.md)
campaign-build:
    go build -trimpath -o out/campaign/bin/glockenspiel-campaign ./cmd/glockenspiel-campaign

# Plan a campaign into out/campaign/DESIGN (refuses to overwrite an existing one)
campaign-plan DESIGN *ARGS: campaign-build
    ./out/campaign/bin/glockenspiel-campaign plan {{ DESIGN }} --dir out/campaign/{{ DESIGN }} {{ ARGS }}

# Run a planned campaign; Ctrl-C stops after the job in flight and a rerun resumes
campaign-run DESIGN *ARGS:
    ./out/campaign/bin/glockenspiel-campaign run --dir out/campaign/{{ DESIGN }} {{ ARGS }}

# Report a running campaign's progress; safe to call while `campaign-run` is going
campaign-status DESIGN:
    ./out/campaign/bin/glockenspiel-campaign status --dir out/campaign/{{ DESIGN }}

# Write out/campaign/DESIGN/results.csv from the run directories
campaign-collect DESIGN *ARGS:
    ./out/campaign/bin/glockenspiel-campaign collect --dir out/campaign/{{ DESIGN }} {{ ARGS }}

# Rebuild the report from results.csv and write it beside it
campaign-analyze DESIGN *ARGS:
    ./out/campaign/bin/glockenspiel-campaign analyze --dir out/campaign/{{ DESIGN }} \
        --out out/campaign/{{ DESIGN }}/report.md {{ ARGS }}

# The smoke campaign is a wiring check and not a measurement: four jobs of 1,200
# evaluations on the short synthetic reference say that plan, run, collect and
# analyze agree about the files between them, and say nothing about which engine is
# better. The directory is removed first because `plan` refuses to overwrite a
# manifest.

# Run the whole harness end to end on the smoke design, which takes seconds
campaign-smoke:
    rm -rf out/campaign/smoke
    just campaign-build
    just campaign-plan smoke
    just campaign-run smoke
    just campaign-collect smoke
    just campaign-analyze smoke

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

# Regenerate the TypeScript mirror of the fit request schema (limits, defaults, name lists)
gen-fit-schema:
    go run ./cmd/gen-fit-schema

# Verify the generated fit schema matches internal/fitschema
check-fit-schema:
    go run ./cmd/gen-fit-schema --check

# Run all checks (formatting, linting, tests, tidiness, generated files)
ci: check-formatted test lint check-tidy check-mayfly-tuning check-presets check-fit-schema

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
