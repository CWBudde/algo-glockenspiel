package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunSynthWritesWAV(t *testing.T) {
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "test.wav")

	options := synthOptions{
		presetPath: filepath.FromSlash("../../testdata/presets/minimal.json"),
		outputPath: outputPath,
		note:       69,
		velocity:   100,
		duration:   0.1,
		sampleRate: 44100,
		autoStop:   false,
		decayDBFS:  -80,
	}

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := runSynth(cmd, options); err != nil {
		t.Fatalf("runSynth failed: %v", err)
	}

	stat, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("expected output file to exist: %v", err)
	}

	if stat.Size() <= 44 {
		t.Fatalf("expected non-empty wav output, got size %d", stat.Size())
	}
}

func TestRunSynthUsesEmbeddedPresetByDefault(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "test.wav")

	options := synthOptions{
		outputPath: outputPath,
		note:       69,
		velocity:   100,
		duration:   0.05,
		sampleRate: 44100,
		decayDBFS:  -80,
	}

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := runSynth(cmd, options); err != nil {
		t.Fatalf("runSynth with embedded preset failed: %v", err)
	}

	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("expected output file to exist: %v", err)
	}
}

func TestSynthCmdDefaultsAreLocationIndependent(t *testing.T) {
	cmd := newSynthCmd()

	if got := cmd.Flags().Lookup("preset").DefValue; got != "" {
		t.Fatalf("expected empty preset default so the embedded preset is used, got %q", got)
	}

	if cmd.Example == "" {
		t.Fatal("expected synth to document examples")
	}

	if cmd.Args == nil {
		t.Fatal("expected synth to reject stray positional arguments")
	}
}
