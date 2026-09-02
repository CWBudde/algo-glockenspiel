package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"math"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/spf13/cobra"
)

type distanceOptions struct {
	referencePath string
	presetPath    string
	boundsPath    string
	note          int
	velocity      int
	sampleRate    int
	jsonOutput    bool
	reference     referenceOptions
}

func newDistanceCmd() *cobra.Command {
	options := distanceOptions{
		note:       69,
		velocity:   100,
		sampleRate: 44100,
	}

	cmd := &cobra.Command{
		Use:   "distance",
		Short: "Score a preset against a reference the way fit would",
		Long: "Render a preset once and print every term of the fit objective for it, " +
			"raw and time-aligned, with and without gain normalisation, through the same " +
			"code the optimizer scores candidates with. Nothing is searched.",
		Example: `  # What the shipped preset scores against the legacy render
  glockenspiel distance --reference testdata/reference/legacy_synth_a4.wav

  # The same for a fitted preset, as JSON for a script
  glockenspiel distance --reference recording.wav --preset out/fit/a4.json --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDistance(cmd, options)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&options.referencePath, "reference", options.referencePath, "Path to reference WAV file")
	flags.StringVar(&options.presetPath, "preset", options.presetPath, "Path to preset JSON file (default: built-in preset)")
	flags.StringVar(&options.boundsPath, "bounds", options.boundsPath,
		"JSON file with the search box to judge pinned dimensions against, kept strict as fit does")
	flags.IntVar(&options.note, "note", options.note, "MIDI note number to render")
	flags.IntVar(&options.velocity, "velocity", options.velocity, "MIDI velocity (0-127)")
	flags.IntVar(&options.sampleRate, "sample-rate", options.sampleRate, "Reference/render sample rate in Hz")
	flags.BoolVar(&options.jsonOutput, "json", options.jsonOutput, "Print the report as JSON instead of text")
	addReferenceFlags(flags, &options.reference)

	_ = cmd.MarkFlagRequired("reference")

	return cmd
}

func runDistance(cmd *cobra.Command, options distanceOptions) error {
	if options.referencePath == "" {
		return fmt.Errorf("reference is required")
	}

	if options.note < 0 || options.note > 127 {
		return fmt.Errorf("note must be in [0,127], got %d", options.note)
	}

	if options.velocity < 0 || options.velocity > 127 {
		return fmt.Errorf("velocity must be in [0,127], got %d", options.velocity)
	}

	if options.sampleRate <= 0 {
		return fmt.Errorf("sample-rate must be positive, got %d", options.sampleRate)
	}

	reference, measurement, err := loadFitReference(options.referencePath, options.reference, options.sampleRate)
	if err != nil {
		return err
	}

	written, err := loadPresetOrDefault(options.presetPath)
	if err != nil {
		return err
	}

	config := optimizer.DistanceConfig{
		SampleRate: options.sampleRate,
		Note:       options.note,
		Velocity:   options.velocity,
		Bounds:     optimizer.DefaultParamBounds,
		Analysis:   measurement,
	}

	if options.boundsPath != "" {
		config.Bounds, err = optimizer.LoadParamBounds(options.boundsPath)
		if err != nil {
			return err
		}

		config.StrictBounds = true
	}

	report, err := optimizer.Distance(reference.Samples, written, config)
	if err != nil {
		return err
	}

	if options.jsonOutput {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")

		return encoder.Encode(report)
	}

	presetName := options.presetPath
	if presetName == "" {
		presetName = "built-in preset"
	}

	writeReferenceCut(cmd.OutOrStdout(), options.referencePath, reference)
	writeDistanceReport(cmd.OutOrStdout(), report, options.referencePath, presetName, written.Name)

	return nil
}

// writeDistanceReport prints the report as a small table: one line of context
// each for the reference and the preset, one row per policy, then the box.
func writeDistanceReport(out io.Writer, report *optimizer.DistanceReport, referencePath, presetPath, presetName string) {
	seconds := float64(report.ReferenceSamples) / float64(report.SampleRate)

	_, _ = fmt.Fprintf(out, "reference %s\n  %d samples, %.3f s at %d Hz, peak %s dBFS, rms %s dBFS\n",
		referencePath, report.ReferenceSamples, seconds, report.SampleRate,
		formatDB(report.Reference.PeakDBFS), formatDB(report.Reference.RMSDBFS))
	_, _ = fmt.Fprintf(out, "preset %s (%s)\n  %d modes, %d dimensions, note %d, velocity %d; render peak %s dBFS, rms %s dBFS\n",
		presetName, presetPath, report.Modes, report.Dimension, report.Note, report.Velocity,
		formatDB(report.Render.PeakDBFS), formatDB(report.Render.RMSDBFS))

	if report.Clamped {
		_, _ = fmt.Fprintln(out, "  the preset lies outside the strict box, so what was scored is the clamped preset")
	}

	_, _ = fmt.Fprintf(out, "\n%-13s %12s %12s %12s %7s %9s %9s\n",
		"policy", "rms", "log", "spectral", "lag", "gain", "gain_db")

	for _, row := range []struct {
		name string
		m    optimizer.Measurement
	}{
		{"raw", report.Raw},
		{"aligned", report.Aligned},
		{"aligned+gain", report.AlignedGain},
	} {
		_, _ = fmt.Fprintf(out, "%-13s %12s %12s %12s %7d %9s %9s\n",
			row.name, formatTerm(row.m.RMS), formatTerm(row.m.Log), formatTerm(row.m.Spectral),
			row.m.Lag, formatTerm(row.m.Gain), formatDB(20*math.Log10(row.m.Gain)))
	}

	_, _ = fmt.Fprintln(out, "  gain is measured under every policy and divided out only under aligned+gain")

	_, _ = fmt.Fprintln(out, "\ncomposite objective, aligned, level gain solved:")
	writeMetrics(out, report.Metrics, optimizer.ProfileBalanced)
	_, _ = fmt.Fprintf(out, "  scores: balanced %.4f, placement %.4f, polish %.4f\n",
		report.Scores[string(optimizer.MetricBalanced)], report.Scores[string(optimizer.MetricPlacement)],
		report.Scores[string(optimizer.MetricPolish)])

	if len(report.Widened) > 0 {
		_, _ = fmt.Fprintln(out, "\nbox widened to contain the preset:")

		for _, widened := range report.Widened {
			_, _ = fmt.Fprintf(out, "  %s %s %g -> %g\n", widened.Name, widened.Side, widened.From, widened.To)
		}
	}

	_, _ = fmt.Fprintf(out, "\npinned: %d of %d dimensions on a bound\n", len(report.Pinned), report.Dimension)

	for _, pinned := range report.Pinned {
		_, _ = fmt.Fprintf(out, "  %s = %g (%s %g)\n", pinned.Name, pinned.Value, pinned.Bound, pinned.Limit)
	}
}

// formatTerm prints an objective term, or n/a where the term could not be
// computed, which for the spectral term means a reference shorter than a frame.
func formatTerm(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "n/a"
	}

	return fmt.Sprintf("%.6g", value)
}

func formatDB(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "-inf"
	}

	return fmt.Sprintf("%+.2f", value)
}
