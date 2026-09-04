package fitschema

import (
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/model"
)

// Range is one [min, max] pair.
type Range struct {
	Min float64
	Max float64
}

// BoundsKeys names the keys a bounds document may carry, in the order
// optimizer.BoundsKeys documents them. It is a thin re-export rather than a
// second list: optimizer.DecodeParamBounds already owns this vocabulary, and
// it is exported precisely so a second copy does not have to exist here.
func BoundsKeys() []string {
	return optimizer.BoundsKeys
}

// LogEncodedBoundsKeys names the dimensions optimizer.ParamCodec log-encodes
// -- see the math.Log10 calls in internal/optimizer/params.go. Their bounds
// must stay strictly above zero: log(0) is not a number the unit-cube
// encoding can take, and the server answers a non-positive one with a 400.
func LogEncodedBoundsKeys() []string {
	return []string{"filter_freq", "frequency", "decay_ms"}
}

// ModelBoundsLimits is the range model.ValidateBarParams holds each
// dimension to, keyed by its bounds document name. DecodeParamBounds in
// internal/optimizer/boundsfile.go rejects a supplied range that leaves this
// box, and for a good reason: every candidate drawn from outside it would
// fail validation and score +Inf, so the fit would burn its whole budget to
// produce nothing.
func ModelBoundsLimits() map[string]Range {
	return map[string]Range{
		"input_mix":     {Min: model.InputMixMin, Max: model.InputMixMax},
		"filter_freq":   {Min: model.FilterFrequencyMinHz, Max: model.FilterFrequencyMaxHz},
		"amplitude":     {Min: model.AmplitudeMin, Max: model.AmplitudeMax},
		"frequency":     {Min: model.FrequencyMinHz, Max: model.FrequencyMaxHz},
		"decay_ms":      {Min: model.DecayMsMin, Max: model.DecayMsValidationMax},
		"harmonic_gain": {Min: model.HarmonicGainMin, Max: model.HarmonicGainMax},
	}
}

// DefaultParamBounds is optimizer.DefaultParamBounds, the search box a fit
// starts from before a request or an uploaded bounds document narrows it,
// keyed by its bounds document name.
func DefaultParamBounds() map[string]Range {
	bounds := optimizer.DefaultParamBounds

	return map[string]Range{
		"input_mix":     {Min: bounds.InputMix.Min, Max: bounds.InputMix.Max},
		"filter_freq":   {Min: bounds.FilterFreq.Min, Max: bounds.FilterFreq.Max},
		"amplitude":     {Min: bounds.Amplitude.Min, Max: bounds.Amplitude.Max},
		"frequency":     {Min: bounds.Frequency.Min, Max: bounds.Frequency.Max},
		"decay_ms":      {Min: bounds.DecayMs.Min, Max: bounds.DecayMs.Max},
		"harmonic_gain": {Min: bounds.HarmonicGain.Min, Max: bounds.HarmonicGain.Max},
	}
}
