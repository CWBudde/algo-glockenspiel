#!/bin/bash
#
# Builds everything the browser needs into web/dist: the React bundle and the
# WebAssembly modules.
#
# The two halves are produced by two toolchains and land in the same directory.
# Vite is configured with `emptyOutDir: false` precisely so that it does not
# delete the module beside it, and this script runs the bundle first and the
# modules second so that neither erases the other's output whichever one changed.
#
# Usage:
#   scripts/build-web.sh                      build both halves
#   scripts/build-web.sh --refresh-wasm-exec  pass the flag through to
#                                             scripts/build-wasm.sh
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT"

if ! command -v npm >/dev/null 2>&1; then
	echo "Error: npm was not found." >&2
	echo "       The browser front end is a Vite + React app; building it needs Node." >&2
	echo "       Install Node 22 or newer, or build only the modules with:" >&2
	echo "" >&2
	echo "         just build-wasm" >&2
	exit 1
fi

echo "Installing web dependencies..."
# `npm ci` rather than `npm install`: it installs exactly what
# web/package-lock.json pins and fails instead of quietly rewriting the lock
# file, which is what makes a local build and a CI build the same build.
npm --prefix web ci

echo "Building the web app..."
npm --prefix web run build

"$ROOT/scripts/build-wasm.sh" "$@"

# wasm_exec.js is the one file the page loads that Vite must not touch: it is
# vendored from the Go toolchain and shares an ABI with the module, so it is
# copied verbatim rather than bundled and content-hashed. index.html references
# it as a classic script, which is why `vite build` prints a note about not
# bundling it.
#
# The copy comes after build-wasm.sh, not before, because --refresh-wasm-exec is
# handled in there: it rewrites web/wasm_exec.js from the toolchain in use. A
# copy taken first would put the pre-upgrade shim in web/dist next to a module
# built by the new toolchain -- an ABI mismatch the build would report as
# success.
echo "Copying wasm_exec.js..."
mkdir -p web/dist
cp web/wasm_exec.js web/dist/wasm_exec.js
