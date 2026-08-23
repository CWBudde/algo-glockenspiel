package optimizer

import (
	"context"
	"testing"
	"time"
)

// TestMayflyAutoVariantIsBudgeted is the test that makes the feature safe to
// ship. mayfly.ClassifyProblem takes no context, and the three short searches
// inside testConvergenceStability call Optimize rather than OptimizeContext, so
// nothing in the library will stop it. Without the limiter a fit would spend
// thousands of real audio renders before starting.
func TestMayflyAutoVariantIsBudgeted(t *testing.T) {
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}, {Min: -10, Max: 10}}}
	initial := []float64{5, 5}

	objective := func(x []float64) float64 {
		return square(x[0]-1.25) + square(x[1]+2.5)
	}

	budget := 120
	tuning := &MayflyTuning{Schedule: &MayflySchedule{ClassifyEvals: &budget}}

	var resolved ResolvedMayfly

	result, err := (&MayflyOptimizer{
		Variant: "auto", Population: 6, Seed: 1, MaxWorkers: 1, Tuning: tuning,
		OnResolve: func(r ResolvedMayfly) { resolved = r },
	}).Optimize(context.Background(), objective, initial, bounds, OptimizeOptions{
		MaxIterations: 10,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if resolved.ClassifyEvaluations > budget {
		t.Fatalf("classification overspent: used %d of %d", resolved.ClassifyEvaluations, budget)
	}

	if resolved.ClassifyEvaluations == 0 {
		t.Fatal("expected classification to actually evaluate the objective")
	}

	if resolved.Variant == "auto" || resolved.Variant == "" {
		t.Fatalf("auto did not resolve to a dialect: %q", resolved.Variant)
	}

	if resolved.Recommendation == "" {
		t.Fatal("expected the selector's reasoning to be reported")
	}

	// The renders classification spent are part of what the run cost, so they
	// must not vanish from the total.
	if result.Evaluations < resolved.ClassifyEvaluations {
		t.Fatalf("classification evaluations were not counted: total=%d classify=%d",
			result.Evaluations, resolved.ClassifyEvaluations)
	}
}

// TestMayflyAutoStopsAtItsDeadline covers the other half of the bound: a run
// whose time budget expires during classification must not keep rendering.
func TestMayflyAutoStopsAtItsDeadline(t *testing.T) {
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}}}

	slow := func(x []float64) float64 {
		time.Sleep(time.Millisecond)

		return square(x[0] - 1.25)
	}

	tracker := newMayflyTracker(slow, bounds, time.Now(), OptimizeOptions{})

	// A 20ms budget gives classification 2ms, so the limiter must cut it off
	// long before a generous evaluation cap is reached.
	_, _, spent := classifyMayfly(context.Background(), tracker, 1, 100_000, 20*time.Millisecond)

	if spent >= 100_000 {
		t.Fatalf("the deadline did not bound classification: spent %d", spent)
	}

	if spent > 200 {
		t.Fatalf("classification ran well past its deadline: spent %d", spent)
	}
}

// TestMayflyValidateAcceptsAutoWithoutMeasuring keeps the fit-slot property: a
// request naming "auto" must be accepted up front, because the dialect is
// chosen from an objective that does not exist at validation time.
func TestMayflyValidateAcceptsAutoWithoutMeasuring(t *testing.T) {
	if err := (&MayflyOptimizer{Variant: "auto"}).Validate(100); err != nil {
		t.Fatalf("auto must validate without running anything: %v", err)
	}

	// Everything that is not the dialect is still checked.
	window := 500
	if err := (&MayflyOptimizer{
		Variant: "auto",
		Tuning:  &MayflyTuning{Convergence: &MayflyConvergence{StagnationIterations: &window}},
	}).Validate(100); err == nil {
		t.Fatal("expected an unreachable stagnation window to be refused under auto")
	}
}
