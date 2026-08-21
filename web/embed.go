// Package web carries the browser front end of the glockenspiel as embedded
// files, so that `glockenspiel serve` can host it from a single binary.
package web

import (
	"embed"
	"io/fs"
)

// staticFS holds the hand-written part of the web app: everything that is
// tracked in git and therefore present in any clone.
//
// The generated part -- web/dist/glockenspiel.wasm, produced by
// scripts/build-wasm.sh -- is deliberately not embedded. web/dist is gitignored
// while go:embed reads the working tree, so embedding it would compile to an
// empty or stale blob on a clone that has never run `just build-web`, and the
// served page would fail at runtime with nothing in the build pointing at the
// cause. The server reads the wasm from disk instead and says so out loud when
// it is missing; see internal/server.
//
// The patterns are spelled out file by file rather than embedding the whole
// directory, because a directory pattern would pull in web/dist whenever it
// happens to exist and make the binary's contents depend on the state of an
// ignored directory. Note also that go:embed skips names beginning with "." or
// "_" unless the "all:" prefix is used; nothing under web/assets uses such a
// name today, so the plain form is enough and the omission stays visible here.
//
//go:embed index.html main.js ui.js wood-texture.js styles.css wasm_exec.js assets
var staticFS embed.FS

// StaticFS returns the embedded static web tree rooted at the web directory,
// so callers see "index.html" rather than "web/index.html".
func StaticFS() fs.FS {
	return staticFS
}
