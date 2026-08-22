package cli

import (
	"fmt"
	"os"

	"github.com/cwbudde/glockenspiel/internal/synth"
	"github.com/cwbudde/glockenspiel/internal/wavio"
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
		Example: `  # Render A4 from the built-in preset
  glockenspiel synth --output a4.wav

  # Render a longer note from a custom preset, trimming the silent tail
  glockenspiel synth --preset my-preset.json --note 84 --duration 4 --auto-stop --output c6.wav`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSynth(cmd, options)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&options.presetPath, "preset", options.presetPath, "Path to preset JSON file (default: built-in preset)")
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

	loadedPreset, err := loadPresetOrDefault(options.presetPath)
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

	if err := wavio.WriteMono(options.outputPath, options.sampleRate, samples); err != nil {
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
