package model

import (
	"errors"
	"fmt"
	"math"
)

const (
	// NumModes is the fixed number of resonant modes in the initial model.
	NumModes = 4

	InputMixMin = 0.0
	InputMixMax = 2.0

	FilterFrequencyMinHz = 20.0
	FilterFrequencyMaxHz = 20000.0

	AmplitudeMin = -2.0
	AmplitudeMax = 2.0

	FrequencyMinHz = 0.01
	FrequencyMaxHz = 50000.0

	DecayMsMin = 0.1
	DecayMsMax = 500.0

	HarmonicGainMin = 0.0
	HarmonicGainMax = 2.0
)

// ParamBounds contains optimization parameter bounds.
type ParamBounds struct {
	InputMix      [2]float64 // [0.0, 2.0]
	FilterFreq    [2]float64 // [20.0, 20000.0] Hz, log scale
	Amplitude     [2]float64 // [-2.0, 2.0]
	FrequencyMult [2]float64 // [0.5, 10.0] * base_frequency
	DecayMs       [2]float64 // [0.1, 500.0] milliseconds
	HarmonicGain  [2]float64 // [0.0, 2.0] per harmonic
}

// DefaultParamBounds are the bounds used for optimization.
var DefaultParamBounds = ParamBounds{
	InputMix:      [2]float64{InputMixMin, InputMixMax},
	FilterFreq:    [2]float64{FilterFrequencyMinHz, FilterFrequencyMaxHz},
	Amplitude:     [2]float64{AmplitudeMin, AmplitudeMax},
	FrequencyMult: [2]float64{0.5, 10.0},
	DecayMs:       [2]float64{DecayMsMin, DecayMsMax},
	HarmonicGain:  [2]float64{HarmonicGainMin, HarmonicGainMax},
}

// ModeParams describes one resonant mode.
type ModeParams struct {
	Amplitude float64 `json:"amplitude"`
	Frequency float64 `json:"frequency"`
	DecayMs   float64 `json:"decay_ms"`
}

// ChebyshevParams controls harmonic excitation.
type ChebyshevParams struct {
	Enabled       bool      `json:"enabled"`
	HarmonicGains []float64 `json:"harmonic_gains"`
}

// BarParams are the top-level model parameters for one bar.
type BarParams struct {
	InputMix        float64              `json:"input_mix"`
	FilterFrequency float64              `json:"filter_frequency"`
	BaseFrequency   float64              `json:"base_frequency"`
	Modes           [NumModes]ModeParams `json:"modes"`
	Chebyshev       ChebyshevParams      `json:"chebyshev"`
}

// Validate checks whether BarParams are well-formed and in supported ranges.
func (p *BarParams) Validate() error {
	return ValidateBarParams(p)
}

// ValidateBarParams validates bar model parameters.
func ValidateBarParams(p *BarParams) error {
	if p == nil {
		return errors.New("bar params cannot be nil")
	}

	if err := validateFiniteRange("input_mix", p.InputMix, InputMixMin, InputMixMax); err != nil {
		return err
	}
	if err := validateFiniteRange("filter_frequency", p.FilterFrequency, FilterFrequencyMinHz, FilterFrequencyMaxHz); err != nil {
		return err
	}
	if err := validateFiniteRange("base_frequency", p.BaseFrequency, FrequencyMinHz, FrequencyMaxHz); err != nil {
		return err
	}

	for i, mode := range p.Modes {
		if err := validateFiniteRange(fmt.Sprintf("modes[%d].amplitude", i), mode.Amplitude, AmplitudeMin, AmplitudeMax); err != nil {
			return err
		}
		if err := validateFiniteRange(fmt.Sprintf("modes[%d].frequency", i), mode.Frequency, FrequencyMinHz, FrequencyMaxHz); err != nil {
			return err
		}
		if err := validateFiniteRange(fmt.Sprintf("modes[%d].decay_ms", i), mode.DecayMs, DecayMsMin, DecayMsMax); err != nil {
			return err
		}
	}

	for i, gain := range p.Chebyshev.HarmonicGains {
		if err := validateFiniteRange(fmt.Sprintf("chebyshev.harmonic_gains[%d]", i), gain, HarmonicGainMin, HarmonicGainMax); err != nil {
			return err
		}
	}

	return nil
}

func validateFiniteRange(field string, value, min, max float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s must be finite", field)
	}
	if value < min || value > max {
		return fmt.Errorf("%s out of range [%g, %g]: %g", field, min, max, value)
	}
	return nil
}
