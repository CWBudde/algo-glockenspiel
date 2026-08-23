#!/bin/bash
#
# Builds the browser demo's audio and optimizer modules into web/dist and writes
# a manifest naming both content hashes, which the workers append to their fetch
# URLs for cache busting.
#
# It builds only the modules. `just build-web` runs scripts/build-web.sh, which
# builds the React bundle beside it; use that unless you deliberately want the
# module on its own.
#
# It refuses to build when the tracked web/wasm_exec.js does not match the Go
# toolchain in use, because the two share an ABI.
#
# Usage:
#   scripts/build-wasm.sh                      build both modules
#   scripts/build-wasm.sh --refresh-wasm-exec  update the tracked
#                                              web/wasm_exec.js from the current
#                                              Go toolchain, then build
set -euo pipefail

REFRESH_WASM_EXEC=0
for arg in "$@"; do
	case "$arg" in
	--refresh-wasm-exec) REFRESH_WASM_EXEC=1 ;;
	*)
		echo "Unknown argument: $arg" >&2
		echo "Usage: $0 [--refresh-wasm-exec]" >&2
		exit 2
		;;
	esac
done

# wasm_exec.js is tracked in git and embedded into the serve binary, so copying
# it over on every build silently dirtied the working tree and could commit a
# toolchain change nobody chose. It is refreshed on request instead, and a
# tracked copy that no longer matches the toolchain in use is a hard error: the
# shim is the ABI between the module and the browser, so a mismatched pair is a
# demo that fails at load or, worse, inside the audio callback, in ways that
# look like a bug in this repository. Reporting success and shipping that pair
# is the one outcome worth ruling out, so the check runs before the compiler
# does -- a failed build then leaves no freshly dated module next to a shim it
# does not match.
#
# The comparison is the whole file rather than a version probe, and that is not
# as trigger-happy as it sounds: lib/wasm/wasm_exec.js is byte-identical across
# Go 1.22.10, 1.24.2, 1.25.0 and 1.26.5, so patch and minor bumps do not move
# it. When it does move, it moves because the ABI moved.
GOROOT=$(go env GOROOT)
WASM_EXEC_SRC=""
for candidate in "$GOROOT/lib/wasm/wasm_exec.js" "$GOROOT/misc/wasm/wasm_exec.js"; do
	if [ -f "$candidate" ]; then
		WASM_EXEC_SRC="$candidate"
		break
	fi
done

if [ -z "$WASM_EXEC_SRC" ]; then
	echo "Error: wasm_exec.js not found in GOROOT" >&2
	exit 1
fi

if [ "$REFRESH_WASM_EXEC" -eq 1 ]; then
	cp "$WASM_EXEC_SRC" web/wasm_exec.js
	echo "Refreshed web/wasm_exec.js from $WASM_EXEC_SRC"
elif ! cmp -s "$WASM_EXEC_SRC" web/wasm_exec.js; then
	echo "Error: web/wasm_exec.js does not match the Go toolchain in use." >&2
	echo "       tracked:   web/wasm_exec.js" >&2
	echo "       toolchain: $WASM_EXEC_SRC" >&2
	echo "" >&2
	echo "       The shim and the module share an ABI, so building against a" >&2
	echo "       mismatched pair produces a demo that does not run. Refresh it" >&2
	echo "       and commit the result if the Go toolchain changed:" >&2
	echo "" >&2
	echo "         $0 --refresh-wasm-exec" >&2
	exit 1
fi

echo "Building WASM glockenspiel demo..."

mkdir -p web/dist
mkdir -p "${GOCACHE:-/tmp/gocache}" "${GOMODCACHE:-/tmp/gomodcache}"

AUDIO_WASM_OUT=web/dist/glockenspiel.wasm
FIT_WASM_OUT=web/dist/glockenspiel-fit.wasm

echo "Compiling audio Go to WASM..."
# -trimpath keeps absolute build paths -- $HOME, CI checkout directories -- out
# of the module, and makes the output reproducible for the content hash below,
# which would otherwise differ per machine for identical source. -s -w drops the
# symbol table and DWARF debug info; they are only reachable from a wasm
# debugger nobody here uses, and they are a large share of the payload.
GOCACHE="${GOCACHE:-/tmp/gocache}" \
	GOMODCACHE="${GOMODCACHE:-/tmp/gomodcache}" \
	GOOS=js GOARCH=wasm \
	go build -trimpath -ldflags="-s -w" -o "$AUDIO_WASM_OUT" ./cmd/glockenspiel-wasm

echo "Compiling optimizer Go to WASM..."
GOCACHE="${GOCACHE:-/tmp/gocache}" \
	GOMODCACHE="${GOMODCACHE:-/tmp/gomodcache}" \
	GOOS=js GOARCH=wasm \
	go build -trimpath -ldflags="-s -w" -o "$FIT_WASM_OUT" ./cmd/glockenspiel-fit-wasm

size_of() {
	wc -c <"$1" | tr -d ' '
}

AUDIO_SIZE_RAW=$(size_of "$AUDIO_WASM_OUT")
FIT_SIZE_RAW=$(size_of "$FIT_WASM_OUT")

# wasm-opt (binaryen) shrinks the module further and is worth having, but it is
# a separate toolchain that a Go checkout does not bring with it. Making it
# mandatory would mean a fresh clone cannot build the demo, so a missing
# wasm-opt is a notice rather than a failure -- the module is already valid
# without it. The pass writes to a temporary file and only replaces the module
# on success, so a wasm-opt that is too old for the features Go emits leaves the
# working build in place instead of a truncated one.
#
# -O3 rather than -Oz: on the synthesis-only module that preceded the browser
# optimizer, -O3 gave 3,212,389 bytes and -Oz 3,171,836, so -Oz bought 1.3% at
# the cost of optimising for size in code that runs in the audio callback. The
# browser-fit build is larger because it carries Gonum, Mayfly and the spectral
# objective, but the audio hot path makes the same tradeoff. The feature flags
# name what Go's wasm backend emits; without them an older wasm-opt rejects the
# module, which is exactly the failure the fallback above keeps out of the build.
if command -v wasm-opt >/dev/null 2>&1; then
	for wasm_output in "$AUDIO_WASM_OUT" "$FIT_WASM_OUT"; do
		echo "Optimizing $wasm_output with wasm-opt..."
		WASM_OPT_LOG=$(mktemp)
		if wasm-opt -O3 --enable-bulk-memory --enable-nontrapping-float-to-int \
			--enable-sign-ext --enable-mutable-globals \
			"$wasm_output" -o "$wasm_output.opt" 2>"$WASM_OPT_LOG"; then
			mv "$wasm_output.opt" "$wasm_output"
		else
			rm -f "$wasm_output.opt"
			echo "Note: wasm-opt failed for $wasm_output, keeping the unoptimized module. Log:" >&2
			sed 's/^/  /' "$WASM_OPT_LOG" >&2 || true
		fi
		rm -f "$WASM_OPT_LOG"
	done
else
	echo "Note: wasm-opt not found, skipping the size pass."
	echo "      Install binaryen (e.g. 'apt install binaryen') for a smaller module."
fi

AUDIO_SIZE_FINAL=$(size_of "$AUDIO_WASM_OUT")
FIT_SIZE_FINAL=$(size_of "$FIT_WASM_OUT")

# The hash is what the front end puts in the fetch URL, so the browser asks for a
# different resource whenever the bytes differ. It is deliberately not part of
# the file name: internal/server hard-codes glockenspiel.wasm so it can tell a
# missing build from a missing file and print the command that fixes it, and
# web/embed.go embeds only a placeholder page. A query parameter busts the
# cache without touching either.
hash_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -c1-16
	else
		shasum -a 256 "$1" | cut -c1-16
	fi
}

AUDIO_HASH=$(hash_of "$AUDIO_WASM_OUT")
FIT_HASH=$(hash_of "$FIT_WASM_OUT")

cat >web/dist/manifest.json <<EOF
{
  "wasm": "glockenspiel.wasm",
  "hash": "$AUDIO_HASH",
  "bytes": $AUDIO_SIZE_FINAL,
  "fitWasm": "glockenspiel-fit.wasm",
  "fitHash": "$FIT_HASH",
  "fitBytes": $FIT_SIZE_FINAL
}
EOF

echo "Build complete. Files in web/dist/"
printf 'Audio module: %s bytes (%s before wasm-opt), hash %s\n' \
	"$AUDIO_SIZE_FINAL" "$AUDIO_SIZE_RAW" "$AUDIO_HASH"
printf 'Fit module:   %s bytes (%s before wasm-opt), hash %s\n' \
	"$FIT_SIZE_FINAL" "$FIT_SIZE_RAW" "$FIT_HASH"
echo "Run: go run ./cmd/glockenspiel serve   (or: npx serve web/dist)"
