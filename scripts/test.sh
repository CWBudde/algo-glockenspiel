#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

export GOCACHE="${GOCACHE:-$repo_root/.tmp/go-cache}"
mkdir -p "$GOCACHE"

go test . ./cmd/glockenspiel ./internal/...
