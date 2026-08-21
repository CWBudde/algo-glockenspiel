package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"

	"github.com/cwbudde/glockenspiel/internal/optimizer"
)

// boundsRange is one [min, max] pair in a bounds file.
type boundsRange [2]float64

// boundsFile is the JSON form of optimizer.ParamBounds.
//
// Every field is optional; an omitted field keeps the corresponding default
// bound, so a file can narrow a single dimension without restating the rest:
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
type boundsFile struct {
	InputMix      *boundsRange `json:"input_mix"`
	FilterFreq    *boundsRange `json:"filter_freq"`
	BaseFrequency *boundsRange `json:"base_frequency"`
	Amplitude     *boundsRange `json:"amplitude"`
	FrequencyMult *boundsRange `json:"frequency_mult"`
	DecayMs       *boundsRange `json:"decay_ms"`
	HarmonicGain  *boundsRange `json:"harmonic_gain"`
}

// boundsFlagHelp documents the --bounds JSON shape in `fit --help`.
const boundsFlagHelp = "Path to a JSON file narrowing the search bounds; keys " +
	"input_mix, filter_freq, base_frequency, amplitude, frequency_mult, decay_ms, " +
	"harmonic_gain each hold a [min, max] pair, and omitted keys keep the default bound"

// loadParamBounds reads a bounds file and overlays it on the default bounds.
func loadParamBounds(path string) (optimizer.ParamBounds, error) {
	bounds := optimizer.DefaultParamBounds

	data, err := os.ReadFile(path)
	if err != nil {
		return bounds, fmt.Errorf("read bounds %q: %w", path, err)
	}

	var file boundsFile

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&file); err != nil {
		return bounds, fmt.Errorf("decode bounds %q: %w", path, err)
	}

	fields := []struct {
		name   string
		source *boundsRange
		target *optimizer.Range
	}{
		{"input_mix", file.InputMix, &bounds.InputMix},
		{"filter_freq", file.FilterFreq, &bounds.FilterFreq},
		{"base_frequency", file.BaseFrequency, &bounds.BaseFrequency},
		{"amplitude", file.Amplitude, &bounds.Amplitude},
		{"frequency_mult", file.FrequencyMult, &bounds.FrequencyMult},
		{"decay_ms", file.DecayMs, &bounds.DecayMs},
		{"harmonic_gain", file.HarmonicGain, &bounds.HarmonicGain},
	}

	for _, field := range fields {
		if field.source == nil {
			continue
		}

		low, high := field.source[0], field.source[1]
		if math.IsNaN(low) || math.IsNaN(high) || math.IsInf(low, 0) || math.IsInf(high, 0) {
			return bounds, fmt.Errorf("bounds %q: %s must be finite", path, field.name)
		}

		if low >= high {
			return bounds, fmt.Errorf("bounds %q: %s min %g must be below max %g", path, field.name, low, high)
		}

		*field.target = optimizer.Range{Min: low, Max: high}
	}

	return bounds, nil
}
