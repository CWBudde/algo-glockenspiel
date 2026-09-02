package cli

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/spf13/cobra"
)

// fitCmdWithFlags returns a fit command with the named flags written, so a
// test can exercise the "was this flag given" rules without parsing a command
// line.
func fitCmdWithFlags(t *testing.T, values map[string]string) *cobra.Command {
	t.Helper()

	cmd := newFitCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	for name, value := range values {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatalf("set --%s: %v", name, err)
		}
	}

	return cmd
}

func TestFitCmdMarksThePerBackendSeedFlagsDeprecated(t *testing.T) {
	cmd := newFitCmd()

	if got := cmd.Flags().Lookup("seed").DefValue; got != "0" {
		t.Fatalf("expected --seed to default to 0, which picks a seed and reports it, got %q", got)
	}

	for _, name := range []string{"mayfly-seed", "cmaes-seed"} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Fatalf("expected --%s to stay available as an alias", name)
		}

		if !strings.Contains(flag.Deprecated, "--seed") {
			t.Fatalf("expected --%s to point at --seed, got %q", name, flag.Deprecated)
		}
	}
}

func TestTheDeprecatedSeedAliasesWriteTheSharedSeed(t *testing.T) {
	for _, alias := range []string{"mayfly-seed", "cmaes-seed"} {
		t.Run(alias, func(t *testing.T) {
			cmd := fitCmdWithFlags(t, map[string]string{alias: "4242"})

			if got := cmd.Flags().Lookup("seed").Value.String(); got != "4242" {
				t.Fatalf("expected --%s to write the shared seed, got --seed = %q", alias, got)
			}
		})
	}
}

func TestRunFitRejectsASeedAliasCombinedWithSeed(t *testing.T) {
	for _, alias := range []string{"mayfly-seed", "cmaes-seed"} {
		t.Run(alias, func(t *testing.T) {
			cmd := fitCmdWithFlags(t, map[string]string{"seed": "1", alias: "2"})

			options := baseFitOptions("reference.wav", "fitted.json", t.TempDir())

			err := runFit(cmd, options)
			if err == nil {
				t.Fatal("expected the seed to be refused when it is named twice")
			}

			if !strings.Contains(err.Error(), "--seed") || !strings.Contains(err.Error(), "--"+alias) {
				t.Fatalf("expected the error to name both flags, got %q", err)
			}
		})
	}
}

func TestRunFitRejectsBothSeedAliasesAtOnce(t *testing.T) {
	cmd := fitCmdWithFlags(t, map[string]string{"mayfly-seed": "1", "cmaes-seed": "2"})

	options := baseFitOptions("reference.wav", "fitted.json", t.TempDir())

	err := runFit(cmd, options)
	if err == nil {
		t.Fatal("expected two aliases for one option to be refused")
	}
}

func TestRunFitRecordsTheResolvedWorkerCountAndRestoresItOnResume(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)
	outputPath := filepath.Join(dir, "fitted.json")
	workDir := filepath.Join(dir, "work")

	// A width the machine cannot have chosen on its own, so a restore that
	// silently fell back to the CPU count fails here instead of passing on a
	// runner that happens to have exactly this many cores.
	width := runtime.NumCPU() + 1
	wantWorkers := fmt.Sprintf("workers=%d", width)

	options := cmaesFitOptions(referencePath, outputPath, workDir)
	options.workers = width

	var out bytes.Buffer

	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	if !strings.Contains(out.String(), wantWorkers) {
		t.Fatalf("expected the resolve line to report the width, got %q", out.String())
	}

	path, err := optimizer.FindLatestCheckpoint(workDir)
	if err != nil {
		t.Fatalf("FindLatestCheckpoint failed: %v", err)
	}

	cp, err := optimizer.LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}

	if cp.State == nil || cp.State.Workers != width {
		t.Fatalf("expected the checkpoint to record %d workers, got %+v", width, cp.State)
	}

	// A resume that says nothing about the width takes the checkpoint's, so a
	// fit continued on a machine with a different CPU count keeps its shape.
	resume := newFitCmd()

	var resumed bytes.Buffer

	resume.SetOut(&resumed)
	resume.SetErr(io.Discard)

	continued := cmaesFitOptions(referencePath, outputPath, workDir)
	continued.resume = true
	continued.maxIter = 4

	if err := runFit(resume, continued); err != nil {
		t.Fatalf("resumed runFit failed: %v", err)
	}

	if !strings.Contains(resumed.String(), wantWorkers) {
		t.Fatalf("expected the resumed run to keep the recorded width, got %q", resumed.String())
	}
}

func TestRunFitCheckpointsOnOptimizerIterationsNotOnReports(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)
	workDir := filepath.Join(dir, "work")

	options := cmaesFitOptions(referencePath, filepath.Join(dir, "fitted.json"), workDir)
	options.maxIter = 12
	options.reportEvery = 1
	options.checkpointEvery = 5
	options.workers = 1

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(workDir, "checkpoint_*.json"))
	if err != nil {
		t.Fatalf("glob checkpoints: %v", err)
	}

	// Twelve iterations reported one at a time used to mean twelve
	// checkpoints at an interval of five, because the interval counted
	// reports. It now counts the backend's own iterations: one once five have
	// passed, one once ten have, and the final one every run writes.
	if len(matches) != 3 {
		t.Fatalf("expected three checkpoints from twelve iterations at an interval of five, got %v", matches)
	}

	counts := make([]int, 0, len(matches))

	for _, match := range matches {
		cp, err := optimizer.LoadCheckpoint(match)
		if err != nil {
			t.Fatalf("LoadCheckpoint failed: %v", err)
		}

		counts = append(counts, cp.OptimizerIterations)
	}

	if counts[0] < 5 || counts[1] < 10 || counts[1]-counts[0] < 5 {
		t.Fatalf("expected the periodic checkpoints at five iterations apart, got %v", counts)
	}

	if counts[2] != 12 {
		t.Fatalf("expected the final checkpoint at the run's last iteration, got %v", counts)
	}
}

func TestTuningFromFlagsWritesAnExplicitStagnationZero(t *testing.T) {
	cmd := fitCmdWithFlags(t, map[string]string{"mayfly-stagnation": "0"})

	options := fitOptions{mayflyStagnation: 0}

	tuning := tuningFromFlags(cmd, options)
	if tuning == nil || tuning.Convergence == nil || tuning.Convergence.StagnationIterations == nil {
		t.Fatalf("expected an explicit zero to be written into the document, got %+v", tuning)
	}

	// Zero is what switches a preset's own stagnation rule off, so it has to
	// reach the document rather than read as "flag not given".
	if got := *tuning.Convergence.StagnationIterations; got != 0 {
		t.Fatalf("expected the document to carry zero, got %d", got)
	}
}

func TestTuningFromFlagsLeavesStagnationAloneWhenTheFlagIsNotGiven(t *testing.T) {
	cmd := fitCmdWithFlags(t, nil)

	if tuning := tuningFromFlags(cmd, fitOptions{}); tuning != nil {
		t.Fatalf("expected an untouched command line to write no document, got %+v", tuning)
	}
}

func TestTuningFromFlagsWritesAnExplicitNCRatioZero(t *testing.T) {
	cmd := fitCmdWithFlags(t, map[string]string{"mayfly-nc-ratio": "0"})

	tuning := tuningFromFlags(cmd, fitOptions{mayflyNCRatio: 0})
	if tuning == nil || tuning.NCRatio == nil {
		t.Fatalf("expected an explicit zero ratio to be written into the document, got %+v", tuning)
	}

	if got := *tuning.NCRatio; got != 0 {
		t.Fatalf("expected the document to carry zero, got %v", got)
	}
}

// TestRunFitWritesAStagnationZeroIntoTheCheckpointsTuning follows the explicit
// zero all the way to where a resume reads it back.
func TestRunFitWritesAStagnationZeroIntoTheCheckpointsTuning(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)
	workDir := filepath.Join(dir, "work")

	cmd := fitCmdWithFlags(t, map[string]string{"mayfly-stagnation": "0"})

	options := mayflyFitOptions(referencePath, filepath.Join(dir, "fitted.json"), workDir)
	options.mayflyStagnation = 0

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	state := latestCheckpointState(t, workDir)
	if state.Tuning == nil || state.Tuning.Convergence == nil || state.Tuning.Convergence.StagnationIterations == nil {
		t.Fatalf("expected the checkpoint's tuning to carry the stagnation rule, got %+v", state.Tuning)
	}

	if got := *state.Tuning.Convergence.StagnationIterations; got != 0 {
		t.Fatalf("expected the checkpoint to record stagnation 0, got %d", got)
	}
}
