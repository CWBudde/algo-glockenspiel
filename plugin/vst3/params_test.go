package vst3

import (
	"testing"

	"github.com/cwbudde/glockenspiel/internal/model"
)

func TestParameterSpecsHaveStableUniqueIDs(t *testing.T) {
	specs := ParameterSpecs()
	if len(specs) != 20 {
		t.Fatalf("unexpected parameter count: got %d want 20", len(specs))
	}

	seenIDs := make(map[ParameterID]struct{}, len(specs))
	seenKeys := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		if _, ok := seenIDs[spec.ID]; ok {
			t.Fatalf("duplicate parameter id: %d", spec.ID)
		}
		seenIDs[spec.ID] = struct{}{}

		if _, ok := seenKeys[spec.Key]; ok {
			t.Fatalf("duplicate parameter key: %q", spec.Key)
		}
		seenKeys[spec.Key] = struct{}{}

		if spec.Min > spec.Max {
			t.Fatalf("invalid range for %q: min=%f max=%f", spec.Key, spec.Min, spec.Max)
		}
		if spec.Default < spec.Min || spec.Default > spec.Max {
			t.Fatalf("default out of range for %q: default=%f", spec.Key, spec.Default)
		}
	}
}

func TestSnapshotRoundTripBarParams(t *testing.T) {
	input := model.BarParams{
		InputMix:        0.25,
		FilterFrequency: 5400,
		BaseFrequency:   440,
		Modes: [model.NumModes]model.ModeParams{
			{Amplitude: 1.0, Frequency: 440, DecayMs: 120},
			{Amplitude: 0.7, Frequency: 1180, DecayMs: 90},
			{Amplitude: 0.3, Frequency: 2010, DecayMs: 70},
			{Amplitude: 0.1, Frequency: 3180, DecayMs: 40},
		},
		Chebyshev: model.ChebyshevParams{
			Enabled:       true,
			HarmonicGains: []float64{1.0, 0.3, 0.15, 0.05},
		},
	}

	snapshot := SnapshotFromBarParams(&input)
	output := snapshot.ToBarParams()

	if output.InputMix != input.InputMix {
		t.Fatalf("input mix mismatch: got %f want %f", output.InputMix, input.InputMix)
	}
	if output.FilterFrequency != input.FilterFrequency {
		t.Fatalf("filter frequency mismatch: got %f want %f", output.FilterFrequency, input.FilterFrequency)
	}
	if output.BaseFrequency != input.BaseFrequency {
		t.Fatalf("base frequency mismatch: got %f want %f", output.BaseFrequency, input.BaseFrequency)
	}
	if output.Chebyshev.Enabled != input.Chebyshev.Enabled {
		t.Fatalf("chebyshev enabled mismatch: got %v want %v", output.Chebyshev.Enabled, input.Chebyshev.Enabled)
	}

	for i := 0; i < model.NumModes; i++ {
		if output.Chebyshev.HarmonicGains[i] != input.Chebyshev.HarmonicGains[i] {
			t.Fatalf("harmonic gain mismatch at %d: got %f want %f", i, output.Chebyshev.HarmonicGains[i], input.Chebyshev.HarmonicGains[i])
		}
		if output.Modes[i] != input.Modes[i] {
			t.Fatalf("mode mismatch at %d: got %+v want %+v", i, output.Modes[i], input.Modes[i])
		}
	}
}
