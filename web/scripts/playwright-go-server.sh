#!/usr/bin/env bash
# Backs the second Playwright webServer entry: the real `glockenspiel serve`
# process the "real fit" spec drives, on port 8080.
#
# It is a script rather than a bare `go run` in playwright.config.ts because a
# developer without the Go toolchain must still be able to run
# `npm run test:visual` and get the existing WASM-path suite: if this script
# always exec'd `go build`, a missing toolchain would make Playwright's
# webServer startup fail and abort every project, including the ones that
# never touch the Go server. Instead, when `go` is unavailable, or the build
# itself fails, this falls back to a bare listener that answers the health
# check and nothing else -- the real-fit spec recognizes it is not talking to
# the real server (it never sees the 404 `/api/fit` answers when idle) and
# skips itself, while every other spec is unaffected.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"

fallback() {
	echo "playwright-go-server: $1; the real-fit spec will skip itself" >&2
	exec node -e '
    require("http")
      .createServer((_request, response) => {
        response.writeHead(200, { "content-type": "application/json" });
        response.end(JSON.stringify({ fallback: true }));
      })
      .listen(8080, "127.0.0.1");
  '
}

if ! command -v go >/dev/null 2>&1; then
	fallback "no go toolchain on PATH"
fi

# One directory, reused, rather than a fresh mktemp per start: this script
# execs the server and so never runs a cleanup of its own, and every start was
# therefore leaving another copy of a 15 MB binary in /tmp until the suite had
# filled a tmpfs and Chromium began failing to allocate. The binary is
# unlinked before it is rebuilt, which on Linux leaves a running server's own
# copy alone -- an unlinked inode stays alive for the process holding it --
# where writing over it in place would fail with "text file busy".
scratch="${TMPDIR:-/tmp}/glockenspiel-playwright-fit"
bin="$scratch/glockenspiel-fit-server"

mkdir -p "$scratch"
rm -f "$bin"

# Built into the scratch directory, never the repo root: the root carries a
# tracked build artifact (`glockenspiel-fit-wasm`) that a bare `go build`
# would silently overwrite.
if ! (cd "$repo_root" && go build -o "$bin" ./cmd/glockenspiel) 1>&2; then
	fallback "go build ./cmd/glockenspiel failed"
fi

# The work directory is a known path rather than one inside the scratch
# directory the binary was built in, because a spec has to be able to write
# into it: the server now follows run directories it finds there, and the only
# honest test of that is a real directory appearing under the real
# --work-dir. It lives under web/test-results, which is already ignored by git
# and already Playwright's own output directory, and the spec resolves the
# same path from its own location. Overriding it is for a developer running
# the server by hand; the spec reads the same variable.
work_dir="${GLOCKENSPIEL_E2E_WORK_DIR:-$repo_root/web/test-results/fit-work}"

# Cleared on every start so one run's synthetic directories are not a previous
# run's history. A server reused across runs (reuseExistingServer) keeps what
# it already adopted, which is why the follow spec identifies its own run by
# being the newest rather than by being the only one.
rm -rf "$work_dir"
mkdir -p "$work_dir"

# --dist is not needed for anything this test exercises -- the app itself is
# served by Vite, and only /api goes through this process -- but it is named
# explicitly anyway so the "app not built" warning does not fire spuriously:
# --dist defaults to "web/dist" relative to the process's own working
# directory, which Playwright sets to web/, not the repo root.
exec "$bin" serve --addr 127.0.0.1:8080 --dist "$repo_root/web/dist" --work-dir "$work_dir"
