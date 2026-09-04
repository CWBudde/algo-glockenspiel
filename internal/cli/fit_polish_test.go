package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestFitCmdDefaultsToTheMeasuredShape pins the Phase 8.6 default: a bare
// `fit` runs the arm the campaign measured, mayfly in one warm round plus
// fifteen cold restarts, and every other backend stays selectable.
//
// It replaced the Phase 8.4 CMA-ES default, which was chosen before anything
// had been measured on this objective. engine-shape put separable CMA-ES
// 0.040 of score behind this arm over twelve paired blocks (p = 0.002 after
// Holm); docs/training.md holds the tables.
func TestFitCmdDefaultsToTheMeasuredShape(t *testing.T) {
	cmd := newFitCmd()

	for flag, want := range map[string]string{
		"optimizer":       "mayfly",
		"mayfly-epochs":   "1",
		"mayfly-restarts": "15",
		"cmaes-restarts":  "0",
	} {
		if got := cmd.Flags().Lookup(flag).DefValue; got != want {
			t.Errorf("--%s defaults to %q, want %q", flag, got, want)
		}
	}

	// Sixteen rounds split the iteration budget evenly, so the default has to
	// leave each round enough to anneal over.
	if got := cmd.Flags().Lookup("max-iter").DefValue; got != "640" {
		t.Errorf("--max-iter defaults to %q, want 640 so sixteen rounds get forty each", got)
	}

	for _, backend := range []string{"simple", "cmaes"} {
		if got := cmd.Flags().Lookup("optimizer").Usage; !strings.Contains(got, backend) {
			t.Errorf("%s is no longer selectable in the usage text: %q", backend, got)
		}
	}
}

func TestFitCmdPolishFlagDefaults(t *testing.T) {
	cmd := newFitCmd()

	for flag, want := range map[string]string{
		"polish":            "none",
		"polish-iterations": "200",
		"polish-budget":     "0s",
		"polish-sigma":      "0.02",
	} {
		lookup := cmd.Flags().Lookup(flag)
		if lookup == nil {
			t.Fatalf("expected a --%s flag", flag)
		}

		if lookup.DefValue != want {
			t.Fatalf("unexpected --%s default: got %q want %q", flag, lookup.DefValue, want)
		}
	}
}

func TestRunFitRejectsAnUnknownPolishEngine(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)

	options := baseFitOptions(referencePath, filepath.Join(dir, "fitted.json"), filepath.Join(dir, "work"))
	options.polish = "gradient-descent"

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := runFit(cmd, options)
	if err == nil || !strings.Contains(err.Error(), "polish engine") {
		t.Fatalf("expected the unknown engine to be named, got %v", err)
	}
}

func TestRunFitReportsThePolishStage(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)

	options := baseFitOptions(referencePath, filepath.Join(dir, "fitted.json"), filepath.Join(dir, "work"))
	options.polish = "nelder-mead"
	options.polishIterations = 5
	options.polishSigma = 0.02

	var out bytes.Buffer

	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	line := out.String()
	if !strings.Contains(line, "polish (nelder-mead): primary ") {
		t.Fatalf("expected the polish line, got %q", line)
	}

	if !strings.Contains(line, "accepted") && !strings.Contains(line, "rejected") {
		t.Fatalf("expected the polish line to state its verdict, got %q", line)
	}
}

func TestRunFitRejectsAPolishStepWiderThanTheBox(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)

	outputPath := filepath.Join(dir, "fitted.json")

	options := baseFitOptions(referencePath, outputPath, filepath.Join(dir, "work"))
	options.polish = "cmaes"
	options.polishIterations = 5
	options.polishSigma = 2

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := runFit(cmd, options)
	if err == nil || !strings.Contains(err.Error(), "polish-sigma") {
		t.Fatalf("expected the flag to be named, got %v", err)
	}

	// The refusal has to land before the search, not after it: the point of
	// validating here is that an operator does not spend a whole fit to be told
	// about a flag.
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no preset to be written, got %v", statErr)
	}
}
