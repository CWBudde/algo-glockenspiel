package optimizer

import (
	"fmt"
	"math"

	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/model"
)

// The search space is shaped by the reference before the search starts. The
// helpers here are what every front end -- the fit command, the fit service,
// the browser fit -- calls to do that, so the three cannot disagree about it.

// KeepTemplateModes is the modes argument that leaves the template's modes
// alone rather than seeding them from the analysis.
const KeepTemplateModes = -1

// frequencySearchNyquistFraction is how far up to the sample rate a mode may
// be placed. A mode above it is not audible at the fitted note, and the model
// would still accept it, so the search would spend steps on nothing.
const frequencySearchNyquistFraction = 0.45

// MeasureReference measures the partials of a reference the way the
// objective's partial term does: from the first strike, with a window that
// shrinks to fit a short reference. It returns nil when the reference is too
// short to measure at all.
func MeasureReference(reference []float32, sampleRate int) *analysis.Measurement {
	if len(reference) == 0 || sampleRate <= 0 {
		return nil
	}

	return measurePartials(reference[analysis.Onset(reference):], sampleRate)
}

// FrequencyBoundsFor returns the mode-frequency box for a fit against a
// reference at sampleRate, in the hertz a preset authored at presetNote is
// written in when the fit plays it at note.
//
// The floor is half the reference's fundamental: nothing a struck bar
// radiates sits below its first mode, and the octave of slack covers a
// fundamental the analysis placed on a higher partial. The ceiling is the
// lesser of the default box and 0.45 times the sample rate, above which a
// mode is inaudible at the fitted note. Both are converted from the fitted
// note to the authored one, the inverse of what model.TransposeToNote does
// at render time. Without a measurement, or without a fundamental in it, the
// floor is the default box's.
func FrequencyBoundsFor(measurement *analysis.Measurement, sampleRate, presetNote, note int) Range {
	bounds := DefaultParamBounds.Frequency

	// authored = played / ratio, the inverse of TransposeToNote.
	ratio := math.Pow(2, float64(note-presetNote)/12)

	if measurement != nil && measurement.FundamentalHz > 0 {
		bounds.Min = math.Max(bounds.Min, measurement.FundamentalHz/2/ratio)
	}

	if sampleRate > 0 {
		bounds.Max = math.Min(bounds.Max, frequencySearchNyquistFraction*float64(sampleRate)/ratio)
	}

	if bounds.Max <= bounds.Min {
		return DefaultParamBounds.Frequency
	}

	return bounds
}

// SeedPreset returns the preset a search should start from: the template with
// its modes replaced by the reference's measured partials, through
// PresetFromAnalysis, or the template itself.
//
// modes is KeepTemplateModes to keep the template, zero for every partial the
// analysis listed, or a positive count for the strongest that many. The
// second return is how many modes were seeded, zero when the template was
// kept, which happens on request or when there is no measurement to seed
// from. Asking for more modes than the analysis found seeds every partial it
// did find; the count says so.
func SeedPreset(template *preset.Preset, measurement *analysis.Measurement, note, modes int) (*preset.Preset, int, error) {
	if template == nil {
		return nil, 0, fmt.Errorf("template preset cannot be nil")
	}

	if modes < 0 || measurement == nil || len(measurement.Partials) == 0 {
		return template, 0, nil
	}

	seeded, err := PresetFromAnalysis(template, measurement, note, modes)
	if err != nil {
		return nil, 0, err
	}

	return seeded, len(seeded.Parameters.Modes), nil
}

// narrowDecayBounds lowers the decay ceiling to what a preset authored at
// note may carry, so that the search cannot write a decay the preset file
// then refuses. A box that lies entirely above that ceiling is an error
// rather than a silently empty search.
func narrowDecayBounds(bounds ParamBounds, note int) (ParamBounds, error) {
	ceiling := model.AuthoredDecayMsMax(note)

	if bounds.DecayMs.Min > ceiling {
		return bounds, fmt.Errorf(
			"decay_ms bounds [%g, %g] lie above the %.1f ms a preset at note %d may carry",
			bounds.DecayMs.Min, bounds.DecayMs.Max, ceiling, note)
	}

	bounds.DecayMs.Max = math.Min(bounds.DecayMs.Max, ceiling)

	return bounds, nil
}
