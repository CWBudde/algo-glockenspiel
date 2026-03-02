package optimizer

import (
	"math"
	"testing"
	"time"

	"github.com/cwbudde/glockenspiel/internal/model"
	"github.com/cwbudde/glockenspiel/internal/preset"
)

func TestMayflyConfigRejectsUnsupportedVariant(t *testing.T) {
	if _, err := newMayflyConfig("nope", 10, 3, 5); err == nil {
		t.Fatal("expected unsupported variant to fail")
	}
}

func TestNormalizeDenormalizeVectorRoundTrip(t *testing.T) {
	bounds := Bounds{Ranges: []Range{
		{Min: -2, Max: 2},
		{Min: 10, Max: 20},
		{Min: 5, Max: 5},
	}}
	input := []float64{1.5, 12.5, 5}

	normalized := normalizeVector(input, bounds)
	denormalized := denormalizeVector(normalized, bounds)

	for i := range input {
		if math.Abs(input[i]-denormalized[i]) > 1e-12 {
			t.Fatalf("round-trip mismatch at %d: got %.12f want %.12f", i, denormalized[i], input[i])
		}
	}
}

func TestMayflyOptimizerRejectsNilObjective(t *testing.T) {
	opt := &MayflyOptimizer{}
	_, err := opt.Optimize(nil, []float64{0.5}, Bounds{Ranges: []Range{{Min: 0, Max: 1}}}, OptimizeOptions{
		MaxIterations: 2,
		TimeBudget:    time.Second,
	})
	if err == nil {
		t.Fatal("expected nil objective to fail")
	}
}

func TestMayflyOptimizerFindsKnownMinimum(t *testing.T) {
	opt := &MayflyOptimizer{
		Variant:    "desma",
		Population: 8,
		Seed:       1,
	}
	initial := []float64{5, 5}
	bounds := Bounds{Ranges: []Range{
		{Min: -10, Max: 10},
		{Min: -10, Max: 10},
	}}

	result, err := opt.Optimize(func(x []float64) float64 {
		return square(x[0]-1.25) + square(x[1]+2.5)
	}, initial, bounds, OptimizeOptions{
		MaxIterations: 80,
		TimeBudget:    2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if result.BestCost > 1e-2 {
		t.Fatalf("unexpected best cost: got %g", result.BestCost)
	}

	if math.Abs(result.BestParams[0]-1.25) > 0.2 || math.Abs(result.BestParams[1]+2.5) > 0.2 {
		t.Fatalf("unexpected optimum: got %v", result.BestParams)
	}
}

func TestMayflyOptimizerImprovesSyntheticReference(t *testing.T) {
	template := loadMinimalPreset(t)
	target := *template
	target.Parameters.InputMix = 0.08
	target.Parameters.FilterFrequency = 1200
	target.Parameters.Modes[0].Amplitude = 0.9
	target.Parameters.Modes[0].Frequency = 470
	target.Parameters.Modes[0].DecayMs = 140

	reference := renderNote(t, &target, 44100, 69, 100, 0.08)
	reference = addDeterministicNoise(reference, 1e-4)

	initial := *template
	initial.Parameters.InputMix = 0.2
	initial.Parameters.FilterFrequency = 900
	initial.Parameters.Modes[0].Amplitude = 0.7
	initial.Parameters.Modes[0].Frequency = 430
	initial.Parameters.Modes[0].DecayMs = 90

	bounds := narrowBoundsAroundTarget(&target.Parameters)
	objective, err := NewObjectiveFunctionWithBounds(reference, &initial, 44100, 69, 100, MetricRMS, bounds)
	if err != nil {
		t.Fatalf("NewObjectiveFunctionWithBounds failed: %v", err)
	}

	initialEncoded, err := objective.Codec().EncodeParams(&initial.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}
	initialCost := objective.Evaluate(initialEncoded)

	result, err := (&MayflyOptimizer{
		Variant:    "desma",
		Population: 8,
		Seed:       1,
	}).Optimize(objective.Objective(), initialEncoded, objective.Codec().EncodedBounds(), OptimizeOptions{
		MaxIterations: 40,
		TimeBudget:    2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if !(result.BestCost < initialCost) {
		t.Fatalf("expected optimization to improve cost: initial=%g best=%g", initialCost, result.BestCost)
	}
	if !(result.BestCost <= initialCost*0.98) {
		t.Fatalf("expected material cost improvement: initial=%g best=%g", initialCost, result.BestCost)
	}
}

func TestMayflyOptimizerImprovesLegacyReference(t *testing.T) {
	legacyPreset := loadDefaultPreset(t)
	reference, sampleRate := loadLegacyReferenceWAV(t)

	initial := *legacyPreset
	initial.Parameters.InputMix = clampToRange(initial.Parameters.InputMix+0.18, model.InputMixMin, model.InputMixMax)
	initial.Parameters.FilterFrequency = clampToRange(initial.Parameters.FilterFrequency*1.18, model.FilterFrequencyMinHz, model.FilterFrequencyMaxHz)
	initial.Parameters.Modes[0].Amplitude = clampToRange(initial.Parameters.Modes[0].Amplitude-0.22, model.AmplitudeMin, model.AmplitudeMax)
	initial.Parameters.Modes[0].Frequency = clampToRange(initial.Parameters.Modes[0].Frequency*0.93, model.FrequencyMinHz, model.FrequencyMaxHz)
	initial.Parameters.Modes[0].DecayMs = clampToRange(initial.Parameters.Modes[0].DecayMs*0.8, model.DecayMsMin, model.DecayMsMax)

	objective, err := NewObjectiveFunctionWithBounds(reference, &initial, sampleRate, 69, 100, MetricRMS, legacyValidationBounds(&legacyPreset.Parameters))
	if err != nil {
		t.Fatalf("NewObjectiveFunctionWithBounds failed: %v", err)
	}

	initialEncoded, err := objective.Codec().EncodeParams(&initial.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}
	initialCost := objective.Evaluate(initialEncoded)

	result, err := (&MayflyOptimizer{
		Variant:    "desma",
		Population: 10,
		Seed:       1,
	}).Optimize(objective.Objective(), initialEncoded, objective.Codec().EncodedBounds(), OptimizeOptions{
		MaxIterations: 25,
		TimeBudget:    2 * time.Second,
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
