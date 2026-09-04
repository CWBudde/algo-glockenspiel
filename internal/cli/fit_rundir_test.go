package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/spf13/cobra"
)

// TestFitWritesARunDirectory pins the whole point of running the command
// through internal/fitrun: --work-dir is a run directory now, holding the same
// files a campaign job and a served fit leave behind. The campaign's collect
// step and the server's restore scan read these by name out of directories they
// did not write, so a fit started from a terminal has to be indistinguishable
// from one started by either of the other two.
//
// The list is the one internal/server/fit_rundir_test.go asserts, minus the
// upload the server writes because the reference it fits was posted to it. A
// fit from the command line names a file that is already on disk.
func TestFitWritesARunDirectory(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)
	outputPath := filepath.Join(dir, "fitted.json")
	workDir := filepath.Join(dir, "run")

	options := baseFitOptions(referencePath, outputPath, workDir)
	// A handful of iterations rather than the single one the other tests use:
	// every file has to be non-empty, and a search that reported once has
	// written only the first line of its trace.
	options.maxIter = 5

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	for _, name := range []string{
		fitrun.FileConfig,
		fitrun.FileAnalysis,
		fitrun.FileTrace,
		fitrun.FileCheckpoint,
		fitrun.FilePreset,
		fitrun.FileRender,
		fitrun.FileResult,
		fitrun.FileLog,
		fitrun.FileReference,
	} {
		info, err := os.Stat(filepath.Join(workDir, name))
		if err != nil {
			t.Fatalf("%s is missing from the run directory: %v", name, err)
		}

		if info.Size() == 0 {
			t.Fatalf("%s is empty", name)
		}
	}

	// --output is a copy filed where the operator asked for it, not a second
	// fit: the preset in the run directory is the same bytes, provenance block
	// included, so whichever one is picked up says the same thing.
	inDirectory, err := os.ReadFile(filepath.Join(workDir, fitrun.FilePreset))
	if err != nil {
		t.Fatalf("preset.json could not be read: %v", err)
	}

	requested, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("the --output preset could not be read: %v", err)
	}

	if string(inDirectory) != string(requested) {
		t.Fatal("the preset in the run directory differs from the one written to --output")
	}

	fitted, err := preset.Decode(inDirectory, "the saved preset")
	if err != nil {
		t.Fatalf("the saved preset does not validate: %v", err)
	}

	if fitted.Provenance == nil || fitted.Provenance.GeneratedBy != fitGeneratedBy {
		t.Fatalf("provenance = %#v, want the fit command's own marker", fitted.Provenance)
	}
}
