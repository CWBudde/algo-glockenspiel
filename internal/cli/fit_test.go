package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/optimizer"
	"github.com/cwbudde/glockenspiel/internal/preset"
	"github.com/cwbudde/glockenspiel/internal/synth"
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
	if err := writeWAV(referencePath, 44100, reference); err != nil {
		t.Fatalf("write reference wav: %v", err)
	}

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err = runFit(cmd, fitOptions{
		referencePath: referencePath,
		presetPath:    filepath.FromSlash("../../testdata/presets/minimal.json"),
		outputPath:    outputPath,
		note:          69,
		velocity:      100,
		sampleRate:    44100,
		optimizerName: "simple",
		maxIter:       1,
		timeBudget:    1,
		reportEvery:   1,
		checkpointEvery: 1,
		workDir:       workDir,
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
	if err := writeWAV(referencePath, 44100, reference); err != nil {
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
		timeBudget:      1,
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
	if err := writeWAV(referencePath, 44100, reference); err != nil {
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
		Version:    "1.0",
		Iteration:  7,
		BestCost:   0.123,
		BestParams: encoded,
		Optimizer:  "simple",
		Metric:     "rms",
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
		timeBudget:      1,
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
		timeBudget:    1,
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
		timeBudget:    1,
		reportEvery:   1,
		workDir:       t.TempDir(),
		metric:        "bad",
	})
	if err == nil {
		t.Fatal("expected invalid metric to fail")
	}
}
