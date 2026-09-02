package cli

import (
	"bytes"
	"fmt"
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
		name                string
		optimizerIterations int
		lastCheckpointed    int
		checkpointEvery     int
		want                bool
	}{
		{name: "every iteration", optimizerIterations: 3, lastCheckpointed: 2, checkpointEvery: 1, want: true},
		{name: "interval reached", optimizerIterations: 4, lastCheckpointed: 2, checkpointEvery: 2, want: true},
		{name: "interval overshot", optimizerIterations: 9, lastCheckpointed: 2, checkpointEvery: 5, want: true},
		{name: "interval not reached", optimizerIterations: 5, lastCheckpointed: 4, checkpointEvery: 2, want: false},
		{name: "disabled", optimizerIterations: 4, checkpointEvery: 0, want: false},
		{name: "negative interval", optimizerIterations: 4, checkpointEvery: -1, want: false},
		{name: "backend reports no iteration count", optimizerIterations: 0, checkpointEvery: 1, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldCheckpoint(tc.optimizerIterations, tc.lastCheckpointed, tc.checkpointEvery)
			if got != tc.want {
				t.Fatalf("shouldCheckpoint(%d, %d, %d) = %v, want %v",
					tc.optimizerIterations, tc.lastCheckpointed, tc.checkpointEvery, got, tc.want)
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

	if got := cmd.Flags().Lookup("mayfly-variant").Usage; !strings.Contains(got, "desma") {
		t.Fatalf("expected mayfly-variant usage to name the dialects, got %q", got)
	}

	for name, want := range map[string]string{
		"mayfly-tuning":     "",
		"mayfly-preset":     "",
		"mayfly-epochs":     "1",
		"mayfly-restarts":   "0",
		"mayfly-stagnation": "0",
		// Three-way flags: their sentinel defaults are legal values, so
		// "not given" is read off Changed rather than off the value.
		"mayfly-target-cost": "0",
		"mayfly-nc":          "-1",
		"mayfly-nc-ratio":    "0",
		"mayfly-selection":   "",
	} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Fatalf("expected a --%s flag", name)
		}

		if flag.DefValue != want {
			t.Errorf("unexpected --%s default: got %q want %q", name, flag.DefValue, want)
		}

		if flag.Usage == "" {
			t.Errorf("expected --%s to document itself", name)
		}
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

	if err := os.WriteFile(boundsPath, []byte(`{"filter_freq": [400.0, 600.0]}`), 0o600); err != nil {
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

	if fitted.Parameters.FilterFrequency < 400-1e-6 || fitted.Parameters.FilterFrequency > 600+1e-6 {
		t.Fatalf("expected filter frequency inside the narrowed bound, got %g", fitted.Parameters.FilterFrequency)
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

// writeTuningFile writes a tuning document and returns its path.
func writeTuningFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write tuning file: %v", err)
	}

	return path
}

// mayflyFitOptions returns options for a very short Mayfly fit.
func mayflyFitOptions(referencePath, outputPath, workDir string) fitOptions {
	options := baseFitOptions(referencePath, outputPath, workDir)
	options.optimizerName = "mayfly"
	options.mayflyPop = 2
	options.seed = 3
	options.maxIter = 2

	return options
}

// latestCheckpointState returns the Mayfly environment of the newest checkpoint.
func latestCheckpointState(t *testing.T, workDir string) *optimizer.MayflyCheckpointEnv {
	t.Helper()

	path, err := optimizer.FindLatestCheckpoint(workDir)
	if err != nil {
		t.Fatalf("FindLatestCheckpoint failed: %v", err)
	}

	cp, err := optimizer.LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}

	if cp.State == nil || cp.State.Mayfly == nil {
		t.Fatalf("expected checkpoint %s to carry mayfly state", path)
	}

	return cp.State.Mayfly
}

func TestRunFitAppliesMayflyTuningDocument(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)
	workDir := filepath.Join(dir, "work")

	tuningPath := writeTuningFile(t, dir, "tuning.json",
		`{"selection": "rank", "convergence": {"stagnation_iterations": 2}, "schedule": {"epochs": 2}}`)

	cmd := &cobra.Command{}

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	options := mayflyFitOptions(referencePath, filepath.Join(dir, "fitted.json"), workDir)
	options.mayflyTuningPath = tuningPath
	// The budget has to leave a round long enough for the stagnation window,
	// which the optimizer checks against the shortest round rather than the
	// total.
	options.maxIter = 6

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	// Two epochs over a six-iteration budget is two rounds of three, and the
	// resolve line is where a caller can see that the document was read.
	if text := out.String(); !strings.Contains(text, "rounds=2x3") {
		t.Fatalf("expected the schedule from the tuning document in the output, got %q", text)
	}

	state := latestCheckpointState(t, workDir)
	if state.Tuning == nil || state.Tuning.Selection == nil || *state.Tuning.Selection != "rank" {
		t.Fatalf("expected the checkpoint to carry the tuning document, got %#v", state.Tuning)
	}
}

// TestRunFitTuningDocumentChoosesTheDialect covers a document that describes a
// whole run rather than only its knobs. --mayfly-variant always carries a
// default, and the engine prefers its Variant field over the document and
// refuses a dialect named twice -- so passing that unwritten default on ran
// desma while the file said gsasma, and turned a document naming a preset into
// a variant/preset conflict.
func TestRunFitTuningDocumentChoosesTheDialect(t *testing.T) {
	for _, row := range []struct {
		name     string
		document string
		expect   string
	}{
		{"variant", `{"variant": "gsasma"}`, "variant=gsasma"},
		{"preset", `{"preset": "highly_multimodal"}`, "preset=highly_multimodal"},
	} {
		t.Run(row.name, func(t *testing.T) {
			dir := t.TempDir()
			referencePath, _, _ := writeFitReference(t, dir)

			tuningPath := writeTuningFile(t, dir, "tuning.json", row.document)

			cmd := &cobra.Command{}

			var out bytes.Buffer

			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)

			// The flag default stands, as it does for any caller who did not
			// write --mayfly-variant.
			options := mayflyFitOptions(referencePath, filepath.Join(dir, "fitted.json"), filepath.Join(dir, "work"))
			options.mayflyVariant = "desma"
			options.mayflyTuningPath = tuningPath

			if err := runFit(cmd, options); err != nil {
				t.Fatalf("runFit failed: %v", err)
			}

			if text := out.String(); !strings.Contains(text, row.expect) {
				t.Fatalf("expected the document to choose the dialect (%s), got %q", row.expect, text)
			}
		})
	}
}

func TestRunFitTuningDocumentWinsOverFlags(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)
	workDir := filepath.Join(dir, "work")

	tuningPath := writeTuningFile(t, dir, "tuning.json", `{"selection": "rank"}`)

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	options := mayflyFitOptions(referencePath, filepath.Join(dir, "fitted.json"), workDir)
	options.mayflyTuningPath = tuningPath
	options.mayflySelection = "tournament"

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	state := latestCheckpointState(t, workDir)
	if state.Tuning == nil || state.Tuning.Selection == nil {
		t.Fatalf("expected the checkpoint to carry a merged tuning document, got %#v", state.Tuning)
	}

	if *state.Tuning.Selection != "rank" {
		t.Fatalf("expected the document to win over the flag, got selection %q", *state.Tuning.Selection)
	}
}

func TestRunFitRejectsMalformedTuningDocument(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)

	tuningPath := writeTuningFile(t, dir, "broken.json", `{"npop": `)

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	options := mayflyFitOptions(referencePath, filepath.Join(dir, "fitted.json"), filepath.Join(dir, "work"))
	options.mayflyTuningPath = tuningPath

	err := runFit(cmd, options)
	if err == nil {
		t.Fatal("expected a malformed tuning document to fail")
	}

	if !strings.Contains(err.Error(), tuningPath) {
		t.Fatalf("error should name the tuning file, got %q", err)
	}
}

func TestRunFitRejectsUnknownTuningKey(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)

	tuningPath := writeTuningFile(t, dir, "unknown.json", `{"npopp": 12}`)

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	options := mayflyFitOptions(referencePath, filepath.Join(dir, "fitted.json"), filepath.Join(dir, "work"))
	options.mayflyTuningPath = tuningPath

	err := runFit(cmd, options)
	if err == nil {
		t.Fatal("expected an unknown tuning key to fail")
	}

	// A misspelled knob that was silently ignored would run at the factory
	// default while the caller believed it had tuned something, so the message
	// has to name the key.
	if !strings.Contains(err.Error(), "npopp") {
		t.Fatalf("error should name the offending key, got %q", err)
	}
}

func TestRunFitRejectsNonPositiveMayflyEpochs(t *testing.T) {
	cmd := newFitCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Flags().Set("mayfly-epochs", "0"); err != nil {
		t.Fatalf("set mayfly-epochs: %v", err)
	}

	options := baseFitOptions("dummy.wav", "dummy.json", t.TempDir())
	options.optimizerName = "mayfly"
	options.mayflyPop = 4
	options.mayflyEpochs = 0

	err := runFit(cmd, options)
	if err == nil {
		t.Fatal("expected mayfly-epochs 0 to fail")
	}

	if !strings.Contains(err.Error(), "mayfly-epochs") {
		t.Fatalf("error should name the flag, got %q", err)
	}
}

func TestRunFitRejectsPresetWithVariant(t *testing.T) {
	cmd := newFitCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Flags().Set("mayfly-variant", "olce"); err != nil {
		t.Fatalf("set mayfly-variant: %v", err)
	}

	options := baseFitOptions("dummy.wav", "dummy.json", t.TempDir())
	options.optimizerName = "mayfly"
	options.mayflyPop = 4
	options.mayflyVariant = "olce"
	options.mayflyPreset = "balanced"

	err := runFit(cmd, options)
	if err == nil {
		t.Fatal("expected a preset combined with a variant to fail")
	}

	if !strings.Contains(err.Error(), "mayfly-preset") {
		t.Fatalf("error should name the flags, got %q", err)
	}
}

func TestRunFitResumeRestoresEffectiveMayflySeed(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)
	outputPath := filepath.Join(dir, "fitted.json")
	workDir := filepath.Join(dir, "work")

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	options := mayflyFitOptions(referencePath, outputPath, workDir)
	// Zero means "pick one and report it", so the resolved value is the only
	// record of the stream the run used.
	options.seed = 0

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	effective := latestCheckpointState(t, workDir).Seed
	if effective == 0 {
		t.Fatal("expected the checkpoint to record a resolved, non-zero seed")
	}

	resume := newFitCmd()

	var out bytes.Buffer

	resume.SetOut(&out)
	resume.SetErr(io.Discard)

	resumed := mayflyFitOptions(referencePath, outputPath, workDir)
	resumed.maxIter = 4
	resumed.resume = true
	resumed.seed = 0

	if err := runFit(resume, resumed); err != nil {
		t.Fatalf("resumed runFit failed: %v", err)
	}

	if want := fmt.Sprintf("seed=%d", effective); !strings.Contains(out.String(), want) {
		t.Fatalf("expected the resumed run to continue %s, got %q", want, out.String())
	}
}

// TestRunFitSeedsTheModesFromTheReference pins Phase 8.3's starting point:
// the modes come from the reference's partials, not from the template, the
// checkpoint records that choice so a resume makes it again, and the report
// says which dimensions ended on a bound.
func TestRunFitSeedsTheModesFromTheReference(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)
	workDir := filepath.Join(dir, "work")
	outputPath := filepath.Join(dir, "fitted.json")

	options := baseFitOptions(referencePath, outputPath, workDir)

	cmd := &cobra.Command{}

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)

	if err := runFit(cmd, options); err != nil {
		t.Fatalf("runFit failed: %v", err)
	}

	fitted, err := preset.Load(outputPath)
	if err != nil {
		t.Fatalf("load fitted preset: %v", err)
	}

	// The minimal preset sounds one mode, so the analysis lists one partial
	// and the fit searches one mode -- in a v2 preset, since v1 holds four.
	if len(fitted.Parameters.Modes) != 1 || fitted.Version != preset.VersionV2 {
		t.Fatalf("fitted preset has %d modes in version %q, want 1 in %q", len(fitted.Parameters.Modes), fitted.Version, preset.VersionV2)
	}

	for _, want := range []string{"modes: 1 seeded from the reference's partials", "pinned: "} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output lacks %q:\n%s", want, out.String())
		}
	}

	latest, err := optimizer.FindLatestCheckpoint(workDir)
	if err != nil {
		t.Fatalf("find checkpoint: %v", err)
	}

	cp, err := optimizer.LoadCheckpoint(latest)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}

	if cp.State == nil || cp.State.Modes != 1 {
		t.Fatalf("checkpoint state = %+v, want modes 1", cp.State)
	}

	// A resume from that checkpoint seeds the same single mode, so the
	// vector still fits.
	resumed := baseFitOptions(referencePath, filepath.Join(dir, "resumed.json"), workDir)
	resumed.resume = true

	out.Reset()

	if err := runFit(cmd, resumed); err != nil {
		t.Fatalf("resumed runFit failed: %v", err)
	}

	if !strings.Contains(out.String(), "modes: 1 seeded") {
		t.Fatalf("the resumed run did not seed the checkpoint's one mode:\n%s", out.String())
	}

	// Keeping the template's modes is a choice, and it keeps the version.
	kept := baseFitOptions(referencePath, filepath.Join(dir, "kept.json"), filepath.Join(dir, "work-kept"))
	kept.modes = optimizer.KeepTemplateModes

	out.Reset()

	if err := runFit(cmd, kept); err != nil {
		t.Fatalf("runFit with the template's modes failed: %v", err)
	}

	keptPreset, err := preset.Load(filepath.Join(dir, "kept.json"))
	if err != nil {
		t.Fatalf("load kept preset: %v", err)
	}

	if len(keptPreset.Parameters.Modes) != 4 || keptPreset.Version != preset.VersionV1 {
		t.Fatalf("kept preset has %d modes in version %q, want the template's 4 in %q", len(keptPreset.Parameters.Modes), keptPreset.Version, preset.VersionV1)
	}

	if !strings.Contains(out.String(), "modes: keeping the preset's 4") {
		t.Fatalf("output does not say the template's modes were kept:\n%s", out.String())
	}
}

// TestRunFitRefusesTheRetiredBoundsKeys pins the message a bounds file from
// before Phase 8.3 gets, which names the key and what became of it.
func TestRunFitRefusesTheRetiredBoundsKeys(t *testing.T) {
	dir := t.TempDir()
	referencePath, _, _ := writeFitReference(t, dir)
	boundsPath := filepath.Join(dir, "bounds.json")

	if err := os.WriteFile(boundsPath, []byte(`{"frequency_mult": [0.5, 10.0]}`), 0o600); err != nil {
		t.Fatalf("write bounds: %v", err)
	}

	options := baseFitOptions(referencePath, filepath.Join(dir, "fitted.json"), filepath.Join(dir, "work"))
	options.boundsPath = boundsPath

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := runFit(cmd, options)
	if err == nil || !strings.Contains(err.Error(), "frequency_mult was replaced by frequency") {
		t.Fatalf("expected the retired key to be refused by name, got %v", err)
	}
}
