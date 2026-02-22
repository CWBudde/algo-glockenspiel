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
