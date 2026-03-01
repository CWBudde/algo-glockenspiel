package optimizer

import (
	"math"
	"testing"
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

	result, err := opt.Optimize(func(x []float64) float64 {
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

	_, err := opt.Optimize(func(x []float64) float64 {
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

	result, err := opt.Optimize(func(x []float64) float64 {
		return square(x[0]-2) + 0.5*square(x[1]+1)
	}, initial, bounds, OptimizeOptions{MaxIterations: 1})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if result.StopReason != "IterationLimit" {
		t.Fatalf("expected iteration limit stop, got %q", result.StopReason)
	}

	if result.Converged {
		t.Fatal("expected iteration limit to be non-converged")
	}
}

func square(x float64) float64 {
	return x * x
}
