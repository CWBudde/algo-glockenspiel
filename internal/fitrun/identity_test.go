package fitrun

import (
	"runtime/debug"
	"testing"
)

// TestIdentityMarksADirtyTree is the regression test for a run recorded as if it
// came from a clean commit. A campaign is read months later by whoever finds
// the revision in results.csv, and a bare hash there is a promise that the
// numbers can be reproduced from that commit.
func TestIdentityMarksADirtyTree(t *testing.T) {
	const revision = "0123456789abcdef0123456789abcdef01234567"

	info := &debug.BuildInfo{
		GoVersion: "go1.24.0",
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.modified", Value: "true"},
			{Key: "vcs.time", Value: "2026-01-02T03:04:05Z"},
		},
	}

	identity := identityFrom(info)

	if want := revision + "+dirty"; identity.Revision != want {
		t.Fatalf("revision = %q, want %q", identity.Revision, want)
	}

	if !identity.Modified {
		t.Fatal("modified = false, want true for a dirty tree")
	}

	if identity.Go != "go1.24.0" {
		t.Fatalf("go = %q, want go1.24.0", identity.Go)
	}
}

// TestIdentityLeavesACleanRevisionAlone is the other half: the suffix must not
// be attached to a commit that was built from an untouched tree, nor to the
// placeholder a build with no stamp reports.
func TestIdentityLeavesACleanRevisionAlone(t *testing.T) {
	const revision = "0123456789abcdef0123456789abcdef01234567"

	info := &debug.BuildInfo{
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.modified", Value: "false"},
		},
	}

	if identity := identityFrom(info); identity.Revision != revision {
		t.Fatalf("revision = %q, want %q", identity.Revision, revision)
	}

	// An unstamped build reports the placeholder, and a dirty flag with no
	// revision to attach to must not turn it into "unknown+dirty".
	unstamped := identityFrom(&debug.BuildInfo{
		Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}},
	})
	if unstamped.Revision != unknownValue {
		t.Fatalf("unstamped revision = %q, want %q", unstamped.Revision, unknownValue)
	}

	if identityFrom(nil).Revision != unknownValue {
		t.Fatalf("revision with no build info = %q, want %q", identityFrom(nil).Revision, unknownValue)
	}
}
