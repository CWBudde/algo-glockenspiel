package cli

import (
	"bytes"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
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

// TestRunFitHonoursAnEvaluationBudget covers --max-evals, the flag that lets
// `glockenspiel fit` reproduce a campaign arm: the campaign matches arms on
// evaluations because an evaluation is one render whichever backend spends it,
// and before the flag existed the command could only be budgeted in iterations
// and seconds. The overrun of at most one generation is the contract: the
// library truncates its last generation, so in practice the cap is exact.
func TestRunFitHonoursAnEvaluationBudget(t *testing.T) {
	const budget = 300

	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)
	outputPath := filepath.Join(dir, "fitted.json")

	cmd := &cobra.Command{}

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	options := cmaesFitOptions(referencePath, outputPath, filepath.Join(dir, "work"))
	options.cmaesLambda = 0
	options.cmaesRestarts = 0
	options.maxIter = 100000
	options.timeBudget = time.Minute
	options.maxEvals = budget
	options.seed = 11

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "stop=max_evaluations") {
		t.Fatalf("expected the finished line to stop on the evaluation budget, got %q", text)
	}

	lambda := resolvedLambdaFrom(t, text)

	spent := finishedEvaluationsFrom(t, text)
	if spent < budget || spent >= budget+lambda {
		t.Fatalf("the run spent %d evaluations, want in [%d, %d)", spent, budget, budget+lambda)
	}

	// The provenance records the population that ran, not the zero that asked
	// the backend to choose one, which is what makes a fitted preset say which
	// search produced it.
	fitted, err := preset.Load(outputPath)
	if err != nil {
		t.Fatalf("load fitted preset: %v", err)
	}

	if fitted.Provenance == nil {
		t.Fatal("the fitted preset carries no provenance block")
	}

	if fitted.Provenance.Engine.Lambda != lambda {
		t.Fatalf("provenance lambda = %d, want the resolved %d",
			fitted.Provenance.Engine.Lambda, lambda)
	}
}

// TestRunFitReportsTheRestartLadder covers the two flags that shape the
// restart loop a campaign arm is written in: a per-run evaluation cap with no
// restart limit is cold restarts until the budget is spent, and a growth
// factor of two on the same loop is IPOP. Both are printed and both are
// recorded, because a checkpoint that dropped them would resume as a single
// fixed-population search.
func TestRunFitReportsTheRestartLadder(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)
	workDir := filepath.Join(dir, "work")

	cmd := &cobra.Command{}

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	options := cmaesFitOptions(referencePath, filepath.Join(dir, "fitted.json"), workDir)
	options.cmaesLambda = 4
	options.cmaesRestarts = 0
	options.maxIter = 100000
	options.timeBudget = time.Minute
	options.maxEvals = 200
	options.cmaesRunEvals = 40
	options.cmaesLambdaGrowth = 2
	options.seed = 11

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	if text := out.String(); !strings.Contains(text, "run-evals=40 lambda-growth=2") {
		t.Fatalf("expected the resolved line to report the ladder, got %q", text)
	}

	state := latestCMAESCheckpointState(t, workDir)
	if state.RunEvaluations != 40 || state.LambdaGrowth != 2 {
		t.Fatalf("checkpoint ladder = %d evaluations per run, growth %v, want 40 and 2",
			state.RunEvaluations, state.LambdaGrowth)
	}
}

// resolvedLambdaFrom reads the population off the resolved cmaes line.
func resolvedLambdaFrom(t *testing.T, text string) int {
	t.Helper()

	return intFieldFrom(t, text, "lambda=")
}

// finishedEvaluationsFrom reads the evaluation count off the Finished line,
// which is the run's total. Every progress line carries an evals= of its own,
// so the summary line has to be isolated first.
func finishedEvaluationsFrom(t *testing.T, text string) int {
	t.Helper()

	_, finished, ok := strings.Cut(text, "Finished:")
	if !ok {
		t.Fatalf("no Finished line in %q", text)
	}

	return intFieldFrom(t, finished, "evals=")
}

func intFieldFrom(t *testing.T, text, key string) int {
	t.Helper()

	_, after, ok := strings.Cut(text, key)
	if !ok {
		t.Fatalf("no %q in %q", key, text)
	}

	end := strings.IndexFunc(after, func(r rune) bool { return r < '0' || r > '9' })
	if end == 0 {
		t.Fatalf("no number after %q in %q", key, text)
	}

	if end < 0 {
		end = len(after)
	}

	value, err := strconv.Atoi(after[:end])
	if err != nil {
		t.Fatalf("parse %q from %q: %v", key, text, err)
	}

	return value
}
