package optimizer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// The bounds file lives here rather than in internal/cli because the CLI is no
// longer its only reader: the fit API accepts the same document as an optional
// multipart field, and a second copy of the parser would be a second place for
// the key names, the finiteness check or the min<max rule to drift. Phase 4.2
// made the same call for the WAV decoder when it created internal/wavio.
//
// internal/optimizer owns ParamBounds and ObjectiveConfig, so it owns the JSON
// spelling of its own vocabulary too.

// BoundsRange is one [min, max] pair in a bounds document.
type BoundsRange [2]float64

// BoundsDocument is the JSON form of ParamBounds.
//
// Every field is optional; an omitted field keeps the corresponding default
// bound, so a document can narrow a single dimension without restating the
// rest:
//
//	{
//	  "input_mix":      [0.0, 2.0],
//	  "filter_freq":    [500.0, 8000.0],
//	  "base_frequency": [400.0, 500.0],
//	  "amplitude":      [-1.0, 1.0],
//	  "frequency_mult": [0.5, 10.0],
//	  "decay_ms":       [50.0, 400.0],
//	  "harmonic_gain":  [0.0, 1.0]
//	}
type BoundsDocument struct {
	InputMix      *BoundsRange `json:"input_mix"`
	FilterFreq    *BoundsRange `json:"filter_freq"`
	BaseFrequency *BoundsRange `json:"base_frequency"`
	Amplitude     *BoundsRange `json:"amplitude"`
	FrequencyMult *BoundsRange `json:"frequency_mult"`
	DecayMs       *BoundsRange `json:"decay_ms"`
	HarmonicGain  *BoundsRange `json:"harmonic_gain"`
}

// BoundsKeys names the accepted keys, in the order they are documented. It is
// what the CLI's --bounds help text and the server's error messages are built
// from, so the list cannot fall out of step with the struct above.
var BoundsKeys = []string{
	"input_mix", "filter_freq", "base_frequency", "amplitude",
	"frequency_mult", "decay_ms", "harmonic_gain",
}

// LoadParamBounds reads a bounds file and overlays it on the default bounds.
func LoadParamBounds(path string) (ParamBounds, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultParamBounds, fmt.Errorf("read bounds %q: %w", path, err)
	}

	return DecodeParamBounds(data, path)
}

// DecodeParamBounds parses a bounds document and overlays it on the default
// bounds. source names the origin for error messages, the way preset.Decode
// takes one, so bytes that never came from a file can still be reported
// usefully.
//
// Unknown keys are rejected: a misspelled dimension that was silently ignored
// would run a fit against the default box while the caller believed it had
// narrowed one.
func DecodeParamBounds(data []byte, source string) (ParamBounds, error) {
	bounds := DefaultParamBounds

	var document BoundsDocument

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&document); err != nil {
		return bounds, fmt.Errorf("decode bounds %q: %w", source, err)
	}

	fields := []struct {
		name   string
		source *BoundsRange
		target *Range
	}{
		{"input_mix", document.InputMix, &bounds.InputMix},
		{"filter_freq", document.FilterFreq, &bounds.FilterFreq},
		{"base_frequency", document.BaseFrequency, &bounds.BaseFrequency},
		{"amplitude", document.Amplitude, &bounds.Amplitude},
		{"frequency_mult", document.FrequencyMult, &bounds.FrequencyMult},
		{"decay_ms", document.DecayMs, &bounds.DecayMs},
		{"harmonic_gain", document.HarmonicGain, &bounds.HarmonicGain},
	}

	for _, field := range fields {
		if field.source == nil {
			continue
		}

		low, high := field.source[0], field.source[1]
		if math.IsNaN(low) || math.IsNaN(high) || math.IsInf(low, 0) || math.IsInf(high, 0) {
			return bounds, fmt.Errorf("bounds %q: %s must be finite", source, field.name)
		}

		if low >= high {
			return bounds, fmt.Errorf("bounds %q: %s min %g must be below max %g", source, field.name, low, high)
		}

		*field.target = Range{Min: low, Max: high}
	}

	return bounds, nil
}
