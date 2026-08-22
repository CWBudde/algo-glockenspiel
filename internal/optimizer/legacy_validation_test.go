package optimizer

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
	"github.com/cwbudde/algo-glockenspiel/model"
)

func TestOptimizationImprovesFitAgainstLegacyReference(t *testing.T) {
	legacyPreset := loadDefaultPreset(t)
	reference, sampleRate := loadLegacyReferenceWAV(t)

	// Clone the parameters rather than relying on the struct copy. BarParams
	// carries Modes as a slice, so a plain copy leaves both presets sharing one
	// backing array and the perturbations below would rewrite the shipped
	// values they are meant to be perturbed away from -- which would leave the
	// recovery assertions comparing the result against the perturbed target
	// instead of the shipped one, i.e. asserting nothing.
	initial := *legacyPreset
	initial.Parameters = legacyPreset.Parameters.Clone()
	initial.Parameters.InputMix = clampToRange(initial.Parameters.InputMix+0.18, model.InputMixMin, model.InputMixMax)
	initial.Parameters.FilterFrequency = clampToRange(initial.Parameters.FilterFrequency*1.18, model.FilterFrequencyMinHz, model.FilterFrequencyMaxHz)
	initial.Parameters.Modes[0].Amplitude = clampToRange(initial.Parameters.Modes[0].Amplitude*legacyAmplitudePerturbation, model.AmplitudeMin, model.AmplitudeMax)
	initial.Parameters.Modes[0].Frequency = clampToRange(initial.Parameters.Modes[0].Frequency*0.93, model.FrequencyMinHz, model.FrequencyMaxHz)
	initial.Parameters.Modes[0].DecayMs = clampToRange(initial.Parameters.Modes[0].DecayMs*0.8, model.DecayMsMin, model.DecayMsSearchMax)

	bounds := legacyValidationBounds(&legacyPreset.Parameters)

	objective, err := NewObjectiveFunctionWithBounds(reference, &initial, sampleRate, 69, 100, MetricRMS, bounds)
	if err != nil {
		t.Fatalf("NewObjectiveFunctionWithBounds failed: %v", err)
	}

	initialEncoded, err := objective.Codec().EncodeParams(&initial.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	initialCost := objective.Evaluate(initialEncoded)

	result, err := (&SimpleOptimizer{
		AbsoluteTolerance: 1e-10,
		RelativeTolerance: 1e-10,
		StallIterations:   50,
	}).Optimize(context.Background(), objective.Objective(), initialEncoded, objective.Codec().EncodedBounds(), OptimizeOptions{
		// Bounded by iterations only: pairing a wall-clock budget with a
		// solution-quality assertion makes the test fail on a loaded runner.
		MaxIterations: 120,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if !(result.BestCost < initialCost) {
		t.Fatalf("expected optimization to improve cost: initial=%g best=%g", initialCost, result.BestCost)
	}

	recovered, err := objective.Codec().DecodeParams(result.BestParams)
	if err != nil {
		t.Fatalf("DecodeParams failed: %v", err)
	}

	// How far back the optimizer walked, measured in the objective rather than
	// parameter by parameter.
	//
	// This used to assert recovery on input_mix, filter_frequency and mode 0's
	// amplitude and frequency individually, and it cannot any more. Those
	// assertions were written against a preset whose output was almost entirely
	// the Chebyshev shaper's DC offset, so the modes carried nothing and the
	// cost surface was nearly flat: perturbing filter_frequency by 18% barely
	// moved the score, and three of the four tolerances were wider than the
	// perturbations they were checking, which made them unfailable.
	//
	// With a preset whose modes carry the signal, the same surface is savagely
	// sharp in mode frequency -- a 2% error on a 1757 Hz partial is twenty
	// cycles of phase drift across the reference -- and a local search cannot
	// climb back into it. Measured from perturbations of 18%, 10%, 5% and 2%,
	// SimpleOptimizer settles at a cost near 0.19 every time and lets mode 0's
	// frequency wander as far as 3218 Hz, while more iterations change nothing:
	// it is a local minimum, not a budget. Fitting mode frequencies is what the
	// global optimizer is for, and TestMayflyOptimizerImprovesLegacyReference
	// is where that belongs.
	//
	// What is left is the well-posed form of the same claim: the optimizer has
	// to close a real share of the gap between where it started and the preset
	// it was perturbed away from. Measured at 33.5%; the bound is set at 20% so
	// that a regression has to be substantial rather than incidental, and it is
	// not vacuous -- a run that merely improved the cost a little would fail it.
	targetEncoded, err := objective.Codec().EncodeParams(&legacyPreset.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams for the target failed: %v", err)
	}

	targetCost := objective.Evaluate(targetEncoded)

	gap := initialCost - targetCost
	if gap <= 0 {
		t.Fatalf("the perturbed preset scores no worse than the shipped one "+
			"(perturbed=%g shipped=%g), so there is nothing to recover", initialCost, targetCost)
	}

	if closed := (initialCost - result.BestCost) / gap; closed < 0.20 {
		t.Errorf("the optimizer closed %.1f%% of the cost gap to the shipped preset "+
			"(perturbed=%g best=%g shipped=%g), want at least 20%%",
			closed*100, initialCost, result.BestCost, targetCost)
	}

	rendered := renderNote(t, &preset.Preset{
		Version:    legacyPreset.Version,
		Name:       legacyPreset.Name,
		Note:       legacyPreset.Note,
		Parameters: *recovered,
	}, sampleRate, 69, 100, float64(len(reference))/float64(sampleRate))
	initialRendered := renderNote(t, &initial, sampleRate, 69, 100, float64(len(reference))/float64(sampleRate))
	initialRMS := ComputeRMSError(initialRendered, reference)

	finalRMS := ComputeRMSError(rendered, reference)
	if !(finalRMS < initialRMS) {
		t.Fatalf("expected rendered RMS to improve: initial=%g final=%g", initialRMS, finalRMS)
	}
}

// legacyAmplitudePerturbation is how far mode 0's amplitude is pushed off the
// shipped value before the optimizer is asked to walk it back.
//
// It is a ratio rather than an absolute offset on purpose: a perturbation has
// to stay a perturbation when the preset's absolute level changes. The shipped
// preset used to render at +15.8 dBFS and was rescaled to -3 dBFS, which
// divided every mode amplitude by about 8.7. The previous absolute -0.22 was a
// 25% nudge against mode 0's old amplitude of 0.886; against the rescaled 0.102
// the same offset would have flipped the sign and landed further from the
// target than the search space is wide, which tests a completely different
// thing. This factor reproduces the original 25% nudge at any level.
const legacyAmplitudePerturbation = 0.75

func loadDefaultPreset(t *testing.T) *preset.Preset {
	t.Helper()

	p, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		t.Fatalf("load default preset: %v", err)
	}

	return p
}

func loadLegacyReferenceWAV(t *testing.T) ([]float32, int) {
	t.Helper()

	samples, sampleRate, err := loadLegacyReferenceForBenchmark()
	if err != nil {
		t.Fatalf("load legacy reference: %v", err)
	}

	return samples, sampleRate
}

func legacyValidationBounds(target *model.BarParams) ParamBounds {
	return ParamBounds{
		InputMix:      Range{Min: math.Max(model.InputMixMin, target.InputMix-0.35), Max: math.Min(model.InputMixMax, target.InputMix+0.35)},
		FilterFreq:    Range{Min: math.Max(model.FilterFrequencyMinHz, target.FilterFrequency*0.6), Max: math.Min(model.FilterFrequencyMaxHz, target.FilterFrequency*1.4)},
		BaseFrequency: Range{Min: target.BaseFrequency, Max: target.BaseFrequency},
		Amplitude:     Range{Min: model.AmplitudeMin, Max: model.AmplitudeMax},
		FrequencyMult: Range{Min: 0.05, Max: 12},
		DecayMs:       Range{Min: model.DecayMsMin, Max: model.DecayMsSearchMax},
		HarmonicGain:  Range{Min: model.HarmonicGainMin, Max: model.HarmonicGainMax},
	}
}

func clampToRange(value, low, high float64) float64 {
	if value < low {
		return low
	}

	if value > high {
		return high
	}

	return value
}

func TestLegacyReferenceFixtureLoads(t *testing.T) {
	samples, sampleRate := loadLegacyReferenceWAV(t)
	if len(samples) == 0 {
		t.Fatal("expected legacy reference to contain samples")
	}

	if sampleRate <= 0 {
		t.Fatalf("expected positive sample rate, got %d", sampleRate)
	}
}

func BenchmarkLegacyObjectiveEvaluate(b *testing.B) {
	legacyPreset, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		b.Fatalf("load default preset: %v", err)
	}

	reference, sampleRate, err := loadLegacyReferenceForBenchmark()
	if err != nil {
		b.Fatalf("load legacy reference: %v", err)
	}

	objective, err := NewObjectiveFunctionWithBounds(reference, legacyPreset, sampleRate, 69, 100, MetricRMS, legacyValidationBounds(&legacyPreset.Parameters))
	if err != nil {
		b.Fatalf("NewObjectiveFunctionWithBounds failed: %v", err)
	}

	encoded, err := objective.Codec().EncodeParams(&legacyPreset.Parameters)
	if err != nil {
		b.Fatalf("EncodeParams failed: %v", err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = objective.Evaluate(encoded)
	}
}

// loadLegacyReferenceForBenchmark reads the pinned legacy render.
//
// It goes through internal/wavio rather than driving go-audio directly, so the
// regression suite decodes its reference with exactly the scaling `fit` applies
// to a user's reference. This file used to carry its own copy of that decode;
// a private copy can drift in bit-depth scaling or channel stride and quietly
// move the cost surface these tests assert on, with nothing failing to say so.
func loadLegacyReferenceForBenchmark() ([]float32, int, error) {
	return wavio.LoadMono(filepath.FromSlash("../../testdata/reference/legacy_synth_a4.wav"))
}
