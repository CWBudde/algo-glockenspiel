package cli

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/cwbudde/glockenspiel/internal/preset"
	"github.com/cwbudde/glockenspiel/internal/synth"
	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/spf13/cobra"
)

type synthOptions struct {
	presetPath string
	outputPath string
	note       int
	velocity   int
	duration   float64
	sampleRate int
	autoStop   bool
	decayDBFS  float64
}

func newSynthCmd() *cobra.Command {
	options := synthOptions{
		presetPath: filepath.FromSlash("assets/presets/default.json"),
		outputPath: "output.wav",
		note:       69,
		velocity:   100,
		duration:   2.0,
		sampleRate: 44100,
		autoStop:   false,
		decayDBFS:  -90,
	}

	cmd := &cobra.Command{
		Use:   "synth",
		Short: "Synthesize audio from a preset",
		Long:  "Generate a synthesized glockenspiel note and write it as a mono WAV file.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSynth(cmd, options)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&options.presetPath, "preset", options.presetPath, "Path to preset JSON file")
	flags.StringVar(&options.outputPath, "output", options.outputPath, "Path to output WAV file")
	flags.IntVar(&options.note, "note", options.note, "MIDI note number to render")
	flags.IntVar(&options.velocity, "velocity", options.velocity, "MIDI velocity (0-127)")
	flags.Float64Var(&options.duration, "duration", options.duration, "Render duration in seconds")
	flags.IntVar(&options.sampleRate, "sample-rate", options.sampleRate, "Output sample rate in Hz")
	flags.BoolVar(&options.autoStop, "auto-stop", options.autoStop, "Stop early when RMS falls below threshold")
	flags.Float64Var(&options.decayDBFS, "decay-dbfs", options.decayDBFS, "Auto-stop threshold in dBFS (negative)")

	return cmd
}

func runSynth(cmd *cobra.Command, options synthOptions) error {
	if options.velocity < 0 || options.velocity > 127 {
		return fmt.Errorf("velocity must be in [0,127], got %d", options.velocity)
	}

	if options.note < 0 || options.note > 127 {
		return fmt.Errorf("note must be in [0,127], got %d", options.note)
	}

	if options.duration <= 0 {
		return fmt.Errorf("duration must be positive, got %f", options.duration)
	}

	if options.sampleRate <= 0 {
		return fmt.Errorf("sample-rate must be positive, got %d", options.sampleRate)
	}

	loadedPreset, err := preset.Load(options.presetPath)
	if err != nil {
		return err
	}

	engine, err := synth.NewSynthesizer(loadedPreset, options.sampleRate)
	if err != nil {
		return err
	}

	samples := engine.RenderNoteWithOptions(options.note, options.velocity, options.duration, synth.RenderOptions{
		AutoStop:  options.autoStop,
		DecayDBFS: options.decayDBFS,
	})
	if len(samples) == 0 {
		return fmt.Errorf("render produced no samples")
	}

	if err := writeWAV(options.outputPath, options.sampleRate, samples); err != nil {
		return err
	}

	stat, err := os.Stat(options.outputPath)
	if err != nil {
		return err
	}

	renderedDuration := float64(len(samples)) / float64(options.sampleRate)

	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"Rendered %.3fs (%d samples) to %s (%d bytes)\n",
		renderedDuration, len(samples), options.outputPath, stat.Size())

	return nil
}

func writeWAV(path string, sampleRate int, samples []float32) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create output file %q: %w", path, err)
	}

	defer func() {
		_ = file.Close()
	}()

	encoder := wav.NewEncoder(file, sampleRate, 16, 1, 1)

	intData := make([]int, len(samples))
	for i, sample := range samples {
		intData[i] = float32ToInt16(sample)
	}

	buffer := &audio.IntBuffer{
		Format: &audio.Format{
			NumChannels: 1,
			SampleRate:  sampleRate,
		},
		SourceBitDepth: 16,
		Data:           intData,
	}
	if err := encoder.Write(buffer); err != nil {
		return fmt.Errorf("write wav data: %w", err)
	}

	if err := encoder.Close(); err != nil {
		return fmt.Errorf("close wav writer: %w", err)
	}

	return nil
}

func float32ToInt16(sample float32) int {
	v := math.Max(-1, math.Min(1, float64(sample)))
	return int(math.Round(v * 32767))
}
