package cli

import (
	"fmt"
	"io"
	"math"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/spf13/pflag"
)

// referenceOptions are the flags fit and distance share for reading a
// reference: the loader's policy plus an optional analysis document.
type referenceOptions struct {
	downmix      string
	window       time.Duration
	keepLevel    bool
	analysisPath string
}

// addReferenceFlags registers the loader's flags on a command.
func addReferenceFlags(flags *pflag.FlagSet, options *referenceOptions) {
	flags.StringVar(&options.downmix, "downmix", options.downmix,
		"How a multi-channel reference is reduced to one: first (channel zero) or mean")
	flags.DurationVar(&options.window, "window", options.window,
		"Cut the reference to this length after its onset instead of where the strike ends")
	flags.BoolVar(&options.keepLevel, "keep-level", options.keepLevel,
		"Keep the reference at the file's level instead of peak-normalising it; only a legacy metric without --normalize-gain can tell")
	flags.StringVar(&options.analysisPath, "analysis", options.analysisPath,
		"An analysis.json from `glockenspiel analyze` whose partials the partial term uses (default: measure the reference)")
}

// loadFitReference reads a reference the way a fit sees it: one channel,
// cut to its first strike, peak-normalised, and checked against the sample
// rate the fit runs at. It also reads the analysis document, if one was
// named, for the partial term.
func loadFitReference(path string, options referenceOptions, sampleRate int) (*analysis.Reference, *analysis.Measurement, error) {
	downmix, err := analysis.ParseDownmix(options.downmix)
	if err != nil {
		return nil, nil, err
	}

	if options.window < 0 {
		return nil, nil, fmt.Errorf("window must not be negative, got %s", options.window)
	}

	reference, err := analysis.LoadReference(path, analysis.LoadOptions{Downmix: downmix, Window: options.window, KeepLevel: options.keepLevel})
	if err != nil {
		return nil, nil, err
	}

	if reference.SampleRate != sampleRate {
		return nil, nil, fmt.Errorf("reference sample rate %d does not match requested sample rate %d", reference.SampleRate, sampleRate)
	}

	var measurement *analysis.Measurement

	if options.analysisPath != "" {
		document, err := analysis.ReadFile(options.analysisPath)
		if err != nil {
			return nil, nil, err
		}

		measurement = &document.Measurement
	}

	return reference, measurement, nil
}

// writeSeededModes prints one line saying where the starting modes came from.
func writeSeededModes(out io.Writer, starting *preset.Preset, seeded, requested int) {
	switch {
	case seeded > 0 && requested > seeded:
		_, _ = fmt.Fprintf(out, "modes: %d seeded from the reference's partials (asked for %d, the analysis lists %d)\n",
			seeded, requested, seeded)
	case seeded > 0:
		_, _ = fmt.Fprintf(out, "modes: %d seeded from the reference's partials\n", seeded)
	case requested >= 0:
		_, _ = fmt.Fprintf(out, "modes: keeping the preset's %d (the analysis lists no partials to seed from)\n",
			len(starting.Parameters.Modes))
	default:
		_, _ = fmt.Fprintf(out, "modes: keeping the preset's %d\n", len(starting.Parameters.Modes))
	}
}

// writePinned lists the dimensions of a result that sit on an edge of the
// search box, which is where the search wanted to go further, in the form
// the distance command prints them.
func writePinned(out io.Writer, pinned []optimizer.PinnedDimension, dimension int) {
	_, _ = fmt.Fprintf(out, "pinned: %d of %d dimensions on a bound\n", len(pinned), dimension)

	for _, pinned := range pinned {
		_, _ = fmt.Fprintf(out, "  %s = %g (%s %g)\n", pinned.Name, pinned.Value, pinned.Bound, pinned.Limit)
	}
}

// writeReferenceCut prints one line saying what the loader did to the file.
func writeReferenceCut(out io.Writer, path string, reference *analysis.Reference) {
	_, _ = fmt.Fprintf(out, "reference %s: channel %s of %d, cut %d..%d (%.3f s, %s), gain %s dB\n",
		path, reference.Downmix, reference.Channels, reference.Onset, reference.End,
		reference.Seconds, reference.EndRule, formatDB(reference.GainDB))
}

// formatMetricsLine renders the terms on one line, for a progress report.
func formatMetricsLine(metrics optimizer.Metrics) string {
	return fmt.Sprintf("partials cents=%s level=%s decay=%s missing=%s extra=%s | spectral fine=%s coarse=%s | envelope %s slope=%s | waveform %s gain=%s",
		formatTerm3(metrics.PartialCents), formatTerm3(metrics.PartialLevelDB), formatTerm3(metrics.PartialDecayOctaves),
		formatTerm3(metrics.PartialMissing), formatTerm3(metrics.PartialExtra),
		formatTerm3(metrics.SpectralFineDB), formatTerm3(metrics.SpectralCoarseDB),
		formatTerm3(metrics.EnvelopeDB), formatTerm3(metrics.DecaySlopeDBps),
		formatTerm3(metrics.Waveform), formatDB(metrics.GainDB))
}

// formatTerm3 prints a term to three significant digits, or n/a.
func formatTerm3(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "n/a"
	}

	return fmt.Sprintf("%.3g", value)
}

// writeMetrics prints the composite breakdown: one line per term with its
// value, its share of the score under the profile, and the solved gain and
// matching underneath.
func writeMetrics(out io.Writer, metrics optimizer.Metrics, profile optimizer.Profile) {
	_, _ = fmt.Fprintf(out, "%-22s %12s %8s %6s %7s\n", "term", "value", "norm", "weight", "share")

	for _, contribution := range metrics.Contributions(profile) {
		value := "n/a"
		if contribution.Measured {
			value = fmt.Sprintf("%.4g %s", contribution.Value, contribution.Term.Unit())
		}

		_, _ = fmt.Fprintf(out, "%-22s %12s %8.4g %6.3f %7.3f\n",
			contribution.Term, value, contribution.Norm, contribution.Weight, contribution.Share)
	}

	_, _ = fmt.Fprintf(out, "score %.4f (%s); gain %s dB, waveform gain %s dB, lag %d, %d of %d reference partials matched by %d model partials\n",
		metrics.Score(profile), profile.Name, formatDB(metrics.GainDB), formatDB(metrics.WaveformGainDB),
		metrics.Lag, metrics.Matched, metrics.ReferencePartials, metrics.ModelPartials)
}
