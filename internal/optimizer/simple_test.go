package optimizer

import (
	"context"
	"math"
	"testing"

	gonumoptimize "gonum.org/v1/gonum/optimize"
)

func TestSimpleOptimizerFindsKnownMinimum(t *testing.T) {
	opt := &SimpleOptimizer{
		AbsoluteTolerance: 1e-10,
		RelativeTolerance: 1e-10,
		StallIterations:   20,
	}
	initial := []float64{5, 5}
	bounds := Bounds{Ranges: []Range{
		{Min: -10, Max: 10},
		{Min: -10, Max: 10},
	}}

	result, err := opt.Optimize(context.Background(), func(x []float64) float64 {
		return square(x[0]-1.25) + square(x[1]+2.5)
	}, initial, bounds, OptimizeOptions{MaxIterations: 300})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if result.BestCost > 1e-8 {
		t.Fatalf("unexpected best cost: got %g", result.BestCost)
	}

	if math.Abs(result.BestParams[0]-1.25) > 1e-3 || math.Abs(result.BestParams[1]+2.5) > 1e-3 {
		t.Fatalf("unexpected optimum: got %v", result.BestParams)
	}

	if result.Iterations == 0 {
		t.Fatal("expected at least one iteration")
	}
}

func TestSimpleOptimizerReportsProgress(t *testing.T) {
	opt := &SimpleOptimizer{}
	initial := []float64{2}
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}}}

	var updates []Progress

	_, err := opt.Optimize(context.Background(), func(x []float64) float64 {
		return square(x[0] - 3)
	}, initial, bounds, OptimizeOptions{
		MaxIterations: 100,
		ReportEvery:   1,
		Report: func(p Progress) {
			updates = append(updates, p)
		},
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if len(updates) == 0 {
		t.Fatal("expected progress updates")
	}

	if updates[0].Iteration == 0 {
		t.Fatal("expected progress iteration to be populated")
	}
}

func TestSimpleOptimizerStopsAtIterationLimit(t *testing.T) {
	opt := &SimpleOptimizer{
		AbsoluteTolerance: 1e-20,
		RelativeTolerance: 1e-20,
		StallIterations:   1000,
	}
	initial := []float64{9, -9}
	bounds := Bounds{Ranges: []Range{
		{Min: -10, Max: 10},
		{Min: -10, Max: 10},
	}}

	result, err := opt.Optimize(context.Background(), func(x []float64) float64 {
		return square(x[0]-2) + 0.5*square(x[1]+1)
	}, initial, bounds, OptimizeOptions{MaxIterations: 1})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	// Compare against the typed status rather than a hand-written literal, so
	// a gonum rename shows up as a build failure instead of a flaky assertion.
	if result.StopReason != gonumoptimize.IterationLimit.String() {
		t.Fatalf("expected iteration limit stop, got %q", result.StopReason)
	}

	if result.Converged {
		t.Fatal("expected iteration limit to be non-converged")
	}
}

// TestSimpleOptimizerResultParamsMatchCost guards the bug where the gonum
// recorder stored an unmirrored simplex point while the objective closure
// stored the mirrored one, so BestParams could belong to a different point than
// BestCost - and could even be out of bounds.
func TestSimpleOptimizerResultParamsMatchCost(t *testing.T) {
	bounds := Bounds{Ranges: []Range{
		{Min: -1, Max: 1},
		{Min: 100, Max: 600},
	}}
	objective := func(x []float64) float64 {
		return square(x[0]-0.4) + square((x[1]-480)/100)
	}

	result, err := (&SimpleOptimizer{}).Optimize(context.Background(), objective,
		[]float64{-0.9, 120}, bounds, OptimizeOptions{MaxIterations: 200})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if !bounds.Contains(result.BestParams) {
		t.Fatalf("result escaped bounds: %v", result.BestParams)
	}

	if recomputed := objective(result.BestParams); math.Abs(recomputed-result.BestCost) > 1e-12 {
		t.Fatalf("BestCost does not belong to BestParams: reported=%g recomputed=%g", result.BestCost, recomputed)
	}
}

// TestSimpleOptimizerSearchesNormalizedSpace covers the axis-scaling defect: in
// raw encoded units the default simplex step is degenerate along a wide axis,
// so the wide parameter never moves.
func TestSimpleOptimizerSearchesNormalizedSpace(t *testing.T) {
	bounds := Bounds{Ranges: []Range{
		{Min: 0, Max: 4},
		{Min: 0.1, Max: 500},
	}}
	initial := []float64{1, 20}
	target := []float64{2.5, 400}

	objective := func(x []float64) float64 {
		return square(x[0]-target[0]) + square(x[1]-target[1])
	}

	result, err := (&SimpleOptimizer{}).Optimize(context.Background(), objective,
		initial, bounds, OptimizeOptions{MaxIterations: 400})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if math.Abs(result.BestParams[1]-target[1]) > 1 {
		t.Fatalf("wide axis barely moved: got %g want %g", result.BestParams[1], target[1])
	}
}

func TestSimpleOptimizerStopsOnCanceledContext(t *testing.T) {
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}}}
	ctx, cancel := context.WithCancel(context.Background())

	calls := 0

	result, err := (&SimpleOptimizer{}).Optimize(ctx, func(x []float64) float64 {
		calls++
		if calls > 10 {
			cancel()
		}

		return square(x[0] - 3)
	}, []float64{9}, bounds, OptimizeOptions{MaxIterations: 100000})

	cancel()

	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if result.StopReason != "context_canceled" {
		t.Fatalf("unexpected stop reason: %q", result.StopReason)
	}

	if result.Converged {
		t.Fatal("a canceled run has not converged")
	}
}

func square(x float64) float64 {
	return x * x
}

// TestSimpleOptimizerStopsAtTheEvaluationCap checks that the shared cap
// reaches gonum's own budget. The campaign never runs Nelder-Mead, so the stop
// reason stays gonum's; what has to hold is that the count means the same
// thing here as it does for the population backends.
func TestSimpleOptimizerStopsAtTheEvaluationCap(t *testing.T) {
	const (
		dims   = 8
		budget = 500
	)

	ranges := make([]Range, dims)
	initial := make([]float64, dims)

	for i := range ranges {
		ranges[i] = Range{Min: -5, Max: 5}
		initial[i] = -3
	}

	// Rosenbrock rather than a sphere: Nelder-Mead converges on a sphere in
	// far fewer than five hundred evaluations, and a cap that never binds
	// tests nothing.
	rosenbrock := func(x []float64) float64 {
		total := 0.0
		for i := 0; i+1 < len(x); i++ {
			total += 100*square(x[i+1]-x[i]*x[i]) + square(1-x[i])
		}

		return total
	}

	result, err := (&SimpleOptimizer{}).Optimize(
		context.Background(), rosenbrock, initial, Bounds{Ranges: ranges},
		OptimizeOptions{MaxEvaluations: budget},
	)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	// A simplex step costs at most one reflection, expansion or contraction
	// per vertex, so 2n+1 bounds the overrun.
	overrun := 2*dims + 1
	if result.Evaluations < budget || result.Evaluations >= budget+overrun {
		t.Fatalf("evaluations = %d, want in [%d, %d)", result.Evaluations, budget, budget+overrun)
	}

	if result.StopReason != gonumoptimize.FunctionEvaluationLimit.String() {
		t.Fatalf("stop reason = %q, want gonum's own evaluation-limit status", result.StopReason)
	}
}
