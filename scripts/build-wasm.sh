#!/bin/bash
set -euo pipefail

echo "Building WASM glockenspiel demo..."

mkdir -p web/dist
mkdir -p "${GOCACHE:-/tmp/gocache}" "${GOMODCACHE:-/tmp/gomodcache}"

echo "Compiling Go to WASM..."
GOCACHE="${GOCACHE:-/tmp/gocache}" \
GOMODCACHE="${GOMODCACHE:-/tmp/gomodcache}" \
GOOS=js GOARCH=wasm \
go build -o web/dist/glockenspiel.wasm ./cmd/glockenspiel-wasm

echo "Copying wasm_exec.js..."
GOROOT=$(go env GOROOT)
if [ -f "$GOROOT/lib/wasm/wasm_exec.js" ]; then
	cp "$GOROOT/lib/wasm/wasm_exec.js" web/
elif [ -f "$GOROOT/misc/wasm/wasm_exec.js" ]; then
	cp "$GOROOT/misc/wasm/wasm_exec.js" web/
else
	echo "Error: wasm_exec.js not found in GOROOT"
	exit 1
fi

echo "Build complete. Files in web/dist/"
echo "Run: python3 -m http.server -d web 8080"
