package model

import (
	"math"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/oscbank"
)

func barParamsWithModes(modes []ModeParams) BarParams {
	return BarParams{
		InputMix:        0,
		FilterFrequency: 8000,
		BaseFrequency:   440,
		Modes:           modes,
	}
}

func renderBar(t *testing.T, params BarParams, samples int) []float32 {
	t.Helper()

	bar, err := NewBar(&params, 44100)
	if err != nil {
		t.Fatalf("NewBar: %v", err)
	}

	out := bar.Synthesize(100, samples)

	rendered := make([]float32, len(out))
	copy(rendered, out)

	return rendered
}

func peakOf(samples []float32) float64 {
	peak := 0.0
	for _, v := range samples {
		peak = math.Max(peak, math.Abs(float64(v)))
	}

	return peak
}

func TestBarModeCountIsRuntimeConfigurable(t *testing.T) {
	for _, modeCount := range []int{0, 1, 2, 4, 9, 33} {
		modes := make([]ModeParams, modeCount)
		for i := range modes {
			modes[i] = ModeParams{Amplitude: 0.5, Frequency: 440 * float64(i+1), DecayMs: 80}
		}

		params := barParamsWithModes(modes)

		bar, err := NewBar(&params, 44100)
		if err != nil {
			t.Fatalf("%d modes: NewBar: %v", modeCount, err)
		}

		if bar.NumModes() != modeCount {
			t.Fatalf("NumModes = %d, want %d", bar.NumModes(), modeCount)
		}

		rendered := renderBar(t, params, 2048)

		if modeCount == 0 {
			if peak := peakOf(rendered); peak != 0 {
				t.Fatalf("a bar with no modes should be silent, peak %g", peak)
			}

			continue
		}

		if peak := peakOf(rendered); peak <= 0 {
			t.Fatalf("%d modes produced silence", modeCount)
		}
	}
}

func TestBarHarmonicsRideOnTheOscillators(t *testing.T) {
	fundamental := barParamsWithModes([]ModeParams{
		{Amplitude: 0.8, Frequency: 1000, DecayMs: 120},
	})

	withPartials := barParamsWithModes([]ModeParams{
		{Amplitude: 0.8, Frequency: 1000, DecayMs: 120, Harmonics: []float64{1, 0.5, 0.25}},
	})

	bar, err := NewBar(&withPartials, 44100)
	if err != nil {
		t.Fatalf("NewBar: %v", err)
	}

	if bar.NumHarmonics() != 3 {
		t.Fatalf("NumHarmonics = %d, want 3", bar.NumHarmonics())
	}

	plain := renderBar(t, fundamental, 4096)
	rich := renderBar(t, withPartials, 4096)

	// The first harmonic gain is 1, so the fundamental is untouched and the
	// extra partials can only add energy.
	if peakOf(rich) <= peakOf(plain) {
		t.Fatalf("partials added no energy: rich peak %g, plain peak %g", peakOf(rich), peakOf(plain))
	}

	identical := true

	for i := range plain {
		if plain[i] != rich[i] {
			identical = false
			break
		}
	}

	if identical {
		t.Fatal("harmonic partials did not change the output")
	}
}

func TestBarHarmonicGainZeroMutesThePartial(t *testing.T) {
	single := barParamsWithModes([]ModeParams{
		{Amplitude: 0.8, Frequency: 1000, DecayMs: 120},
	})

	explicit := barParamsWithModes([]ModeParams{
		{Amplitude: 0.8, Frequency: 1000, DecayMs: 120, Harmonics: []float64{1, 0, 0}},
	})

	plain := renderBar(t, single, 2048)
	padded := renderBar(t, explicit, 2048)

	for i := range plain {
		if plain[i] != padded[i] {
			t.Fatalf("sample %d: muted partials changed the output: %v vs %v", i, padded[i], plain[i])
		}
	}
}

func TestChebyshevStageSelectsWhereTheShaperSits(t *testing.T) {
	modes := []ModeParams{
		{Amplitude: 0.9, Frequency: 1200, DecayMs: 150},
		{Amplitude: 0.4, Frequency: 3300, DecayMs: 40},
	}

	excitation := barParamsWithModes(modes)
	excitation.Chebyshev = ChebyshevParams{
		Enabled:       true,
		HarmonicGains: []float64{1, 0.5, 0.3, 0.2},
	}

	output := barParamsWithModes(modes)
	output.Chebyshev = ChebyshevParams{
		Enabled:       true,
		Stage:         ChebyshevStageOutput,
		HarmonicGains: []float64{1, 0.5, 0.3, 0.2},
	}

	if excitation.Chebyshev.ResolvedStage() != ChebyshevStageExcitation {
		t.Fatal("an unset stage must resolve to the v1 excitation placement")
	}

	before := renderBar(t, excitation, 4096)
	after := renderBar(t, output, 4096)

	if peakOf(after) <= 0 {
		t.Fatal("the output-stage shaper produced silence")
	}

	identical := true

	for i := range before {
		if before[i] != after[i] {
			identical = false
			break
		}
	}

	if identical {
		t.Fatal("shaping before and after the oscillators produced the same signal")
	}
}

func TestBarRejectsOutOfRangeHarmonicGain(t *testing.T) {
	params := barParamsWithModes([]ModeParams{
		{Amplitude: 0.5, Frequency: 1000, DecayMs: 50, Harmonics: []float64{1, HarmonicGainMax + 1}},
	})

	if _, err := NewBar(&params, 44100); err == nil {
		t.Fatal("expected a validation error")
	}
}

func TestBarParamsCloneIsDeep(t *testing.T) {
	original := barParamsWithModes([]ModeParams{
		{Amplitude: 0.5, Frequency: 1000, DecayMs: 50, Harmonics: []float64{1, 0.5}},
	})
	original.Chebyshev = ChebyshevParams{Enabled: true, HarmonicGains: []float64{1, 0.5}}

	clone := original.Clone()
	clone.Modes[0].Frequency = 2000
	clone.Modes[0].Harmonics[0] = 0
	clone.Chebyshev.HarmonicGains[0] = 0

	if original.Modes[0].Frequency != 1000 {
		t.Fatal("clone shares the modes slice")
	}

	if original.Modes[0].Harmonics[0] != 1 {
		t.Fatal("clone shares the per-mode harmonics slice")
	}

	if original.Chebyshev.HarmonicGains[0] != 1 {
		t.Fatal("clone shares the Chebyshev gains slice")
	}
}

func TestUpdateParamsDoesNotAliasCallerSlices(t *testing.T) {
	params := barParamsWithModes([]ModeParams{
		{Amplitude: 0.5, Frequency: 1000, DecayMs: 50},
	})

	bar, err := NewBar(&params, 44100)
	if err != nil {
		t.Fatalf("NewBar: %v", err)
	}

	before := make([]float32, 512)
	copy(before, bar.Synthesize(100, 512))

	// Mutating the caller's slice after the fact must not reach the bar.
	params.Modes[0].Frequency = 9000

	bar.Reset()

	after := bar.Synthesize(100, 512)
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("sample %d changed after the caller mutated its own params", i)
		}
	}
}

// BenchmarkOscBank4x4Block512 renders the bar's own working shape -- four
// oscillators with four harmonics each, sixteen rotors -- through the bank, so
// the model package keeps a rendering-path benchmark of its own now that the
// fixed four-mode oscillator it was measured against is gone.
func BenchmarkOscBank4x4Block512(b *testing.B) {
	bank := oscbank.New(44100)

	oscillators := make([]oscbank.Oscillator, 4)
	for i := range oscillators {
		oscillators[i] = oscbank.Oscillator{
			Amplitude: 1,
			Frequency: 1000 * float64(i+1),
			DecayMs:   100 / float64(i+1),
			Harmonics: []float64{1, 0.5, 0.3, 0.2},
		}
	}

	if err := bank.SetOscillators(oscillators); err != nil {
		b.Fatalf("SetOscillators: %v", err)
	}

	input := make([]float32, 512)
	input[0] = 1
	output := make([]float32, 512)

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		bank.ProcessBlock(input, output)
	}
}
