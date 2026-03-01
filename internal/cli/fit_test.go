package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"

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
