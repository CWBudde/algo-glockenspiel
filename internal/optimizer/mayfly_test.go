package optimizer

import (
	"context"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cwbudde/glockenspiel/internal/preset"
	"github.com/cwbudde/glockenspiel/model"
	"github.com/cwbudde/mayfly"
)

func TestMayflyConfigRejectsUnsupportedVariant(t *testing.T) {
	if _, err := newMayflyConfig("nope", 10, 3, 5); err == nil {
		t.Fatal("expected unsupported variant to fail")
	}
}

func TestBoundsNormalizeDenormalizeRoundTrip(t *testing.T) {
	bounds := Bounds{Ranges: []Range{
		{Min: -2, Max: 2},
		{Min: 10, Max: 20},
		{Min: 5, Max: 5},
	}}
	input := []float64{1.5, 12.5, 5}

	normalized, err := bounds.Normalize(input)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}

	denormalized, err := bounds.Denormalize(normalized)
	if err != nil {
		t.Fatalf("Denormalize failed: %v", err)
	}

	for i := range input {
		if math.Abs(input[i]-denormalized[i]) > 1e-12 {
			t.Fatalf("round-trip mismatch at %d: got %.12f want %.12f", i, denormalized[i], input[i])
		}
	}
}

// TestMayflyOptimizerStartsFromInitialGuess is the regression test for the
// initial guess being discarded: without WithInitialPopulation the run starts
// uniformly at random and --preset and --resume have no effect at all.
func TestMayflyOptimizerStartsFromInitialGuess(t *testing.T) {
	optimum := []float64{12.5, -7.25, 3}
	bounds := Bounds{Ranges: []Range{
		{Min: -1000, Max: 1000},
		{Min: -1000, Max: 1000},
		{Min: -1000, Max: 1000},
	}}
	sphere := func(x []float64) float64 {
		total := 0.0
		for i := range x {
			total += square(x[i] - optimum[i])
		}

		return total
	}

	result, err := (&MayflyOptimizer{Population: 8, Seed: 1}).Optimize(
		context.Background(), sphere, optimum, bounds, OptimizeOptions{MaxIterations: 20},
	)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if result.BestCost > 1e-9 {
		t.Fatalf("starting from the optimum must stay at the optimum: got %g", result.BestCost)
	}

	// Starting one unit away, a seeded population refines the guess. An
	// unseeded one samples a box a thousand times wider and never gets close.
	near := []float64{optimum[0] + 0.5, optimum[1] + 0.5, optimum[2] + 0.5}
	nearCost := sphere(near)

	result, err = (&MayflyOptimizer{Population: 8, Seed: 1}).Optimize(
		context.Background(), sphere, near, bounds, OptimizeOptions{MaxIterations: 60},
	)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if result.BestCost > nearCost*0.5 {
		t.Fatalf("expected the seeded population to improve on the initial guess: initial=%g best=%g",
			nearCost, result.BestCost)
	}
}

func TestMayflyOptimizerReportsTerminationReason(t *testing.T) {
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}}}

	result, err := (&MayflyOptimizer{Population: 4, Seed: 1}).Optimize(
		context.Background(), func(x []float64) float64 { return square(x[0]) },
		[]float64{5}, bounds, OptimizeOptions{MaxIterations: 7},
	)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if result.StopReason != string(mayfly.TerminationMaxIterations) {
		t.Fatalf("unexpected stop reason: %q", result.StopReason)
	}

	if result.Iterations != 7 {
		t.Fatalf("expected the real iteration count, got %d", result.Iterations)
	}

	if result.Converged {
		t.Fatal("exhausting the iteration budget is not convergence")
	}
}

func TestMayflyOptimizerStopsOnCanceledContext(t *testing.T) {
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}}}

	ctx, cancel := context.WithCancel(context.Background())

	// The objective is called from several workers at once, so the trip counter
	// has to be atomic - exactly the contract parallel evaluation imposes.
	var calls atomic.Int64

	result, err := (&MayflyOptimizer{Population: 4, Seed: 1}).Optimize(
		ctx, func(x []float64) float64 {
			if calls.Add(1) > 20 {
				cancel()
			}

			return square(x[0])
		}, []float64{5}, bounds, OptimizeOptions{MaxIterations: 100000},
	)

	cancel()

	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if result.StopReason != "context_canceled" {
		t.Fatalf("unexpected stop reason: %q", result.StopReason)
	}

	if result.Iterations >= 100000 {
		t.Fatalf("expected a truncated run, got %d iterations", result.Iterations)
	}
}

func TestMayflyOptimizerStopsOnTimeBudget(t *testing.T) {
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}}}

	result, err := (&MayflyOptimizer{Population: 4, Seed: 1}).Optimize(
		context.Background(), func(x []float64) float64 { return square(x[0]) },
		[]float64{5}, bounds, OptimizeOptions{MaxIterations: 100000000, TimeBudget: 50 * time.Millisecond},
	)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if result.StopReason != "time_budget" {
		t.Fatalf("unexpected stop reason: %q", result.StopReason)
	}
}

func TestMayflyOptimizerCountsProgressCallbacks(t *testing.T) {
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}}}

	var updates []Progress

	_, err := (&MayflyOptimizer{Population: 4, Seed: 1}).Optimize(
		context.Background(), func(x []float64) float64 { return square(x[0]) },
		[]float64{5}, bounds, OptimizeOptions{
			MaxIterations: 20,
			ReportEvery:   5,
			Report:        func(p Progress) { updates = append(updates, p) },
		},
	)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if len(updates) != 4 {
		t.Fatalf("expected one callback per 5 of 20 iterations, got %d", len(updates))
	}

	for i, update := range updates {
		if update.Iteration != i+1 {
			t.Fatalf("Progress.Iteration must count callbacks: update %d reported %d", i, update.Iteration)
		}
	}
}

func TestMayflyOptimizerRejectsNilObjective(t *testing.T) {
	opt := &MayflyOptimizer{}

	_, err := opt.Optimize(context.Background(), nil, []float64{0.5}, Bounds{Ranges: []Range{{Min: 0, Max: 1}}}, OptimizeOptions{
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

	result, err := opt.Optimize(context.Background(), func(x []float64) float64 {
		return square(x[0]-1.25) + square(x[1]+2.5)
	}, initial, bounds, OptimizeOptions{
		MaxIterations: 80,
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
	}).Optimize(context.Background(), objective.Objective(), initialEncoded, objective.Codec().EncodedBounds(), OptimizeOptions{
		// Bounded by iterations only: pairing a wall-clock budget with a
		// solution-quality assertion makes the test fail on a loaded runner.
		MaxIterations: 40,
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

	// Clone the parameters: BarParams.Modes is a slice, so a plain struct copy
	// would let the perturbations below rewrite the shipped preset that
	// legacyPreset is reused as the reference for.
	initial := *legacyPreset
	initial.Parameters = legacyPreset.Parameters.Clone()
	initial.Parameters.InputMix = clampToRange(initial.Parameters.InputMix+0.18, model.InputMixMin, model.InputMixMax)
	initial.Parameters.FilterFrequency = clampToRange(initial.Parameters.FilterFrequency*1.18, model.FilterFrequencyMinHz, model.FilterFrequencyMaxHz)
	initial.Parameters.Modes[0].Amplitude = clampToRange(initial.Parameters.Modes[0].Amplitude*legacyAmplitudePerturbation, model.AmplitudeMin, model.AmplitudeMax)
	initial.Parameters.Modes[0].Frequency = clampToRange(initial.Parameters.Modes[0].Frequency*0.93, model.FrequencyMinHz, model.FrequencyMaxHz)
	initial.Parameters.Modes[0].DecayMs = clampToRange(initial.Parameters.Modes[0].DecayMs*0.8, model.DecayMsMin, model.DecayMsSearchMax)

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
	}).Optimize(context.Background(), objective.Objective(), initialEncoded, objective.Codec().EncodedBounds(), OptimizeOptions{
		MaxIterations: 25,
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
