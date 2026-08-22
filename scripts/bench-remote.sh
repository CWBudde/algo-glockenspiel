#!/usr/bin/env bash
#
# Runs `go test` on a remote host over ssh, against a copy of the working tree.
#
# It exists for the arm64 side of docs/oscillator-bank.md. On an amd64
# development machine the only way to execute the NEON kernel at all is
# qemu-user, which is a translation layer: trustworthy for correctness,
# worthless for timing. Any number in the benchmark table has to come off native
# arm64 hardware, and this script is how that hardware is reached reproducibly
# instead of by hand.
#
# The tree is rsynced rather than pulled from git, so uncommitted work is
# measured too -- which is the point when the change under test is the kernel.
#
# Usage:
#   GLOCKENSPIEL_ARM64_HOST=user@host scripts/bench-remote.sh
#   GLOCKENSPIEL_ARM64_HOST=user@host scripts/bench-remote.sh -bench . ./model
#
# Environment:
#   GLOCKENSPIEL_ARM64_HOST  required, ssh destination, e.g. user@192.0.2.10
#   GLOCKENSPIEL_ARM64_GO    remote go binary, default <remote home>/go/bin/go
#   GLOCKENSPIEL_ARM64_DIR   remote scratch dir, default
#                            <remote home>/.cache/glockenspiel-bench
#
# Arguments after the script name replace the default `go test` arguments, so
# the same plumbing runs the test suite:
#
#   GLOCKENSPIEL_ARM64_HOST=user@host scripts/bench-remote.sh -race ./...
set -euo pipefail

if [ -z "${GLOCKENSPIEL_ARM64_HOST:-}" ]; then
	echo "Error: GLOCKENSPIEL_ARM64_HOST is not set." >&2
	echo "" >&2
	echo "       It names the ssh destination of a native arm64 host, and key" >&2
	echo "       auth has to already work there:" >&2
	echo "" >&2
	echo "         GLOCKENSPIEL_ARM64_HOST=user@host $0" >&2
	echo "" >&2
	echo "       Optional: GLOCKENSPIEL_ARM64_GO (default" >&2
	echo "                 <remote home>/go/bin/go) and GLOCKENSPIEL_ARM64_DIR" >&2
	echo "                 (default <remote home>/.cache/glockenspiel-bench)." >&2
	exit 2
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Every one of the three inputs reaches a remote shell, so each is checked
# before it is used rather than trusted.
#
# The host goes to ssh and rsync as a bare argument: a value starting with "-"
# is parsed as an option instead of a destination.
host="$GLOCKENSPIEL_ARM64_HOST"
case "$host" in
-*)
	echo "Error: GLOCKENSPIEL_ARM64_HOST must not start with '-': $host" >&2
	echo "       ssh would parse it as an option rather than a destination." >&2
	exit 2
	;;
esac

# An allowlist rather than a metacharacter blocklist: the destination is only
# ever [user@]host or [user@][v6addr], so anything outside that alphabet is a
# mistake, and enumerating what is allowed cannot miss a case the way
# enumerating what is forbidden can.
if ! printf %s "$host" | grep -Eq '^[][A-Za-z0-9._@:%-]+$'; then
	echo "Error: GLOCKENSPIEL_ARM64_HOST is not a plain ssh destination:" >&2
	echo "       $host" >&2
	echo "       Expected [user@]host, for example user@192.0.2.10." >&2
	exit 2
fi

# The remote home directory is resolved once, over ssh, and every remote path is
# built absolute from it. Leaving "$HOME" in the strings does not work: rsync
# protects its remote arguments, so an unexpanded $HOME in the destination
# creates a directory literally named "$HOME" instead of landing in the home
# directory.
remote_home="$(ssh "$host" 'printf %s "$HOME"')"
if [ -z "$remote_home" ]; then
	echo "Error: could not resolve the remote home directory on $host." >&2
	exit 1
fi

remote_go="${GLOCKENSPIEL_ARM64_GO:-$remote_home/go/bin/go}"
remote_dir="${GLOCKENSPIEL_ARM64_DIR:-$remote_home/.cache/glockenspiel-bench}"

# The sync below runs with --delete, which removes everything in the destination
# that is not in the checkout. Pointed at a shared directory that is not a
# mistake with a small blast radius, so the destination is refused unless it is
# a dedicated scratch directory: absolute, at least two components deep, and
# neither the remote home itself nor a system root.
case "$remote_dir" in
/*) ;;
*)
	echo "Error: GLOCKENSPIEL_ARM64_DIR must be an absolute path: $remote_dir" >&2
	exit 2
	;;
esac

# Stripped after the absolute-path check, not before, so that "/" is reported by
# the depth check below rather than as an empty relative path.
remote_dir="${remote_dir%/}"

case "$remote_dir" in
*/../* | */..)
	echo "Error: GLOCKENSPIEL_ARM64_DIR must not contain '..': $remote_dir" >&2
	exit 2
	;;
esac

if [ "$remote_dir" = "$remote_home" ]; then
	echo "Error: GLOCKENSPIEL_ARM64_DIR is the remote home directory." >&2
	echo "       The sync runs with --delete and would remove everything in" >&2
	echo "       it that is not in this checkout. Use a scratch subdirectory," >&2
	echo "       for example $remote_home/.cache/glockenspiel-bench." >&2
	exit 2
fi

# Two components below the root, so "/", "/tmp" and "/Users/someone" are all
# refused while "/Users/someone/.cache/glockenspiel-bench" is not.
remote_dir_depth="$(printf %s "${remote_dir#/}" | awk -F/ '{print NF}')"
if [ "${remote_dir_depth:-0}" -lt 2 ]; then
	echo "Error: GLOCKENSPIEL_ARM64_DIR is too shallow to sync into with" >&2
	echo "       --delete: $remote_dir" >&2
	echo "       Use a dedicated scratch directory at least two levels deep," >&2
	echo "       for example $remote_home/.cache/glockenspiel-bench." >&2
	exit 2
fi

# The default selection is the set docs/oscillator-bank.md quotes. They are run
# together on purpose: the packed-versus-portable ratio is only meaningful when
# both halves come out of one binary in one thermal state. `-count 10` is there
# so the reader can take medians rather than trust a single run.
if [ "$#" -gt 0 ]; then
	test_args=("$@")
else
	test_args=(
		-run '^$'
		-bench '^(BenchmarkBank4x4|BenchmarkBank4x4Portable|BenchmarkBank16x4|BenchmarkBank64x4|BenchmarkReduceLanes4x4)$'
		-benchmem
		-count 10
		./internal/oscbank
	)
fi

# go.mod carries a `replace` for github.com/cwbudde/vst3go that points at a
# sibling checkout, which will not exist over there. That is harmless: every
# file under plugin/ is behind `linux && cgo && vst3go`, so nothing the remote
# builds ever loads the module.
echo "Syncing $repo_root to $host:$remote_dir ..."
rsync -az --delete \
	--exclude '.git/' \
	--exclude 'out/' \
	--exclude '.tmp/' \
	--exclude 'bin/' \
	--exclude '.claude/' \
	--exclude 'node_modules/' \
	--rsync-path="mkdir -p $(printf %q "$remote_dir") && rsync" \
	"$repo_root/" "$host:$remote_dir/"

# GOPATH is set explicitly because a Go installed by unpacking the tarball into
# ~/go leaves GOROOT and the default GOPATH the same directory, and the
# toolchain warns on every invocation when they collide.
printf 'Running on %s: go test' "$host"
printf ' %q' "${test_args[@]}"
printf '\n'

remote_cmd="cd $(printf '%q' "$remote_dir") && GOPATH=$(printf '%q' "$remote_home/gopath") $(printf '%q' "$remote_go") test"
for arg in "${test_args[@]}"; do
	remote_cmd+=" $(printf '%q' "$arg")"
done

exec ssh "$host" "$remote_cmd"
