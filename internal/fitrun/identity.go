package fitrun

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
)

// unknownValue is what an identity field holds when the build carries no
// answer for it. It is never the empty string, because a blank field in a
// recorded run reads as "nobody wrote this yet" rather than "this build could
// not say".
const unknownValue = "unknown"

// The module paths of the two search libraries whose versions move the
// numbers a campaign records, and the short names the record keys them by.
const (
	mayflyModule = "github.com/cwbudde/mayfly"
	cmaesModule  = "github.com/CWBudde/go-cma-es"

	// MayflyLibrary and CMAESLibrary are the keys of Identity.Libraries.
	MayflyLibrary = "mayfly"
	CMAESLibrary  = "go-cma-es"
)

// Identity is the build that ran a fit.
//
// A campaign's results are only comparable across runs that were produced by
// the same code, and the code here is the repository plus two search
// libraries, so all three are recorded. Revision is empty in a build with no
// VCS stamp, which is the normal case under `go test` and `go run`, and is
// reported as "unknown" rather than as a blank. A build made from a dirty
// working tree reports its revision with a "+dirty" suffix, because the commit
// alone would name code that is not what ran.
type Identity struct {
	Go        string            `json:"go"`
	Revision  string            `json:"revision"`
	Modified  bool              `json:"modified"`
	Time      string            `json:"time"`
	Libraries map[string]string `json:"libraries"`
}

// ReadIdentity reads the build identity out of the running binary.
func ReadIdentity() Identity {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return identityFrom(nil)
	}

	return identityFrom(info)
}

// identityFrom derives the identity from build information, which is a
// separate function so that the derivation can be tested against a constructed
// debug.BuildInfo rather than against whatever stamp the test binary happens
// to carry. A nil info is the build that could say nothing about itself.
func identityFrom(info *debug.BuildInfo) Identity {
	identity := Identity{
		Go:       runtime.Version(),
		Revision: unknownValue,
		Time:     unknownValue,
		Libraries: map[string]string{
			MayflyLibrary: unknownValue,
			CMAESLibrary:  unknownValue,
		},
	}

	if info == nil {
		return identity
	}

	if info.GoVersion != "" {
		identity.Go = info.GoVersion
	}

	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if setting.Value != "" {
				identity.Revision = setting.Value
			}
		case "vcs.modified":
			identity.Modified = setting.Value == "true"
		case "vcs.time":
			if setting.Value != "" {
				identity.Time = setting.Value
			}
		}
	}

	for _, dep := range info.Deps {
		// A replace directive is what a local checkout of either library looks
		// like, and its version is the one that ran, so it wins over the
		// version the requirement names.
		version := dep.Version
		if dep.Replace != nil && dep.Replace.Version != "" {
			version = dep.Replace.Version
		}

		if version == "" {
			continue
		}

		switch dep.Path {
		case mayflyModule:
			identity.Libraries[MayflyLibrary] = version
		case cmaesModule:
			identity.Libraries[CMAESLibrary] = version
		}
	}

	identity.Revision = revisionOf(identity.Revision, identity.Modified)

	return identity
}

// revisionOf marks a revision built from a dirty working tree. The commit hash
// alone would claim the run is reproducible from that commit, which is exactly
// what an uncommitted edit makes false, and a campaign read months later is
// read by whoever finds the recorded revision rather than by whoever remembers
// the state of the tree that day. Modified is kept as its own field so that a
// reader need not parse the suffix.
func revisionOf(revision string, modified bool) string {
	if !modified || revision == unknownValue || revision == "" {
		return revision
	}

	return revision + "+dirty"
}

// FileSHA256 hashes a file. It is how a run pins the reference it was scored
// against: a path is a name someone can reuse for a different recording, and a
// campaign that compared two such runs would be comparing nothing.
func FileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %q: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("hash %q: %w", path, err)
	}

	return hex.EncodeToString(digest.Sum(nil)), nil
}
