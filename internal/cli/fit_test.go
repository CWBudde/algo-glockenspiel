package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
	"github.com/spf13/cobra"
)

func TestRunFitWritesArtifacts(t *testing.T) {
	dir := t.TempDir()
	referencePath := filepath.Join(dir, "reference.wav")
	outputPath := filepath.Join(dir, "fitted.json")
	workDir := filepath.Join(dir, "work")

	p, err := preset.Load(filepath.FromSlash("../../testdata/presets/minimal.json"))
	if err != nil {
		t.Fatalf("load preset: %v", err)
	}

	engine, err := synth.NewSynthesizer(p, 44100)
	if err != nil {
		t.Fatalf("new synthesizer: %v", err)
	}

	reference := engine.RenderNote(69, 100, 0.05)
	if err := wavio.WriteMono(referencePath, 44100, reference); err != nil {
		t.Fatalf("write reference wav: %v", err)
	}

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err = runFit(cmd, fitOptions{
		referencePath:   referencePath,
		presetPath:      filepath.FromSlash("../../testdata/presets/minimal.json"),
		outputPath:      outputPath,
		note:            69,
		velocity:        100,
		sampleRate:      44100,
		optimizerName:   "simple",
		maxIter:         1,
		timeBudget:      time.Second,
		reportEvery:     1,
		checkpointEvery: 1,
		workDir:         workDir,
	})
	if err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("expected fitted preset to exist: %v", err)
	}

	if _, err := os.Stat(filepath.Join(workDir, "fitted_output.wav")); err != nil {
		t.Fatalf("expected fitted output wav to exist: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(workDir, "checkpoint_*.json"))
	if err != nil {
		t.Fatalf("glob checkpoints: %v", err)
	}

	if len(matches) == 0 {
		t.Fatal("expected at least one checkpoint file")
	}
}

func TestRunFitCanDisableCheckpoints(t *testing.T) {
	dir := t.TempDir()
	referencePath := filepath.Join(dir, "reference.wav")
	outputPath := filepath.Join(dir, "fitted.json")
	workDir := filepath.Join(dir, "work")

	p, err := preset.Load(filepath.FromSlash("../../testdata/presets/minimal.json"))
	if err != nil {
		t.Fatalf("load preset: %v", err)
	}

	engine, err := synth.NewSynthesizer(p, 44100)
	if err != nil {
		t.Fatalf("new synthesizer: %v", err)
	}

	reference := engine.RenderNote(69, 100, 0.05)
	if err := wavio.WriteMono(referencePath, 44100, reference); err != nil {
		t.Fatalf("write reference wav: %v", err)
	}

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err = runFit(cmd, fitOptions{
		referencePath:   referencePath,
		presetPath:      filepath.FromSlash("../../testdata/presets/minimal.json"),
		outputPath:      outputPath,
		note:            69,
		velocity:        100,
		sampleRate:      44100,
		optimizerName:   "simple",
		maxIter:         1,
		timeBudget:      time.Second,
		reportEvery:     1,
		checkpointEvery: 0,
		workDir:         workDir,
	})
	if err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(workDir, "checkpoint_*.json"))
	if err != nil {
		t.Fatalf("glob checkpoints: %v", err)
	}

	if len(matches) != 0 {
		t.Fatalf("expected no checkpoint files when disabled, got %d", len(matches))
	}
}

func TestRunFitWritesCPUProfile(t *testing.T) {
	dir := t.TempDir()
	referencePath := filepath.Join(dir, "reference.wav")
	outputPath := filepath.Join(dir, "fitted.json")
	workDir := filepath.Join(dir, "work")
	profilePath := filepath.Join(dir, "fit.cpu")

	p, err := preset.Load(filepath.FromSlash("../../testdata/presets/minimal.json"))
	if err != nil {
		t.Fatalf("load preset: %v", err)
	}

	engine, err := synth.NewSynthesizer(p, 44100)
	if err != nil {
		t.Fatalf("new synthesizer: %v", err)
	}

	reference := engine.RenderNote(69, 100, 0.05)
	if err := wavio.WriteMono(referencePath, 44100, reference); err != nil {
		t.Fatalf("write reference wav: %v", err)
	}

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err = runFit(cmd, fitOptions{
		referencePath:   referencePath,
		presetPath:      filepath.FromSlash("../../testdata/presets/minimal.json"),
		outputPath:      outputPath,
		note:            69,
		velocity:        100,
		sampleRate:      44100,
		optimizerName:   "simple",
		maxIter:         1,
		timeBudget:      time.Second,
		reportEvery:     1,
		checkpointEvery: 1,
		workDir:         workDir,
		cpuProfilePath:  profilePath,
	})
	if err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	stat, err := os.Stat(profilePath)
	if err != nil {
		t.Fatalf("expected cpu profile to exist: %v", err)
	}

	if stat.Size() == 0 {
		t.Fatal("expected cpu profile to be non-empty")
	}
}

func TestRunFitResumesFromCheckpoint(t *testing.T) {
	dir := t.TempDir()
	referencePath := filepath.Join(dir, "reference.wav")
	outputPath := filepath.Join(dir, "fitted.json")
	workDir := filepath.Join(dir, "work")

	p, err := preset.Load(filepath.FromSlash("../../testdata/presets/minimal.json"))
	if err != nil {
		t.Fatalf("load preset: %v", err)
	}

	engine, err := synth.NewSynthesizer(p, 44100)
	if err != nil {
		t.Fatalf("new synthesizer: %v", err)
	}

	reference := engine.RenderNote(69, 100, 0.05)
	if err := wavio.WriteMono(referencePath, 44100, reference); err != nil {
		t.Fatalf("write reference wav: %v", err)
	}

	objective, err := optimizer.NewObjectiveFunction(reference, p, 44100, 69, 100, optimizer.MetricRMS)
	if err != nil {
		t.Fatalf("NewObjectiveFunction failed: %v", err)
	}

	encoded, err := objective.Codec().EncodeParams(&p.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	if err := optimizer.SaveCheckpoint(filepath.Join(workDir, "checkpoint_0007.json"), &optimizer.Checkpoint{
		Version:             optimizer.CheckpointVersion,
		Iteration:           7,
		OptimizerIterations: 7,
		BestCost:            0.123,
		BestParams:          encoded,
		Optimizer:           "simple",
		Metric:              "rms",
		State: &optimizer.OptimizerState{
			Kind: "simple",
		},
	}); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	cmd := &cobra.Command{}

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	err = runFit(cmd, fitOptions{
		referencePath:   referencePath,
		presetPath:      filepath.FromSlash("../../testdata/presets/minimal.json"),
		outputPath:      outputPath,
		note:            69,
		velocity:        100,
		sampleRate:      44100,
		optimizerName:   "simple",
		maxIter:         1,
		timeBudget:      time.Second,
		reportEvery:     1,
		checkpointEvery: 1,
		workDir:         workDir,
		resume:          true,
	})
	if err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	if !strings.Contains(out.String(), "Resuming from") {
		t.Fatalf("expected resume output, got %q", out.String())
	}
}

func TestRunFitResumeRestoresMayflySettingsFromCheckpoint(t *testing.T) {
	dir := t.TempDir()
	referencePath := filepath.Join(dir, "reference.wav")
	outputPath := filepath.Join(dir, "fitted.json")
	workDir := filepath.Join(dir, "work")

	p, err := preset.Load(filepath.FromSlash("../../testdata/presets/minimal.json"))
	if err != nil {
		t.Fatalf("load preset: %v", err)
	}

	engine, err := synth.NewSynthesizer(p, 44100)
	if err != nil {
		t.Fatalf("new synthesizer: %v", err)
	}

	reference := engine.RenderNote(69, 100, 0.05)
	if err := wavio.WriteMono(referencePath, 44100, reference); err != nil {
		t.Fatalf("write reference wav: %v", err)
	}

	objective, err := optimizer.NewObjectiveFunction(reference, p, 44100, 69, 100, optimizer.MetricRMS)
	if err != nil {
		t.Fatalf("NewObjectiveFunction failed: %v", err)
	}

	encoded, err := objective.Codec().EncodeParams(&p.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	if err := optimizer.SaveCheckpoint(filepath.Join(workDir, "checkpoint_0007.json"), &optimizer.Checkpoint{
		Version:             optimizer.CheckpointVersion,
		Iteration:           7,
		OptimizerIterations: 7,
		BestCost:            0.123,
		BestParams:          encoded,
		Optimizer:           "mayfly",
		Metric:              "spectral",
		State: &optimizer.OptimizerState{
			Kind: "mayfly",
			Mayfly: &optimizer.MayflyCheckpointEnv{
				Variant:    "desma",
				Population: 6,
				Seed:       7,
			},
		},
	}); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("optimizer", "", "")
	cmd.Flags().String("metric", "", "")
	cmd.Flags().String("mayfly-variant", "", "")
	cmd.Flags().Int("mayfly-pop", 0, "")
	cmd.Flags().Int64("mayfly-seed", 0, "")

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	err = runFit(cmd, fitOptions{
		referencePath: referencePath,
		presetPath:    filepath.FromSlash("../../testdata/presets/minimal.json"),
		outputPath:    outputPath,
		note:          69,
		velocity:      100,
		sampleRate:    44100,
		optimizerName: "simple",
		maxIter:       8,
		timeBudget:    time.Second,
		reportEvery:   1,
		workDir:       workDir,
		resume:        true,
	})
	if err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "optimizer=mayfly") || !strings.Contains(text, "metric=spectral") {
		t.Fatalf("expected resume output to restore optimizer/metric, got %q", text)
	}

	if !strings.Contains(text, "remaining-iter=1") {
		t.Fatalf("expected remaining iterations to account for checkpoint, got %q", text)
	}
}

func TestRunFitRejectsInvalidMayflyPopulation(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := runFit(cmd, fitOptions{
		referencePath: "dummy.wav",
		outputPath:    "dummy.json",
		note:          69,
		velocity:      100,
		sampleRate:    44100,
		optimizerName: "mayfly",
		maxIter:       1,
		timeBudget:    time.Second,
		reportEvery:   1,
		workDir:       t.TempDir(),
		mayflyPop:     1,
	})
	if err == nil {
		t.Fatal("expected invalid mayfly population to fail")
	}
}

func TestRunFitRejectsInvalidMetric(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := runFit(cmd, fitOptions{
		referencePath: "dummy.wav",
		outputPath:    "dummy.json",
		note:          69,
		velocity:      100,
		sampleRate:    44100,
		optimizerName: "simple",
		maxIter:       1,
		timeBudget:    time.Second,
		reportEvery:   1,
		workDir:       t.TempDir(),
		metric:        "bad",
	})
	if err == nil {
		t.Fatal("expected invalid metric to fail")
	}
}

func TestShouldCheckpoint(t *testing.T) {
	tests := []struct {
		name            string
		iteration       int
		checkpointEvery int
		want            bool
	}{
		{name: "every report", iteration: 3, checkpointEvery: 1, want: true},
		{name: "on cadence", iteration: 4, checkpointEvery: 2, want: true},
		{name: "off cadence", iteration: 5, checkpointEvery: 2, want: false},
		{name: "disabled", iteration: 4, checkpointEvery: 0, want: false},
		{name: "negative interval", iteration: 4, checkpointEvery: -1, want: false},
		{name: "zero iteration", iteration: 0, checkpointEvery: 1, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldCheckpoint(tc.iteration, tc.checkpointEvery); got != tc.want {
				t.Fatalf("shouldCheckpoint(%d, %d) = %v, want %v", tc.iteration, tc.checkpointEvery, got, tc.want)
			}
		})
	}
}

func TestDurationFlagSet(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{name: "go duration", raw: "10m", want: 10 * time.Minute},
		{name: "seconds suffix", raw: "30s", want: 30 * time.Second},
		{name: "bare integer stays seconds", raw: "30", want: 30 * time.Second},
		{name: "bare float stays seconds", raw: "1.5", want: 1500 * time.Millisecond},
		{name: "garbage", raw: "soon", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var budget time.Duration

			err := durationFlag{value: &budget}.Set(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected %q to fail", tc.raw)
				}

				return
			}

			if err != nil {
				t.Fatalf("Set(%q) failed: %v", tc.raw, err)
			}

			if budget != tc.want {
				t.Fatalf("Set(%q) = %s, want %s", tc.raw, budget, tc.want)
			}
		})
	}
}

func TestFitCmdFlags(t *testing.T) {
	cmd := newFitCmd()

	if got := cmd.Flags().Lookup("preset").DefValue; got != "" {
		t.Fatalf("expected empty preset default so the embedded preset is used, got %q", got)
	}

	if got := cmd.Flags().Lookup("time-budget").DefValue; got != "30s" {
		t.Fatalf("unexpected time-budget default: %q", got)
	}

	if got := cmd.Flags().Lookup("optimizer").Usage; !strings.Contains(got, "mayfly") {
		t.Fatalf("expected optimizer usage to list mayfly, got %q", got)
	}

	if cmd.Flags().Lookup("bounds") == nil {
		t.Fatal("expected a --bounds flag")
	}

	if cmd.Example == "" {
		t.Fatal("expected fit to document examples")
	}

	if cmd.Args == nil {
		t.Fatal("expected fit to reject stray positional arguments")
	}
}

// writeFitReference renders the minimal preset and returns its WAV path.
func writeFitReference(t *testing.T, dir string) (string, *preset.Preset, []float32) {
	t.Helper()

	p, err := preset.Load(filepath.FromSlash("../../testdata/presets/minimal.json"))
	if err != nil {
		t.Fatalf("load preset: %v", err)
	}

	engine, err := synth.NewSynthesizer(p, 44100)
	if err != nil {
		t.Fatalf("new synthesizer: %v", err)
	}

	reference := engine.RenderNote(69, 100, 0.05)

	referencePath := filepath.Join(dir, "reference.wav")
	if err := wavio.WriteMono(referencePath, 44100, reference); err != nil {
		t.Fatalf("write reference wav: %v", err)
	}

	return referencePath, p, reference
}

// baseFitOptions returns options for a one-iteration fit run.
func baseFitOptions(referencePath, outputPath, workDir string) fitOptions {
	return fitOptions{
		referencePath:   referencePath,
		presetPath:      filepath.FromSlash("../../testdata/presets/minimal.json"),
		outputPath:      outputPath,
		note:            69,
		velocity:        100,
		sampleRate:      44100,
		optimizerName:   "simple",
		maxIter:         1,
		timeBudget:      time.Second,
		reportEvery:     1,
		checkpointEvery: 1,
		workDir:         workDir,
	}
}

func TestRunFitHonorsNarrowedBounds(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)
	outputPath := filepath.Join(dir, "fitted.json")
	boundsPath := filepath.Join(dir, "bounds.json")

	if err := os.WriteFile(boundsPath, []byte(`{"base_frequency": [430.0, 450.0]}`), 0o600); err != nil {
		t.Fatalf("write bounds: %v", err)
	}

	options := baseFitOptions(referencePath, outputPath, filepath.Join(dir, "work"))
	options.boundsPath = boundsPath

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit with bounds failed: %v", err)
	}

	fitted, err := preset.Load(outputPath)
	if err != nil {
		t.Fatalf("load fitted preset: %v", err)
	}

	if fitted.Parameters.BaseFrequency < 430 || fitted.Parameters.BaseFrequency > 450 {
		t.Fatalf("expected base frequency inside the narrowed bound, got %g", fitted.Parameters.BaseFrequency)
	}
}

func TestRunFitRejectsUnreadableBounds(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)

	options := baseFitOptions(referencePath, filepath.Join(dir, "fitted.json"), filepath.Join(dir, "work"))
	options.boundsPath = filepath.Join(dir, "absent.json")

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := runFit(cmd, options); err == nil {
		t.Fatal("expected missing bounds file to fail the run")
	}
}

func TestRunFitResumeRejectsMismatchedCheckpoint(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)
	workDir := filepath.Join(dir, "work")

	if err := optimizer.SaveCheckpoint(filepath.Join(workDir, "checkpoint_0007.json"), &optimizer.Checkpoint{
		Version:             optimizer.CheckpointVersion,
		Iteration:           7,
		OptimizerIterations: 7,
		BestCost:            0.5,
		BestParams:          []float64{0.1, 0.2},
		Optimizer:           "simple",
		Metric:              "rms",
	}); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	options := baseFitOptions(referencePath, filepath.Join(dir, "fitted.json"), workDir)
	options.resume = true

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := runFit(cmd, options)
	if err == nil {
		t.Fatal("expected a mismatched checkpoint to fail the resume")
	}

	if !strings.Contains(err.Error(), "parameters") {
		t.Fatalf("expected a parameter-count error, got %v", err)
	}
}

func TestRunFitResumeWarnsWhenNoCheckpointExists(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)

	options := baseFitOptions(referencePath, filepath.Join(dir, "fitted.json"), filepath.Join(dir, "work"))
	options.resume = true

	cmd := &cobra.Command{}

	var errOut bytes.Buffer

	cmd.SetOut(io.Discard)
	cmd.SetErr(&errOut)

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	if !strings.Contains(errOut.String(), "found no checkpoint") {
		t.Fatalf("expected a warning about the missing checkpoint, got %q", errOut.String())
	}
}

func TestRunFitResumeWarnsWhenBudgetIsExhausted(t *testing.T) {
	dir := t.TempDir()
	referencePath, p, reference := writeFitReference(t, dir)
	workDir := filepath.Join(dir, "work")

	objective, err := optimizer.NewObjectiveFunction(reference, p, 44100, 69, 100, optimizer.MetricRMS)
	if err != nil {
		t.Fatalf("NewObjectiveFunction failed: %v", err)
	}

	encoded, err := objective.Codec().EncodeParams(&p.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	if err := optimizer.SaveCheckpoint(filepath.Join(workDir, "checkpoint_0020.json"), &optimizer.Checkpoint{
		Version:             optimizer.CheckpointVersion,
		Iteration:           20,
		OptimizerIterations: 20,
		BestCost:            0.5,
		BestParams:          encoded,
		Optimizer:           "simple",
		Metric:              "rms",
	}); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	options := baseFitOptions(referencePath, filepath.Join(dir, "fitted.json"), workDir)
	options.resume = true
	options.maxIter = 5

	cmd := &cobra.Command{}

	var out, errOut bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	if !strings.Contains(errOut.String(), "exhausts --max-iter") {
		t.Fatalf("expected a warning about the exhausted budget, got %q", errOut.String())
	}

	if !strings.Contains(out.String(), "remaining-iter=1") {
		t.Fatalf("expected the resumed budget to stay at one iteration, got %q", out.String())
	}
}

func TestRunFitResumeDoesNotWriteCheckpointsWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	referencePath, p, reference := writeFitReference(t, dir)
	workDir := filepath.Join(dir, "work")

	objective, err := optimizer.NewObjectiveFunction(reference, p, 44100, 69, 100, optimizer.MetricRMS)
	if err != nil {
		t.Fatalf("NewObjectiveFunction failed: %v", err)
	}

	encoded, err := objective.Codec().EncodeParams(&p.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	existing := filepath.Join(workDir, "checkpoint_0007.json")
	if err := optimizer.SaveCheckpoint(existing, &optimizer.Checkpoint{
		Version:             optimizer.CheckpointVersion,
		Iteration:           7,
		OptimizerIterations: 7,
		BestCost:            0.5,
		BestParams:          encoded,
		Optimizer:           "simple",
		Metric:              "rms",
	}); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	options := baseFitOptions(referencePath, filepath.Join(dir, "fitted.json"), workDir)
	options.resume = true
	options.checkpointEvery = 0
	options.maxIter = 8

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

	if len(matches) != 1 || matches[0] != existing {
		t.Fatalf("expected --checkpoint-interval 0 to write no checkpoint, got %v", matches)
	}
}

func TestRunFitKeepsExplicitBoundsAsHardConstraint(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)
	outputPath := filepath.Join(dir, "fitted.json")
	boundsPath := filepath.Join(dir, "bounds.json")

	// The starting preset's first mode has amplitude 1.0, well outside this
	// range. The box used to be widened to contain it, which let the fitted
	// preset violate the amplitude the caller asked for.
	if err := os.WriteFile(boundsPath, []byte(`{"amplitude": [-0.5, 0.5]}`), 0o600); err != nil {
		t.Fatalf("write bounds: %v", err)
	}

	options := baseFitOptions(referencePath, outputPath, filepath.Join(dir, "work"))
	options.boundsPath = boundsPath

	cmd := &cobra.Command{}

	var errOut bytes.Buffer

	cmd.SetOut(io.Discard)
	cmd.SetErr(&errOut)

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit with strict bounds failed: %v", err)
	}

	if !strings.Contains(errOut.String(), "clamped") {
		t.Fatalf("expected a warning that the starting preset was clamped, got %q", errOut.String())
	}

	fitted, err := preset.Load(outputPath)
	if err != nil {
		t.Fatalf("load fitted preset: %v", err)
	}

	for i, mode := range fitted.Parameters.Modes {
		if mode.Amplitude < -0.5 || mode.Amplitude > 0.5 {
			t.Fatalf("mode %d amplitude %g escaped the requested bounds", i, mode.Amplitude)
		}
	}
}

func TestRunFitResumeRestoresMetricBeforeBuildingObjective(t *testing.T) {
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")

	p, err := preset.Load(filepath.FromSlash("../../testdata/presets/minimal.json"))
	if err != nil {
		t.Fatalf("load preset: %v", err)
	}

	engine, err := synth.NewSynthesizer(p, 44100)
	if err != nil {
		t.Fatalf("new synthesizer: %v", err)
	}

	// Too short for the spectral metric, which needs a full analysis frame.
	reference := engine.RenderNote(69, 100, 0.01)

	referencePath := filepath.Join(dir, "reference.wav")
	if err := wavio.WriteMono(referencePath, 44100, reference); err != nil {
		t.Fatalf("write reference wav: %v", err)
	}

	objective, err := optimizer.NewObjectiveFunction(reference, p, 44100, 69, 100, optimizer.MetricRMS)
	if err != nil {
		t.Fatalf("NewObjectiveFunction failed: %v", err)
	}

	encoded, err := objective.Codec().EncodeParams(&p.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	if err := optimizer.SaveCheckpoint(filepath.Join(workDir, "checkpoint_0007.json"), &optimizer.Checkpoint{
		Version:             optimizer.CheckpointVersion,
		Iteration:           7,
		OptimizerIterations: 7,
		BestCost:            0.123,
		BestParams:          encoded,
		Optimizer:           "simple",
		Metric:              "spectral",
	}); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	options := baseFitOptions(referencePath, filepath.Join(dir, "fitted.json"), workDir)
	options.maxIter = 8
	options.resume = true

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	// The checkpoint's metric has to be restored before the objective is built,
	// otherwise the run silently optimizes RMS while reporting "spectral" and
	// skips the spectral input validation entirely.
	err = runFit(cmd, options)
	if err == nil {
		t.Fatal("expected the restored spectral metric to reject the short reference")
	}

	if !strings.Contains(err.Error(), "spectral metric needs at least") {
		t.Fatalf("expected a spectral input error, got %v", err)
	}
}

func TestRunFitCheckpointRecordsOptimizerIterations(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)
	workDir := filepath.Join(dir, "work")

	options := baseFitOptions(referencePath, filepath.Join(dir, "fitted.json"), workDir)
	options.maxIter = 20
	// One progress report per five optimizer iterations: the report count and
	// the iteration count must not be confused for one another.
	options.reportEvery = 5

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	latest, err := optimizer.FindLatestCheckpoint(workDir)
	if err != nil {
		t.Fatalf("FindLatestCheckpoint failed: %v", err)
	}

	cp, err := optimizer.LoadCheckpoint(latest)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}

	if cp.OptimizerIterations <= 0 {
		t.Fatalf("expected the checkpoint to record optimizer iterations, got %d", cp.OptimizerIterations)
	}
}

func TestRunFitResumeIgnoresReportCountForBudget(t *testing.T) {
	dir := t.TempDir()
	referencePath, p, reference := writeFitReference(t, dir)
	workDir := filepath.Join(dir, "work")

	objective, err := optimizer.NewObjectiveFunction(reference, p, 44100, 69, 100, optimizer.MetricRMS)
	if err != nil {
		t.Fatalf("NewObjectiveFunction failed: %v", err)
	}

	encoded, err := objective.Codec().EncodeParams(&p.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	// A checkpoint written after 100 optimizer iterations with --report-every 10
	// carries Iteration=10. Charging that against --max-iter would hand the
	// resumed run 90 more iterations on an exhausted budget.
	if err := optimizer.SaveCheckpoint(filepath.Join(workDir, "checkpoint_0010.json"), &optimizer.Checkpoint{
		Version:             optimizer.CheckpointVersion,
		Iteration:           10,
		OptimizerIterations: 100,
		BestCost:            0.5,
		BestParams:          encoded,
		Optimizer:           "simple",
		Metric:              "rms",
	}); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	options := baseFitOptions(referencePath, filepath.Join(dir, "fitted.json"), workDir)
	options.resume = true
	options.maxIter = 100

	cmd := &cobra.Command{}

	var out, errOut bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	if !strings.Contains(errOut.String(), "exhausts --max-iter") {
		t.Fatalf("expected the exhausted budget warning, got %q", errOut.String())
	}

	if !strings.Contains(out.String(), "remaining-iter=1") {
		t.Fatalf("expected the resumed budget to stay at one iteration, got %q", out.String())
	}
}
