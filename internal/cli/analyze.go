package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
	"github.com/spf13/cobra"
)

type analyzeOptions struct {
	referencePath  string
	outputPath     string
	trimmedPath    string
	downmix        string
	window         time.Duration
	keepLevel      bool
	frameSize      int
	maxPartials    int
	minLevelDB     float64
	minFrequencyHz float64
	jsonOutput     bool
}

func newAnalyzeCmd() *cobra.Command {
	options := analyzeOptions{
		frameSize:      analysis.DefaultFrameSize,
		maxPartials:    analysis.DefaultMaxPartials,
		minLevelDB:     analysis.DefaultMinLevelDB,
		minFrequencyHz: analysis.DefaultMinFrequencyHz,
	}

	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Measure a reference recording: its strike and its partials",
		Long: "Cut a reference to its first strike, normalise its level, and measure the " +
			"partials it holds: frequency, level, attack level and half-life, the way " +
			"testdata/reference/README.md measured the C5 recording by hand. The result is " +
			"the analysis.json a fit reads to size its search space.",
		Example: `  # Print the table for the shipped recording
  glockenspiel analyze --reference testdata/reference/glockenspiel_c5.wav

  # Write analysis.json and the cut reference next to a fit
  glockenspiel analyze --reference recording.wav \
    --output out/run/analysis.json --trimmed-out out/run/reference.wav`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAnalyze(cmd, options)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&options.referencePath, "reference", options.referencePath, "Path to reference WAV file")
	flags.StringVar(&options.outputPath, "output", options.outputPath, "Write the analysis as JSON to this path")
	flags.StringVar(&options.trimmedPath, "trimmed-out", options.trimmedPath,
		"Write the cut, normalised reference as a 16-bit mono WAV to this path")
	flags.StringVar(&options.downmix, "downmix", string(analysis.DownmixFirst),
		"How a multi-channel file is reduced: first (channel zero) or mean")
	flags.DurationVar(&options.window, "window", options.window,
		"Cut this long after the onset instead of finding where the strike ends (e.g. 1s)")
	flags.BoolVar(&options.keepLevel, "keep-level", options.keepLevel, "Leave the level alone instead of normalising the peak to full scale")
	flags.IntVar(&options.frameSize, "frame-size", options.frameSize, "Spectrum analysis window in samples")
	flags.IntVar(&options.maxPartials, "max-partials", options.maxPartials, "Report at most this many partials")
	flags.Float64Var(&options.minLevelDB, "min-level", options.minLevelDB, "Deepest level below the strongest partial to report, in dB")
	flags.Float64Var(&options.minFrequencyHz, "min-frequency", options.minFrequencyHz, "Ignore everything below this frequency in Hz")
	flags.BoolVar(&options.jsonOutput, "json", options.jsonOutput, "Print the analysis as JSON instead of text")

	_ = cmd.MarkFlagRequired("reference")

	return cmd
}

func runAnalyze(cmd *cobra.Command, options analyzeOptions) error {
	if options.referencePath == "" {
		return fmt.Errorf("reference is required")
	}

	if options.window < 0 {
		return fmt.Errorf("window must not be negative, got %s", options.window)
	}

	if options.frameSize <= 0 {
		return fmt.Errorf("frame-size must be positive, got %d", options.frameSize)
	}

	if options.maxPartials <= 0 {
		return fmt.Errorf("max-partials must be positive, got %d", options.maxPartials)
	}

	if options.minLevelDB >= 0 {
		return fmt.Errorf("min-level must be negative, got %g", options.minLevelDB)
	}

	downmix, err := analysis.ParseDownmix(options.downmix)
	if err != nil {
		return err
	}

	reference, err := analysis.LoadReference(options.referencePath, analysis.LoadOptions{
		Downmix:   downmix,
		Window:    options.window,
		KeepLevel: options.keepLevel,
	})
	if err != nil {
		return err
	}

	document, err := analysis.AnalyzeReference(options.referencePath, reference, analysis.PartialOptions{
		FrameSize:      options.frameSize,
		MaxPartials:    options.maxPartials,
		MinLevelDB:     options.minLevelDB,
		MinFrequencyHz: options.minFrequencyHz,
	})
	if err != nil {
		return err
	}

	if options.outputPath != "" {
		if err := document.WriteFile(options.outputPath); err != nil {
			return err
		}
	}

	if options.trimmedPath != "" {
		if err := wavio.WriteMono(options.trimmedPath, reference.SampleRate, reference.Samples); err != nil {
			return err
		}
	}

	if options.jsonOutput {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")

		return encoder.Encode(document)
	}

	writeAnalysis(cmd.OutOrStdout(), document, options)

	return nil
}

// writeAnalysis prints the document the way testdata/reference/README.md
// tabulates it, with the cut record above the table.
func writeAnalysis(out io.Writer, document *analysis.Analysis, options analyzeOptions) {
	ref := document.Reference
	print := func(format string, args ...any) {
		_, _ = fmt.Fprintf(out, format+"\n", args...)
	}

	print("reference %s", document.Source)
	print("  %d Hz, %d channel(s) read as %s, %d frames", ref.SampleRate, ref.Channels, ref.Downmix, ref.Frames)
	print("  strike from frame %d to %d (%.3f s, %s)", ref.Onset, ref.End, ref.Seconds, ref.EndRule)
	print("  peak %s before, gain %+.1f dB applied", formatDBFS(ref.PeakBefore), ref.GainDB)

	if options.outputPath != "" {
		print("  analysis written to %s", options.outputPath)
	}

	if options.trimmedPath != "" {
		print("  cut reference written to %s", options.trimmedPath)
	}

	print("")
	print("fundamental %.1f Hz, noise floor %.1f dB, %d partial(s) within %.0f dB of the strongest",
		document.FundamentalHz, document.NoiseFloorDB, len(document.Partials), -document.Options.MinLevelDB)
	print("")
	print("%12s %9s %9s %9s %10s %9s", "partial", "level", "amplitude", "attack", "half-life", "T60")

	for _, partial := range document.Partials {
		print("%9.1f Hz %6.1f dB %6.1f dB %6.1f dB %10s %9s",
			partial.FrequencyHz, partial.LevelDB, partial.AmplitudeDB, partial.AttackDB,
			formatMs(partial.HalfLifeMs), formatT60(partial.HalfLifeMs))
	}
}

func formatDBFS(linear float64) string {
	if linear <= 0 {
		return "-inf dBFS"
	}

	return fmt.Sprintf("%.1f dBFS", 20*math.Log10(linear))
}

func formatMs(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "n/a"
	}

	return fmt.Sprintf("%.0f ms", value)
}

// formatT60 is the half-life scaled to the time a decay takes to fall 60 dB,
// which is how a room, and the README's table, states a decay.
func formatT60(halfLifeMs float64) string {
	if math.IsNaN(halfLifeMs) || math.IsInf(halfLifeMs, 0) {
		return "n/a"
	}

	return fmt.Sprintf("%.2f s", halfLifeMs/1000*60/(20*math.Log10(2)))
}
