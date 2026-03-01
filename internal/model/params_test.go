package model

import (
	"math"
	"strings"
	"testing"
)

func TestValidateBarParamsValid(t *testing.T) {
	params := validTestParams()
	if err := ValidateBarParams(&params); err != nil {
		t.Fatalf("expected params to be valid, got error: %v", err)
	}
}

func TestValidateBarParamsNil(t *testing.T) {
	err := ValidateBarParams(nil)
	if err == nil {
		t.Fatal("expected error for nil params")
	}
}

func TestValidateBarParamsInputMixOutOfRange(t *testing.T) {
	params := validTestParams()
	params.InputMix = InputMixMax + 0.01

	err := ValidateBarParams(&params)
	assertFieldError(t, err, "input_mix")
}

func TestValidateBarParamsFilterFrequencyOutOfRange(t *testing.T) {
	params := validTestParams()
	params.FilterFrequency = FilterFrequencyMinHz - 0.01

	err := ValidateBarParams(&params)
	assertFieldError(t, err, "filter_frequency")
}

func TestValidateBarParamsBaseFrequencyOutOfRange(t *testing.T) {
	params := validTestParams()
	params.BaseFrequency = 0

	err := ValidateBarParams(&params)
	assertFieldError(t, err, "base_frequency")
}

func TestValidateBarParamsModeAmplitudeOutOfRange(t *testing.T) {
	params := validTestParams()
	params.Modes[2].Amplitude = AmplitudeMin - 0.01

	err := ValidateBarParams(&params)
	assertFieldError(t, err, "modes[2].amplitude")
}

func TestValidateBarParamsModeFrequencyOutOfRange(t *testing.T) {
	params := validTestParams()
	params.Modes[1].Frequency = -1

	err := ValidateBarParams(&params)
	assertFieldError(t, err, "modes[1].frequency")
}

func TestValidateBarParamsModeDecayOutOfRange(t *testing.T) {
	params := validTestParams()
	params.Modes[0].DecayMs = DecayMsMax + 1

	err := ValidateBarParams(&params)
	assertFieldError(t, err, "modes[0].decay_ms")
}

func TestValidateBarParamsChebyshevGainOutOfRange(t *testing.T) {
	params := validTestParams()
	params.Chebyshev.HarmonicGains[1] = HarmonicGainMax + 0.1

	err := ValidateBarParams(&params)
	assertFieldError(t, err, "chebyshev.harmonic_gains[1]")
}

func TestValidateBarParamsRejectsNaN(t *testing.T) {
	params := validTestParams()
	params.InputMix = math.NaN()

	err := ValidateBarParams(&params)
	assertFieldError(t, err, "input_mix")
}

func TestBarParamsValidateMethod(t *testing.T) {
	params := validTestParams()
	if err := params.Validate(); err != nil {
		t.Fatalf("expected Validate method to pass, got error: %v", err)
	}
}

func validTestParams() BarParams {
	return BarParams{
		InputMix:        0.472,
		FilterFrequency: 522.9,
		BaseFrequency:   440.0,
		Modes: [NumModes]ModeParams{
			{Amplitude: 0.886, Frequency: 1756.6, DecayMs: 188.2},
			{Amplitude: 1.995, Frequency: 4768.1, DecayMs: 1.603},
			{Amplitude: -0.465, Frequency: 38.24, DecayMs: 5.559},
			{Amplitude: 0.364, Frequency: 32.63, DecayMs: 8.682},
		},
		Chebyshev: ChebyshevParams{
			Enabled:       true,
			HarmonicGains: []float64{1.0, 0.5, 0.3, 0.2},
		},
	}
}

func assertFieldError(t *testing.T, err error, field string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected validation error for %s", field)
	}

	if !strings.Contains(err.Error(), field) {
		t.Fatalf("expected error to mention %q, got %q", field, err.Error())
	}
}
