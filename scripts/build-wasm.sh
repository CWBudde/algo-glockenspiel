#!/bin/bash
#
# Builds the browser demo: compiles cmd/glockenspiel-wasm to web/dist and writes
# a manifest naming the content hash of the module, which web/main.js appends to
# the fetch URL for cache busting.
#
# Usage:
#   scripts/build-wasm.sh                      build the module
#   scripts/build-wasm.sh --refresh-wasm-exec  also update the tracked
#                                              web/wasm_exec.js from the current
#                                              Go toolchain
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

echo "Building WASM glockenspiel demo..."

mkdir -p web/dist
mkdir -p "${GOCACHE:-/tmp/gocache}" "${GOMODCACHE:-/tmp/gomodcache}"

WASM_OUT=web/dist/glockenspiel.wasm

echo "Compiling Go to WASM..."
# -trimpath keeps absolute build paths -- $HOME, CI checkout directories -- out
# of the module, and makes the output reproducible for the content hash below,
# which would otherwise differ per machine for identical source. -s -w drops the
# symbol table and DWARF debug info; they are only reachable from a wasm
# debugger nobody here uses, and they are a large share of the payload.
GOCACHE="${GOCACHE:-/tmp/gocache}" \
	GOMODCACHE="${GOMODCACHE:-/tmp/gomodcache}" \
	GOOS=js GOARCH=wasm \
	go build -trimpath -ldflags="-s -w" -o "$WASM_OUT" ./cmd/glockenspiel-wasm

size_of() {
	wc -c <"$1" | tr -d ' '
}

SIZE_RAW=$(size_of "$WASM_OUT")

# wasm-opt (binaryen) shrinks the module further and is worth having, but it is
# a separate toolchain that a Go checkout does not bring with it. Making it
# mandatory would mean a fresh clone cannot build the demo, so a missing
# wasm-opt is a notice rather than a failure -- the module is already valid
# without it. The pass writes to a temporary file and only replaces the module
# on success, so a wasm-opt that is too old for the features Go emits leaves the
# working build in place instead of a truncated one.
#
# -O3 rather than -Oz: measured on this module (binaryen 132), -O3 gives
# 3,212,389 bytes and -Oz 3,171,836, so -Oz buys 1.3% at the cost of optimising
# for size in code that runs in the audio callback. The feature flags name what
# Go's wasm backend emits; without them an older wasm-opt rejects the module,
# which is exactly the failure the fallback above keeps out of the build.
if command -v wasm-opt >/dev/null 2>&1; then
	echo "Optimizing with wasm-opt..."
	WASM_OPT_LOG=$(mktemp)
	if wasm-opt -O3 --enable-bulk-memory --enable-nontrapping-float-to-int \
		--enable-sign-ext --enable-mutable-globals \
		"$WASM_OUT" -o "$WASM_OUT.opt" 2>"$WASM_OPT_LOG"; then
		mv "$WASM_OUT.opt" "$WASM_OUT"
	else
		rm -f "$WASM_OUT.opt"
		echo "Note: wasm-opt failed, keeping the unoptimized module. Log:" >&2
		sed 's/^/  /' "$WASM_OPT_LOG" >&2 || true
	fi
	rm -f "$WASM_OPT_LOG"
else
	echo "Note: wasm-opt not found, skipping the size pass."
	echo "      Install binaryen (e.g. 'apt install binaryen') for a smaller module."
fi

SIZE_FINAL=$(size_of "$WASM_OUT")

# The hash is what web/main.js puts in the fetch URL, so the browser asks for a
# different resource whenever the bytes differ. It is deliberately not part of
# the file name: internal/server hard-codes glockenspiel.wasm so it can tell a
# missing build from a missing file and print the command that fixes it, and
# web/embed.go lists the tracked files one by one. A query parameter busts the
# cache without touching either.
if command -v sha256sum >/dev/null 2>&1; then
	HASH=$(sha256sum "$WASM_OUT" | cut -c1-16)
else
	HASH=$(shasum -a 256 "$WASM_OUT" | cut -c1-16)
fi

cat >web/dist/manifest.json <<EOF
{
  "wasm": "glockenspiel.wasm",
  "hash": "$HASH",
  "bytes": $SIZE_FINAL
}
EOF

# wasm_exec.js is tracked in git and embedded into the serve binary, so copying
# it over on every build silently dirtied the working tree and could commit a
# toolchain change nobody chose. It is refreshed on request instead; the check
# below still says when the tracked copy no longer matches the toolchain in use,
# because a mismatch there breaks the module in ways that look like a bug in
# this repository.
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
	echo "Note: web/wasm_exec.js differs from $WASM_EXEC_SRC" >&2
	echo "      Run '$0 --refresh-wasm-exec' and commit the result if the" >&2
	echo "      Go toolchain changed." >&2
fi

echo "Build complete. Files in web/dist/"
printf 'Module: %s bytes (%s before wasm-opt), hash %s\n' "$SIZE_FINAL" "$SIZE_RAW" "$HASH"
echo "Run: go run ./cmd/glockenspiel serve   (or: python3 -m http.server -d web 8080)"
