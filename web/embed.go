// Package web carries the one page the glockenspiel binary can serve without a
// build step, so that `glockenspiel serve` on a fresh clone says something
// useful rather than nothing at all.
package web

import (
	"embed"
	"io/fs"
)

// placeholderFS holds the page shown when web/dist has not been built.
//
// The app itself is not here. It used to be: the front end was hand-written ES
// modules that a checkout contained in full, so embedding them was free. It is
// now a Vite bundle -- content-hashed file names, produced by `just build-web`
// -- and go:embed reads the working tree rather than git, so embedding a
// gitignored build directory would compile to an empty blob on a clone that has
// never run the build, or to a stale one that happens to be lying around. Phase
// 0's acceptance criteria forbid tracking build artifacts, which rules out the
// other way of making it work.
//
// So the split follows what is true: everything generated is read from disk by
// internal/server, and the binary carries exactly one honest fallback page that
// names the command producing the rest. That also keeps `go build ./...`
// working on a machine with no Node installed, which every CI job depends on.
//
//go:embed placeholder.html
var placeholderFS embed.FS

// StaticFS returns the embedded fallback tree rooted at the web directory, so
// callers see "placeholder.html" rather than "web/placeholder.html".
func StaticFS() fs.FS {
	return placeholderFS
}
