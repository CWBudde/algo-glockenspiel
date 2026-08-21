package model

import (
	"errors"
	"fmt"
	"math"
)

const (
	// NumModes is the default resonant mode count and the count every v1 preset
	// carries. It is a default, not a limit: BarParams.Modes is a slice and the
	// oscillator bank sizes itself at runtime.
	NumModes = 4

	// MaxModes bounds a preset's mode count so a malformed file cannot ask for
	// an unbounded allocation.
	MaxModes = 512

	// MaxHarmonics bounds the partials one mode may carry.
	MaxHarmonics = 64

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
//
// Harmonics are optional integer-multiple partials computed on top of this
// mode's oscillator: entry k adds a rotor at (k+1) * Frequency sharing DecayMs,
// with its gain applied on top of Amplitude. An empty slice means the mode is a
// single oscillator at its fundamental, which is what every v1 preset describes.
type ModeParams struct {
	Amplitude float64   `json:"amplitude"`
	Frequency float64   `json:"frequency"`
	DecayMs   float64   `json:"decay_ms"`
	Harmonics []float64 `json:"harmonics,omitempty"`
}

// Clone returns a deep copy of the mode.
func (m ModeParams) Clone() ModeParams {
	if len(m.Harmonics) > 0 {
		m.Harmonics = append([]float64(nil), m.Harmonics...)
	}

	return m
}

// ChebyshevStage selects where the Chebyshev waveshaper sits in the chain.
type ChebyshevStage string

const (
	// ChebyshevStageExcitation shapes the filtered excitation before it reaches
	// the oscillators. This is what v1 presets describe and stays the default,
	// so their rendering is unchanged.
	ChebyshevStageExcitation ChebyshevStage = "excitation"

	// ChebyshevStageOutput shapes the oscillator bank's output instead, which
	// is the post-oscillator placement the shaper was always meant to have.
	ChebyshevStageOutput ChebyshevStage = "output"
)

// ChebyshevParams controls harmonic excitation.
type ChebyshevParams struct {
	Enabled       bool           `json:"enabled"`
	Stage         ChebyshevStage `json:"stage,omitempty"`
	HarmonicGains []float64      `json:"harmonic_gains"`
}

// ResolvedStage returns the shaper stage, defaulting to the v1 placement.
func (c ChebyshevParams) ResolvedStage() ChebyshevStage {
	if c.Stage == ChebyshevStageOutput {
		return ChebyshevStageOutput
	}

	return ChebyshevStageExcitation
}

// Clone returns a deep copy of the Chebyshev parameters.
func (c ChebyshevParams) Clone() ChebyshevParams {
	if len(c.HarmonicGains) > 0 {
		c.HarmonicGains = append([]float64(nil), c.HarmonicGains...)
	}

	return c
}

// BarParams are the top-level model parameters for one bar.
//
// Modes is a slice: the mode count is runtime configuration. Copy BarParams
// with Clone rather than assignment, or the copy shares this slice.
type BarParams struct {
	InputMix        float64         `json:"input_mix"`
	FilterFrequency float64         `json:"filter_frequency"`
	BaseFrequency   float64         `json:"base_frequency"`
	Modes           []ModeParams    `json:"modes"`
	Chebyshev       ChebyshevParams `json:"chebyshev"`
}

// Clone returns a deep copy, safe to mutate independently of the original.
func (p BarParams) Clone() BarParams {
	if len(p.Modes) > 0 {
		modes := make([]ModeParams, len(p.Modes))
		for i, mode := range p.Modes {
			modes[i] = mode.Clone()
		}

		p.Modes = modes
	}

	p.Chebyshev = p.Chebyshev.Clone()

	return p
}

// DefaultModes returns a zero-valued slice of the default mode count, so
// callers that still think in terms of four modes have somewhere to start.
func DefaultModes() []ModeParams {
	return make([]ModeParams, NumModes)
}

// Validate checks whether BarParams are well-formed and in supported ranges.
func (p *BarParams) Validate() error {
	return ValidateBarParams(p)
}

// ValidateBarParams validates bar model parameters.
func ValidateBarParams(params *BarParams) error {
	if params == nil {
		return errors.New("bar params cannot be nil")
	}

	if err := validateFiniteRange("input_mix", params.InputMix, InputMixMin, InputMixMax); err != nil {
		return err
	}

	if err := validateFiniteRange("filter_frequency", params.FilterFrequency, FilterFrequencyMinHz, FilterFrequencyMaxHz); err != nil {
		return err
	}

	if err := validateFiniteRange("base_frequency", params.BaseFrequency, FrequencyMinHz, FrequencyMaxHz); err != nil {
		return err
	}

	if len(params.Modes) > MaxModes {
		return fmt.Errorf("modes: %d exceeds the maximum of %d", len(params.Modes), MaxModes)
	}

	for modeIndex, mode := range params.Modes {
		if len(mode.Harmonics) > MaxHarmonics {
			return fmt.Errorf("modes[%d].harmonics: %d exceeds the maximum of %d", modeIndex, len(mode.Harmonics), MaxHarmonics)
		}

		for harmonicIndex, gain := range mode.Harmonics {
			if !isFiniteInRange(gain, HarmonicGainMin, HarmonicGainMax) {
				return fmt.Errorf("modes[%d].harmonics[%d] out of range [%g, %g]: %g",
					modeIndex, harmonicIndex, HarmonicGainMin, HarmonicGainMax, gain)
			}
		}

		if !isFiniteInRange(mode.Amplitude, AmplitudeMin, AmplitudeMax) {
			return rangeErrorf("modes[%d].amplitude", modeIndex, mode.Amplitude, AmplitudeMin, AmplitudeMax)
		}

		if !isFiniteInRange(mode.Frequency, FrequencyMinHz, FrequencyMaxHz) {
			return rangeErrorf("modes[%d].frequency", modeIndex, mode.Frequency, FrequencyMinHz, FrequencyMaxHz)
		}

		if !isFiniteInRange(mode.DecayMs, DecayMsMin, DecayMsMax) {
			return rangeErrorf("modes[%d].decay_ms", modeIndex, mode.DecayMs, DecayMsMin, DecayMsMax)
		}
	}

	if stage := params.Chebyshev.Stage; stage != "" && stage != ChebyshevStageExcitation && stage != ChebyshevStageOutput {
		return fmt.Errorf("chebyshev.stage must be %q or %q: %q", ChebyshevStageExcitation, ChebyshevStageOutput, stage)
	}

	for gainIndex, gain := range params.Chebyshev.HarmonicGains {
		if !isFiniteInRange(gain, HarmonicGainMin, HarmonicGainMax) {
			return rangeErrorf("chebyshev.harmonic_gains[%d]", gainIndex, gain, HarmonicGainMin, HarmonicGainMax)
		}
	}

	return nil
}

func isFiniteInRange(value, min, max float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= min && value <= max
}

func validateFiniteRange(field string, value, min, max float64) error {
	if !isFiniteInRange(value, min, max) {
		return rangeError(field, value, min, max)
	}

	return nil
}

func rangeError(field string, value, min, max float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s must be finite", field)
	}

	return fmt.Errorf("%s out of range [%g, %g]: %g", field, min, max, value)
}

func rangeErrorf(fieldFmt string, index int, value, min, max float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf(fieldFmt+" must be finite", index)
	}

	return fmt.Errorf(fieldFmt+" out of range [%g, %g]: %g", index, min, max, value)
}
