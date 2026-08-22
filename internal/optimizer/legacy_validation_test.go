package optimizer

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/preset"
	"github.com/cwbudde/glockenspiel/internal/wavio"
	"github.com/cwbudde/glockenspiel/model"
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
	initial.Parameters.Modes[0].DecayMs = clampToRange(initial.Parameters.Modes[0].DecayMs*0.8, model.DecayMsMin, model.DecayMsMax)

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

	assertCloseWithin(t, recovered.InputMix, legacyPreset.Parameters.InputMix, 0.22, "legacy input_mix")
	assertCloseWithin(t, recovered.FilterFrequency, legacyPreset.Parameters.FilterFrequency, 180, "legacy filter_frequency")
	// The amplitude tolerance is an absolute one, so it was rescaled alongside
	// the preset: 0.3 against the old amplitude of 0.886 is the same relative
	// slack as 0.0344 against the rescaled 0.102. Leaving it at 0.3 would have
	// left an assertion that no plausible result could fail.
	assertCloseWithin(t, recovered.Modes[0].Amplitude, legacyPreset.Parameters.Modes[0].Amplitude, 0.0344, "legacy mode0 amplitude")
	// The legacy WAV fit is not uniquely identifiable with the current time-domain
	// objective, so mode 0 can settle into a different but still plausible local
	// minimum while the waveform error improves materially.
	assertCloseWithin(t, recovered.Modes[0].Frequency, legacyPreset.Parameters.Modes[0].Frequency, 300, "legacy mode0 frequency")

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
		DecayMs:       Range{Min: model.DecayMsMin, Max: model.DecayMsMax},
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
