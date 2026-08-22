# TinyGo Evaluation for the Web Build

Date: 2026-08-22

Decision: **the browser demo keeps using the standard Go toolchain.** This note records
why, so that the question is not reopened from scratch, and names the conditions under
which the answer changes.

## What Prompted It

The WebAssembly module is by far the largest thing the page downloads. Standard Go emits a
whole runtime -- garbage collector, scheduler, type metadata -- into every module, and
TinyGo exists in large part to avoid that. Measured on this repository at the time of
writing (Go 1.26.5, `GOOS=js GOARCH=wasm`, `cmd/glockenspiel-wasm`):

| Build                         | Bytes     |
| ----------------------------- | --------- |
| plain `go build`              | 3,476,521 |
| `-trimpath -ldflags="-s -w"`  | 3,399,530 |
| the above plus `wasm-opt -O3` | 3,212,389 |

So the flags in `scripts/build-wasm.sh` recover 7.6%. TinyGo routinely does an order of
magnitude better on small programs, which is the reason to take it seriously rather than
dismiss it.

## Why Not, Today

**1. It cannot build this module at all.** TinyGo tracks the Go release it was built
against and refuses anything newer:

```
$ tinygo version
tinygo version 0.37.0 linux/amd64 (using go version go1.26.5 and LLVM version 19.1.2)

$ tinygo build -o /tmp/tinygo.wasm -target wasm ./cmd/glockenspiel-wasm
requires go version 1.19 through 1.24, got go1.26
```

`go.mod` declares `go 1.25.0`. Adopting TinyGo therefore means pinning the whole
repository -- not just the web build -- to a Go release older than the one it already
requires, or waiting for a TinyGo release that catches up. That is a project-wide
constraint bought for one artifact.

**2. It would fork the bridge, not just the compiler.** TinyGo ships its own
`wasm_exec.js` with a different runtime contract from the upstream one. `web/wasm_exec.js`
is tracked in git, listed file by file in `web/embed.go`, and embedded into the `serve`
binary. Supporting both toolchains means two loader scripts, two embed entries and a front
end that knows which one it got; supporting only TinyGo means the module can no longer be
built by a plain `go build`, which is the fallback documented in `web/README.md` and the
one that works on any machine with a Go toolchain.

**3. The dependency surface is not the kind TinyGo is happiest with.** The module pulls in
`internal/preset` (`encoding/json`, hence `reflect`), `go:embed`, `model` and
`internal/oscbank` (both of which carry Plan 9 assembly for amd64 and arm64 -- build-tagged
away for wasm, but they define the shape of these packages), and `algo-vecmath/cpu`. None
of that is knowably fatal, but each is a place where TinyGo's partial `reflect` and
different runtime behaviour would have to be verified rather than assumed, and the audio
path additionally depends on the scheduler behaving sanely around `syscall/js` callbacks.

**4. The payload is not the bottleneck we actually have.** The module is fetched once per
build and revalidated by ETag afterwards, and this is a local demo served from `localhost`
or a static host. The measurable first-paint cost on this page today is
`web/wood-texture.js` filling a 1024x576 canvas pixel by pixel before the WASM fetch even
starts (Phase 5.4), not the three megabytes.

## What Would Change the Answer

- A TinyGo release that supports the Go version in `go.mod`, removing objection 1.
- Shipping this demo as a product rather than a demo, where a 3 MB download on a phone is a
  real cost and the effort of maintaining a second bridge is justified.
- The engine losing its `encoding/json` and `reflect` dependency at the wasm boundary --
  for instance, if the preset were baked in as generated Go rather than decoded at runtime,
  which would also cut the standard-Go module.

Until then the size work stays where `scripts/build-wasm.sh` already puts it: `-trimpath`,
`-s -w`, and an optional `wasm-opt` pass.
