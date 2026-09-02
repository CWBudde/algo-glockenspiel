package cli

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/spf13/cobra"
)

// cmaesFitOptions returns options for a short CMA-ES run.
func cmaesFitOptions(referencePath, outputPath, workDir string) fitOptions {
	options := baseFitOptions(referencePath, outputPath, workDir)
	options.optimizerName = "cmaes"
	options.cmaesCovariance = "separable"
	options.cmaesLambda = 4
	options.cmaesSigma = 0.3
	options.cmaesRestarts = 1
	options.maxIter = 2

	return options
}

// latestCMAESCheckpointState returns the CMA-ES environment of the newest
// checkpoint.
func latestCMAESCheckpointState(t *testing.T, workDir string) *optimizer.CMAESCheckpointEnv {
	t.Helper()

	path, err := optimizer.FindLatestCheckpoint(workDir)
	if err != nil {
		t.Fatalf("FindLatestCheckpoint failed: %v", err)
	}

	cp, err := optimizer.LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}

	if cp.State == nil || cp.State.CMAES == nil {
		t.Fatalf("expected checkpoint %s to carry cmaes state", path)
	}

	return cp.State.CMAES
}

func TestRunFitReportsAndCheckpointsTheResolvedCMAESSettings(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)
	outputPath := filepath.Join(dir, "fitted.json")
	workDir := filepath.Join(dir, "work")

	cmd := &cobra.Command{}

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	options := cmaesFitOptions(referencePath, outputPath, workDir)
	// Zero asks the backend to draw a seed, which is the case the checkpoint
	// has to record: without the resolved value a resumed run would draw a
	// different stream.
	options.seed = 0

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "cmaes: covariance=separable lambda=4 sigma=0.3 seed=") {
		t.Fatalf("expected the resolved cmaes line, got %q", text)
	}

	if !strings.Contains(text, "restarts=") {
		t.Fatalf("expected the finished line to report the restart count, got %q", text)
	}

	state := latestCMAESCheckpointState(t, workDir)
	if state.Covariance != "separable" || state.Lambda != 4 || state.Sigma != 0.3 {
		t.Fatalf("checkpoint recorded %+v, want the settings the run was given", state)
	}

	if state.Seed == 0 {
		t.Fatal("checkpoint recorded seed 0, want the seed the run resolved")
	}

	if state.Restarts != 1 {
		t.Fatalf("checkpoint recorded %d restarts, want 1", state.Restarts)
	}
}

func TestRunFitResumeRestoresCMAESSettingsFromCheckpoint(t *testing.T) {
	dir := t.TempDir()
	referencePath, template, reference := writeFitReference(t, dir)
	outputPath := filepath.Join(dir, "fitted.json")
	workDir := filepath.Join(dir, "work")

	objective, err := optimizer.NewObjectiveFunction(reference, template, 44100, 69, 100, optimizer.MetricRMS)
	if err != nil {
		t.Fatalf("NewObjectiveFunction failed: %v", err)
	}

	encoded, err := objective.Codec().EncodeParams(&template.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	if err = optimizer.SaveCheckpoint(filepath.Join(workDir, "checkpoint_0003.json"), &optimizer.Checkpoint{
		Version:             optimizer.CheckpointVersion,
		Iteration:           3,
		OptimizerIterations: 1,
		BestCost:            0.5,
		BestParams:          encoded,
		Optimizer:           "cmaes",
		Metric:              "rms",
		State: &optimizer.OptimizerState{
			Kind: "cmaes",
			CMAES: &optimizer.CMAESCheckpointEnv{
				Covariance: "block",
				Lambda:     6,
				Sigma:      0.5,
				Seed:       4242,
				Restarts:   1,
			},
		},
	}); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	cmd := &cobra.Command{}

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	// The optimizer named here is the one the checkpoint overrides: a resumed
	// run continues the search it was written by, backend included.
	options := baseFitOptions(referencePath, outputPath, workDir)
	options.metric = "rms"
	options.maxIter = 3
	options.resume = true

	if err = runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "cmaes: covariance=block lambda=6 sigma=0.5 seed=4242") {
		t.Fatalf("expected the checkpoint's cmaes settings to be restored, got %q", text)
	}

	if !strings.Contains(text, "optimizer=cmaes") {
		t.Fatalf("expected the resume line to name the cmaes backend, got %q", text)
	}
}

func TestRunFitResumeKeepsACMAESFlagTheCallerWrote(t *testing.T) {
	dir := t.TempDir()
	referencePath, template, reference := writeFitReference(t, dir)
	outputPath := filepath.Join(dir, "fitted.json")
	workDir := filepath.Join(dir, "work")

	objective, err := optimizer.NewObjectiveFunction(reference, template, 44100, 69, 100, optimizer.MetricRMS)
	if err != nil {
		t.Fatalf("NewObjectiveFunction failed: %v", err)
	}

	encoded, err := objective.Codec().EncodeParams(&template.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	if err = optimizer.SaveCheckpoint(filepath.Join(workDir, "checkpoint_0003.json"), &optimizer.Checkpoint{
		Version:             optimizer.CheckpointVersion,
		Iteration:           3,
		OptimizerIterations: 1,
		BestCost:            0.5,
		BestParams:          encoded,
		Optimizer:           "cmaes",
		Metric:              "rms",
		State: &optimizer.OptimizerState{
			Kind:  "cmaes",
			CMAES: &optimizer.CMAESCheckpointEnv{Covariance: "block", Lambda: 6, Sigma: 0.5, Seed: 4242},
		},
	}); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	// A flag written on the resume command wins over what the checkpoint
	// recorded, exactly as it does for the mayfly settings.
	cmd := &cobra.Command{}
	cmd.Flags().Int("cmaes-lambda", 0, "")

	if err = cmd.Flags().Set("cmaes-lambda", "5"); err != nil {
		t.Fatalf("set cmaes-lambda: %v", err)
	}

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	options := baseFitOptions(referencePath, outputPath, workDir)
	options.metric = "rms"
	options.maxIter = 3
	options.resume = true
	options.cmaesLambda = 5

	if err = runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	if text := out.String(); !strings.Contains(text, "covariance=block lambda=5 sigma=0.5 seed=4242") {
		t.Fatalf("expected the written lambda to survive the resume, got %q", text)
	}
}
