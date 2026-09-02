package optimizer

import (
	"context"
	"math"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/model"
	"github.com/cwbudde/mayfly"
)

func TestMayflyConfigRejectsUnsupportedVariant(t *testing.T) {
	if _, err := newMayflyConfig(ResolvedMayfly{Variant: "nope"}, 10, 3, 5, nil); err == nil {
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

func TestMayflyOptimizerReportsAcrossShortRounds(t *testing.T) {
	// Twelve rounds of eight or nine iterations against a cadence of ten. The
	// per-round counter never reaches ten, so gating on it reports nothing at
	// all for the whole fit -- which is exactly what a browser watching the
	// cost curve of a scheduled mayfly run used to see.
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}}}

	var updates []Progress

	epochs, restarts := 4, 8

	_, err := (&MayflyOptimizer{
		Population: 4,
		Seed:       1,
		Tuning: &MayflyTuning{
			Schedule: &MayflySchedule{Epochs: &epochs, Restarts: &restarts},
		},
	}).Optimize(
		context.Background(), func(x []float64) float64 { return square(x[0]) },
		[]float64{5}, bounds, OptimizeOptions{
			MaxIterations: 100,
			ReportEvery:   10,
			Report:        func(p Progress) { updates = append(updates, p) },
		},
	)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if len(updates) == 0 {
		t.Fatal("a scheduled run reported no progress at all")
	}

	for i, update := range updates {
		if update.OptimizerIterations%10 != 0 {
			t.Fatalf("update %d reported at iteration %d, which is not a multiple of the cadence",
				i, update.OptimizerIterations)
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

	// Measure the rendered result under the same alignment the objective used.
	// A bare ComputeRMSError compares sample by sample, so it scores a fit that
	// is near-perfect but shifted by a few samples as a large regression -- and
	// an onset-aligning objective is free to find exactly that. This assertion
	// used to pass only because the trajectory happened not to land on a
	// shifted solution; measured across seeds 1, 2 and 3 the aligned error
	// improves by about 88% while the unaligned one swings from -81% to +41%.
	plan := objective.Alignment()
	initialRMS := plan.RMSError(initialRendered, reference)

	finalRMS := plan.RMSError(rendered, reference)
	if !(finalRMS < initialRMS) {
		t.Fatalf("expected rendered RMS to improve: initial=%g final=%g", initialRMS, finalRMS)
	}
}

// TestMayflyZeroSeedIsReportedAndReproducible pins the difference between "pick
// a seed" and "be unreproducible". A zero seed used to leave cfg.Rand nil, so
// the library chose a seed, reported it in a field this wrapper discarded, and
// the run could never be repeated. Resolving it here makes the same run
// repeatable by feeding the reported seed back in.
func TestMayflyZeroSeedIsReportedAndReproducible(t *testing.T) {
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}, {Min: -10, Max: 10}}}
	initial := []float64{5, 5}
	objective := func(x []float64) float64 { return square(x[0]-1.25) + square(x[1]+2.5) }

	run := func(seed int64) (int64, float64) {
		t.Helper()

		var resolved ResolvedMayfly

		opt := &MayflyOptimizer{
			Variant:    "desma",
			Population: 6,
			Seed:       seed,
			MaxWorkers: 1,
			OnResolve:  func(r ResolvedMayfly) { resolved = r },
		}

		result, err := opt.Optimize(context.Background(), objective, initial, bounds, OptimizeOptions{
			MaxIterations: 20,
		})
		if err != nil {
			t.Fatalf("Optimize failed: %v", err)
		}

		if resolved.Seed == 0 {
			t.Fatal("expected a non-zero resolved seed to be reported")
		}

		if resolved.Variant != "desma" {
			t.Fatalf("unexpected resolved variant: got %q", resolved.Variant)
		}

		return resolved.Seed, result.BestCost
	}

	chosen, first := run(0)

	replayed, second := run(chosen)
	if replayed != chosen {
		t.Fatalf("explicit seed was not honoured: got %d want %d", replayed, chosen)
	}

	if first != second {
		t.Fatalf("replaying the reported seed changed the result: got %g want %g", second, first)
	}
}

// TestMayflyValidateChecksTheWholeConfiguration guards the property the HTTP and
// WASM front ends rely on: a request that cannot run is refused before it books
// the single fit slot. Validate now builds the real configuration, so it
// catches far more than an unknown variant name.
func TestMayflyValidateChecksTheWholeConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		opt     MayflyOptimizer
		iters   int
		wantErr bool
	}{
		{name: "default is valid", opt: MayflyOptimizer{}, iters: 100},
		{name: "explicit variant is valid", opt: MayflyOptimizer{Variant: "gsasma"}, iters: 100},
		{name: "alias is valid", opt: MayflyOptimizer{Variant: "olce-ma"}, iters: 100},
		{name: "unknown variant", opt: MayflyOptimizer{Variant: "nope"}, iters: 100, wantErr: true},
		// A Population below two is not an error here: population() normalises
		// it to the default. The NPop guard in validateMayflyConfig covers the
		// path that can genuinely set it, which is the tuning document.
		{name: "population below two is normalised", opt: MayflyOptimizer{Population: 1}, iters: 100},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.opt.Validate(test.iters)
			if test.wantErr && err == nil {
				t.Fatal("expected an error")
			}

			if !test.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestMayflyUnknownVariantListsTheAlternativesInOrder pins the sorted list.
// ListVariants ranges over a map, so without sorting the message differs
// between runs and cannot be asserted on or usefully read.
func TestMayflyUnknownVariantListsTheAlternativesInOrder(t *testing.T) {
	_, err := resolveVariant("nope")
	if err == nil {
		t.Fatal("expected an error")
	}

	first := err.Error()

	for range 20 {
		_, err = resolveVariant("nope")
		if err.Error() != first {
			t.Fatalf("variant list is not stable:\n%s\n%s", first, err.Error())
		}
	}

	if !strings.Contains(first, "aoblmoa, desma, eobbma, gsasma, hmma, ma, mpma, olce") {
		t.Fatalf("expected a sorted variant list, got %q", first)
	}
}

// TestTuningZeroValueIsTodaysBehaviour is the invariant the whole tuning
// surface rests on: a run that asks for nothing must be configured exactly as
// it was before the document existed.
//
// It compares configurations rather than outcomes deliberately. Comparing two
// fits would be slow, would depend on the objective, and would fail for
// reasons that have nothing to do with the document.
func TestTuningZeroValueIsTodaysBehaviour(t *testing.T) {
	variants := mayfly.ListVariants()
	sort.Strings(variants)

	for _, variant := range variants {
		t.Run(variant, func(t *testing.T) {
			resolved := ResolvedMayfly{Variant: variant}

			absent, err := newMayflyConfig(resolved, 10, 3, 50, nil)
			if err != nil {
				t.Fatalf("newMayflyConfig with no document failed: %v", err)
			}

			empty, err := newMayflyConfig(resolved, 10, 3, 50, &MayflyTuning{})
			if err != nil {
				t.Fatalf("newMayflyConfig with an empty document failed: %v", err)
			}

			// ObjectiveFunc and Rand are not set at this point, and neither is
			// comparable anyway.
			if !reflect.DeepEqual(absent, empty) {
				t.Fatalf("an empty tuning document changed the configuration:\n absent=%+v\n  empty=%+v", absent, empty)
			}
		})
	}
}

// TestMayflyTuningOverridesTheVariantDefault checks the precedence the ordering
// in newMayflyConfig promises: the document is applied last and wins.
func TestMayflyTuningOverridesTheVariantDefault(t *testing.T) {
	rate := 0.5
	tuning := &MayflyTuning{CoolingRate: &rate}

	cfg, err := newMayflyConfig(ResolvedMayfly{Variant: "gsasma"}, 10, 3, 50, tuning)
	if err != nil {
		t.Fatalf("newMayflyConfig failed: %v", err)
	}

	if cfg.CoolingRate != rate {
		t.Fatalf("tuning did not win: got %v want %v", cfg.CoolingRate, rate)
	}

	// A knob belonging to another dialect is refused rather than silently
	// written, because mayfly ignores the fields of variants it is not running.
	elite := 3
	if _, err := newMayflyConfig(ResolvedMayfly{Variant: "gsasma"}, 10, 3, 50,
		&MayflyTuning{EliteCount: &elite}); err == nil {
		t.Fatal("expected a DESMA knob to be refused under GSASMA")
	}
}

// TestMayflyPresetSelectsADialect covers the preset path, including the two
// things about it that surprise: it chooses a dialect, so it cannot be combined
// with an explicit variant; and it does not choose the size of the run.
func TestMayflyPresetSelectsADialect(t *testing.T) {
	t.Run("a preset picks the dialect and its knobs", func(t *testing.T) {
		resolved, err := (&MayflyOptimizer{Preset: "highly_multimodal"}).resolve()
		if err != nil {
			t.Fatalf("resolve failed: %v", err)
		}

		cfg, err := newMayflyConfig(resolved, 10, 3, 50, nil)
		if err != nil {
			t.Fatalf("newMayflyConfig failed: %v", err)
		}

		if !cfg.UseOLCE {
			t.Fatal("expected highly_multimodal to select OLCE")
		}
	})

	t.Run("the run's budget wins over the preset's", func(t *testing.T) {
		resolved, err := (&MayflyOptimizer{Preset: "high_dimensional"}).resolve()
		if err != nil {
			t.Fatalf("resolve failed: %v", err)
		}

		cfg, err := newMayflyConfig(resolved, 10, 3, 50, nil)
		if err != nil {
			t.Fatalf("newMayflyConfig failed: %v", err)
		}

		if cfg.MaxIterations != 50 || cfg.NPop != 10 {
			t.Fatalf("preset overrode the run's budget: iters=%d pop=%d", cfg.MaxIterations, cfg.NPop)
		}
	})

	t.Run("a preset and a variant together are refused", func(t *testing.T) {
		if _, err := (&MayflyOptimizer{Preset: "multimodal", Variant: "gsasma"}).resolve(); err == nil {
			t.Fatal("expected the combination to be refused")
		}
	})

	t.Run("an unknown preset lists the alternatives in order", func(t *testing.T) {
		err := (&MayflyOptimizer{Preset: "nope"}).Validate(50)
		if err == nil {
			t.Fatal("expected an error")
		}

		if !strings.Contains(err.Error(), "deceptive, fast_convergence") {
			t.Fatalf("expected a sorted preset list, got %q", err)
		}
	})
}

// TestMayflyTuningCanNameTheVariant covers a document standing on its own, and
// the rule that an explicitly configured variant still wins over it.
func TestMayflyTuningCanNameTheVariant(t *testing.T) {
	named := "mpma"

	resolved, err := (&MayflyOptimizer{Tuning: &MayflyTuning{Variant: &named}}).resolve()
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	if resolved.Variant != "mpma" {
		t.Fatalf("document variant ignored: got %q", resolved.Variant)
	}

	resolved, err = (&MayflyOptimizer{Variant: "gsasma", Tuning: &MayflyTuning{Variant: &named}}).resolve()
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	if resolved.Variant != "gsasma" {
		t.Fatalf("an explicit variant must win over the document: got %q", resolved.Variant)
	}
}

// TestMayflyConvergenceStopsEarly is the first test to exercise Result
// .Converged. The wrapper has always read TerminationTargetCost and
// TerminationStagnation back out of the library, but never set
// Config.Convergence, so neither reason could ever fire and Converged was
// permanently false.
func TestMayflyConvergenceStopsEarly(t *testing.T) {
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}, {Min: -10, Max: 10}}}
	initial := []float64{5, 5}
	objective := func(x []float64) float64 { return square(x[0]-1.25) + square(x[1]+2.5) }

	run := func(t *testing.T, tuning *MayflyTuning) *Result {
		t.Helper()

		result, err := (&MayflyOptimizer{
			Variant: "desma", Population: 8, Seed: 1, MaxWorkers: 1, Tuning: tuning,
		}).Optimize(context.Background(), objective, initial, bounds, OptimizeOptions{
			MaxIterations: 400,
		})
		if err != nil {
			t.Fatalf("Optimize failed: %v", err)
		}

		return result
	}

	t.Run("without a convergence block the budget is spent", func(t *testing.T) {
		result := run(t, nil)
		if result.Converged {
			t.Fatal("an unconfigured run must not claim convergence")
		}

		if result.StopReason != string(mayfly.TerminationMaxIterations) {
			t.Fatalf("unexpected stop reason: %q", result.StopReason)
		}
	})

	t.Run("target cost stops the run", func(t *testing.T) {
		target := 1e-6
		result := run(t, &MayflyTuning{Convergence: &MayflyConvergence{TargetCost: &target}})

		if !result.Converged || result.StopReason != string(mayfly.TerminationTargetCost) {
			t.Fatalf("expected a target-cost stop, got converged=%v reason=%q",
				result.Converged, result.StopReason)
		}

		if result.Iterations >= 400 {
			t.Fatalf("expected the run to stop early, used %d iterations", result.Iterations)
		}
	})

	t.Run("stagnation stops the run", func(t *testing.T) {
		window, minIters := 5, 1
		result := run(t, &MayflyTuning{Convergence: &MayflyConvergence{
			StagnationIterations: &window,
			MinIterations:        &minIters,
		}})

		if !result.Converged || result.StopReason != string(mayfly.TerminationStagnation) {
			t.Fatalf("expected a stagnation stop, got converged=%v reason=%q",
				result.Converged, result.StopReason)
		}

		if result.Iterations >= 400 {
			t.Fatalf("expected the run to stop early, used %d iterations", result.Iterations)
		}
	})
}

// TestMayflyTargetCostEndsTheWholeSchedule pins that a scheduled run stops at
// the target rather than working through the rest of its round plan. Without
// the break the later rounds spend audio renders on a question already
// answered, and a cold restart can finish on maximum_iterations -- reporting
// the run as unconverged after it had converged.
//
// The assertion is an equality rather than a threshold: a four-round schedule
// that meets the target in its first round must cost exactly what that round
// costs on its own. Both runs share a seed, and the first round of the plan is
// the same 100-iteration search either way.
func TestMayflyTargetCostEndsTheWholeSchedule(t *testing.T) {
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}, {Min: -10, Max: 10}}}
	target := 1e-6

	run := func(t *testing.T, epochs, restarts, budget int) (int, *Result) {
		t.Helper()

		evaluations := 0

		result, err := (&MayflyOptimizer{
			Variant: "desma", Population: 8, Seed: 1, MaxWorkers: 1,
			Tuning: &MayflyTuning{
				Convergence: &MayflyConvergence{TargetCost: &target},
				Schedule:    &MayflySchedule{Epochs: &epochs, Restarts: &restarts},
			},
		}).Optimize(context.Background(), func(x []float64) float64 {
			evaluations++

			return square(x[0]-1.25) + square(x[1]+2.5)
		}, []float64{5, 5}, bounds, OptimizeOptions{MaxIterations: budget})
		if err != nil {
			t.Fatalf("Optimize failed: %v", err)
		}

		return evaluations, result
	}

	firstRoundOnly, single := run(t, 1, 0, 100)
	scheduled, result := run(t, 2, 2, 400)

	if !result.Converged || result.StopReason != string(mayfly.TerminationTargetCost) {
		t.Fatalf("expected the schedule to end on the target cost, got converged=%v reason=%q",
			result.Converged, result.StopReason)
	}

	if scheduled != firstRoundOnly {
		t.Fatalf("a met target must end the schedule: the four-round run spent %d evaluations, its first round alone %d",
			scheduled, firstRoundOnly)
	}

	if result.Iterations != single.Iterations {
		t.Fatalf("expected %d iterations, the first round's own count, got %d",
			single.Iterations, result.Iterations)
	}
}

// TestMayflyPresetReportsTheDialectItChose closes a reporting gap: a preset
// selects a dialect without naming one, so the resolved report was blank and
// the caller could not see what had run.
func TestMayflyPresetReportsTheDialectItChose(t *testing.T) {
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}, {Min: -10, Max: 10}}}

	var resolved ResolvedMayfly

	_, err := (&MayflyOptimizer{
		Preset: "highly_multimodal", Population: 6, Seed: 1, MaxWorkers: 1,
		OnResolve: func(r ResolvedMayfly) { resolved = r },
	}).Optimize(context.Background(), func(x []float64) float64 {
		return square(x[0]-1.25) + square(x[1]+2.5)
	}, []float64{5, 5}, bounds, OptimizeOptions{MaxIterations: 10})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if resolved.Preset != "highly_multimodal" {
		t.Fatalf("preset not reported: %q", resolved.Preset)
	}

	if resolved.Variant != "olce" {
		t.Fatalf("expected the preset's dialect to be reported, got %q", resolved.Variant)
	}
}

// TestMayflyIsReproducibleAcrossWorkerCounts pins the property the whole
// engine comparison rests on: at a fixed seed a run is bit-identical however
// many workers evaluate it. Mayfly v0.7.0 gave the sequential and parallel
// paths the same proposal and commit semantics, so width does not change the
// trajectory either, and both halves are asserted here.
func TestMayflyIsReproducibleAcrossWorkerCounts(t *testing.T) {
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}, {Min: -10, Max: 10}}}
	initial := []float64{5, 5}
	objective := func(x []float64) float64 { return square(x[0]-1.25) + square(x[1]+2.5) }

	run := func(workers int) *Result {
		t.Helper()

		result, err := (&MayflyOptimizer{
			Variant: "desma", Population: 8, Seed: 4242, MaxWorkers: workers,
		}).Optimize(context.Background(), objective, initial, bounds, OptimizeOptions{
			MaxIterations: 40,
		})
		if err != nil {
			t.Fatalf("Optimize with %d workers failed: %v", workers, err)
		}

		return result
	}

	first, second := run(4), run(4)

	// A run that found nothing would agree with itself just as exactly, so the
	// reproducibility claim is only worth something next to a progress claim.
	// Forty iterations on a two-dimensional sphere reach the optimum to many
	// digits; 1e-6 is loose enough not to pin a trajectory.
	if first.BestCost > 1e-6 {
		t.Fatalf("best cost = %g, want a run that actually converged", first.BestCost)
	}

	if first.BestCost != second.BestCost || !reflect.DeepEqual(first.BestParams, second.BestParams) {
		t.Fatalf("four workers were not reproducible: %g %v then %g %v",
			first.BestCost, first.BestParams, second.BestCost, second.BestParams)
	}

	serial := run(1)

	if serial.BestCost != first.BestCost || !reflect.DeepEqual(serial.BestParams, first.BestParams) {
		t.Fatalf("one worker walked a different trajectory than four: %g %v against %g %v",
			serial.BestCost, serial.BestParams, first.BestCost, first.BestParams)
	}
}

// TestMayflyResultSeedEchoesTheConfiguredSeed checks the contract the wrapper's
// report depends on. buildConfig sets Config.Seed rather than Config.Rand, and
// the run's reported seed is only honest if mayfly agrees that it is the value
// the generator was built from.
func TestMayflyResultSeedEchoesTheConfiguredSeed(t *testing.T) {
	resolved := ResolvedMayfly{Variant: "desma", Seed: 20260902}

	cfg, err := (&MayflyOptimizer{Population: 6, MaxWorkers: 1}).buildConfig(resolved, 2, 20)
	if err != nil {
		t.Fatalf("buildConfig failed: %v", err)
	}

	cfg.ObjectiveFunc = func(x []float64) float64 { return square(x[0]) + square(x[1]) }

	res, err := mayfly.OptimizeContext(context.Background(), cfg)
	if err != nil {
		t.Fatalf("OptimizeContext failed: %v", err)
	}

	if res.Seed == nil {
		t.Fatal("mayfly did not report the seed the run was configured with")
	}

	if *res.Seed != resolved.Seed {
		t.Fatalf("Result.Seed = %d, want the configured %d", *res.Seed, resolved.Seed)
	}
}

// TestMayflySurvivesAnInvalidRegion covers what a fit objective actually does:
// it returns +Inf for a vector that fails to decode or validate. Mayfly v0.7.0
// fails a search whose whole initial population is non-finite, and a cold
// restart draws a fresh uniform population that can land entirely inside such a
// region, so the tracker reports a large finite penalty instead. Without that
// the run below errors and loses the answer the first round already found.
func TestMayflySurvivesAnInvalidRegion(t *testing.T) {
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}, {Min: -10, Max: 10}}}
	initial := []float64{9.95, 9.95}

	// Valid only in a corner a tenth of a percent of the box wide, so a
	// uniformly drawn population is invalid with overwhelming probability.
	objective := func(x []float64) float64 {
		if x[0] < 9.9 || x[1] < 9.9 {
			return math.Inf(1)
		}

		return square(x[0]-9.95) + square(x[1]-9.95)
	}

	restarts := 2
	tuning := &MayflyTuning{Schedule: &MayflySchedule{Restarts: &restarts}}

	result, err := (&MayflyOptimizer{
		Variant: "desma", Population: 6, Seed: 11, MaxWorkers: 1, Tuning: tuning,
	}).Optimize(context.Background(), objective, initial, bounds, OptimizeOptions{
		MaxIterations: 30,
	})
	if err != nil {
		t.Fatalf("Optimize failed on an objective with an invalid region: %v", err)
	}

	if !isFinite(result.BestCost) {
		t.Fatalf("best cost = %g, want a finite one", result.BestCost)
	}
}

// TestMayflyColdRoundsDoNotRepeatEachOther pins what a restart is for. Mayfly
// reads Config.Seed at the start of every OptimizeContext call, so a schedule
// that handed every round the same seed would run one search several times:
// each cold round would draw the identical uniform population and follow the
// identical trajectory, and the independent exploration a restart exists to buy
// would be spent on a search already done.
func TestMayflyColdRoundsDoNotRepeatEachOther(t *testing.T) {
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}, {Min: -10, Max: 10}}}
	initial := []float64{5, 5}
	objective := func(x []float64) float64 { return square(x[0]-1.25) + square(x[1]+2.5) }

	// One warm round, then two cold ones. The two cold rounds are what this
	// compares: they start from a fresh population rather than the incumbent,
	// so nothing but the random stream distinguishes them.
	restarts := 2
	tuning := &MayflyTuning{Schedule: &MayflySchedule{Restarts: &restarts}}

	const iterationsPerRound = 10

	var perIteration []float64

	_, err := (&MayflyOptimizer{
		Variant: "desma", Population: 6, Seed: 99, MaxWorkers: 1, Tuning: tuning,
	}).Optimize(context.Background(), objective, initial, bounds, OptimizeOptions{
		MaxIterations: 3 * iterationsPerRound,
		ReportEvery:   1,
		Report: func(progress Progress) {
			// CurrentCost is the round's own best, which restarts with the
			// round, so the sequence is a fingerprint of that round's search.
			perIteration = append(perIteration, progress.CurrentCost)
		},
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if len(perIteration) != 3*iterationsPerRound {
		t.Fatalf("got %d reports, want one per iteration of three rounds", len(perIteration))
	}

	secondRound := perIteration[iterationsPerRound : 2*iterationsPerRound]
	thirdRound := perIteration[2*iterationsPerRound:]

	if reflect.DeepEqual(secondRound, thirdRound) {
		t.Fatalf("the two cold rounds walked the same trajectory: %v", secondRound)
	}
}

// TestMayflyHMMAIsSelectableAndNamed covers the dialect mayfly v0.7.0 split out
// of GSASMA. Its Use flag is its own, so without a case in variantNameForConfig
// a run asking for it would report itself as plain "ma" and its two knobs would
// be looked up under the wrong dialect.
func TestMayflyHMMAIsSelectableAndNamed(t *testing.T) {
	rate := 0.4
	tuning := &MayflyTuning{CauchyMutationRate: &rate}

	cfg, err := newMayflyConfig(ResolvedMayfly{Variant: "hmma"}, 10, 3, 50, tuning)
	if err != nil {
		t.Fatalf("newMayflyConfig for hmma failed: %v", err)
	}

	if !cfg.UseHMMA {
		t.Fatal("expected the hmma dialect to be selected")
	}

	if got := variantNameForConfig(cfg); got != "hmma" {
		t.Fatalf("variantNameForConfig = %q, want hmma", got)
	}

	if cfg.CauchyMutationRate != rate {
		t.Fatalf("cauchy_mutation_rate = %v, want %v", cfg.CauchyMutationRate, rate)
	}

	// The same knob under the dialect it used to belong to is refused, which is
	// the whole point of moving it.
	if _, err := newMayflyConfig(ResolvedMayfly{Variant: "gsasma"}, 10, 3, 50, tuning); err == nil {
		t.Fatal("expected an hmma knob to be refused under gsasma")
	}
}
