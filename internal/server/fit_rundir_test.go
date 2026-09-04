package server_test

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

// runDirName is the shape a job id and its directory both have: a UTC instant
// that sorts chronologically, and a counter that separates two fits started in
// the same second.
var runDirName = regexp.MustCompile(`^fit-\d{8}T\d{6}-\d{4,}$`)

// A served fit leaves the same run directory a campaign job leaves. This is
// the whole point of the server running its fits through internal/fitrun: the
// files below are read by name by the campaign's collect step, out of
// directories it did not write, so a fit started from the browser has to be
// indistinguishable from one started by the campaign runner.
func TestAServedFitWritesARunDirectory(t *testing.T) {
	workDir := t.TempDir()
	handler := newFitServerIn(t, workDir).Handler()

	started := startFit(t, handler, shortFit())
	if started.Code != http.StatusAccepted {
		t.Fatalf("start = %d: %s", started.Code, started.Body.String())
	}

	final := waitForTerminalState(t, handler, 60*time.Second)
	if final.State != "succeeded" {
		t.Fatalf("state = %q (error %q), want succeeded", final.State, final.Error)
	}

	if !runDirName.MatchString(final.JobID) {
		t.Fatalf("job id %q is not a run directory name", final.JobID)
	}

	// The id and the directory are one string, which is what lets a client
	// that has only a job id find the run it names.
	dir := filepath.Join(workDir, final.JobID)

	wanted := []string{
		fitrun.FileConfig,
		fitrun.FileAnalysis,
		fitrun.FileTrace,
		fitrun.FileCheckpoint,
		fitrun.FilePreset,
		fitrun.FileRender,
		fitrun.FileResult,
		fitrun.FileLog,
		fitrun.FileReference,
		"upload.wav",
	}

	for _, name := range wanted {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s is missing from the run directory: %v", name, err)
		}

		if info.Size() == 0 {
			t.Fatalf("%s is empty", name)
		}
	}

	// The preset in the directory is the one the endpoint serves, so a client
	// and the campaign tooling read the same fit rather than two.
	saved, err := os.ReadFile(filepath.Join(dir, fitrun.FilePreset))
	if err != nil {
		t.Fatalf("preset.json could not be read: %v", err)
	}

	fitted, err := preset.Decode(saved, "the saved preset")
	if err != nil {
		t.Fatalf("the saved preset does not validate: %v", err)
	}

	if fitted.Provenance == nil {
		t.Fatal("the saved preset carries no provenance block")
	}
}

// A second fit gets a directory of its own rather than writing over the first.
func TestEachServedFitGetsItsOwnRunDirectory(t *testing.T) {
	workDir := t.TempDir()
	handler := newFitServerIn(t, workDir).Handler()

	var ids []string

	for range 2 {
		started := startFit(t, handler, shortFit())
		if started.Code != http.StatusAccepted {
			t.Fatalf("start = %d: %s", started.Code, started.Body.String())
		}

		final := waitForTerminalState(t, handler, 60*time.Second)
		if final.State != "succeeded" {
			t.Fatalf("state = %q (error %q), want succeeded", final.State, final.Error)
		}

		ids = append(ids, final.JobID)
	}

	if ids[0] == ids[1] {
		t.Fatalf("both fits claimed %q", ids[0])
	}

	entries, err := os.ReadDir(workDir)
	if err != nil {
		t.Fatalf("the work directory could not be read: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("the work directory holds %d entries, want 2", len(entries))
	}
}
