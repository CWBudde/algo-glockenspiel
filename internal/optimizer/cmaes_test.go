package optimizer

import (
	"context"
	"math"
	"sync/atomic"
	"testing"
	"time"
)

// unitSphere is a sphere shifted off the centre of the unit cube, which is the
// cube every backend searches after normalization.
func unitSphere(optimum []float64) ObjectiveFunc {
	return func(x []float64) float64 {
		total := 0.0
		for i := range x {
			total += square(x[i] - optimum[i])
		}

		return total
	}
}

func cmaesOptimum(dims int) []float64 {
	optimum := make([]float64, dims)
	for i := range optimum {
		optimum[i] = 0.25 + 0.5*float64(i)/float64(dims)
	}

	return optimum
}

// badStart is the corner furthest from cmaesOptimum, so a run that only
// polishes its starting point cannot pass the tests below.
func badStart(dims int) []float64 {
	start := make([]float64, dims)
	for i := range start {
		start[i] = 0.99
	}

	return start
}

func TestCMAESOptimizerFindsShiftedSphereMinimumFromABadStart(t *testing.T) {
	const dims = 6

	optimum := cmaesOptimum(dims)

	for _, covariance := range []string{"separable", "block"} {
		t.Run(covariance, func(t *testing.T) {
			opt := &CMAESOptimizer{Covariance: covariance, Seed: 11, MaxWorkers: 2}
			if covariance == covarianceBlock {
				opt.BlockGroups = [][]int{{0, 1, 2}, {3, 4, 5}}
			}

			result, err := opt.Optimize(
				context.Background(), unitSphere(optimum), badStart(dims),
				UnitBounds(dims), OptimizeOptions{MaxIterations: 200},
			)
			if err != nil {
				t.Fatalf("Optimize failed: %v", err)
			}

			if result.BestCost > 1e-8 {
				t.Fatalf("expected the sphere minimum, got cost %g", result.BestCost)
			}

			for i, value := range result.BestParams {
				if math.Abs(value-optimum[i]) > 1e-3 {
					t.Fatalf("dimension %d: got %g want %g", i, value, optimum[i])
				}
			}
		})
	}
}

func TestCMAESOptimizerNeverReportsWorseThanItsInitialGuess(t *testing.T) {
	const dims = 4

	optimum := cmaesOptimum(dims)

	// Starting at the optimum, every cold restart samples a worse basin, so
	// only a run that evaluates its starting point can report the optimum.
	result, err := (&CMAESOptimizer{Seed: 3, RestartLimit: 3, MaxWorkers: 1}).Optimize(
		context.Background(), unitSphere(optimum), optimum,
		UnitBounds(dims), OptimizeOptions{MaxIterations: 30},
	)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if result.BestCost != 0 {
		t.Fatalf("starting from the optimum must stay at the optimum: got %g", result.BestCost)
	}
}

// runSeededCMAES is one fully determined run: a fixed seed, a fixed iteration
// budget and no time budget, so two calls may differ only through the worker
// count.
func runSeededCMAES(t *testing.T, workers int) *Result {
	t.Helper()

	const dims = 5

	result, err := (&CMAESOptimizer{Seed: 7, MaxWorkers: workers, RestartLimit: 3}).Optimize(
		context.Background(), unitSphere(cmaesOptimum(dims)), badStart(dims),
		UnitBounds(dims), OptimizeOptions{MaxIterations: 60},
	)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	return result
}

func assertSameSolution(t *testing.T, first, second *Result) {
	t.Helper()

	if first.BestCost != second.BestCost {
		t.Fatalf("best cost differs: %v vs %v", first.BestCost, second.BestCost)
	}

	if len(first.BestParams) != len(second.BestParams) {
		t.Fatalf("parameter count differs: %d vs %d", len(first.BestParams), len(second.BestParams))
	}

	for i := range first.BestParams {
		if first.BestParams[i] != second.BestParams[i] {
			t.Fatalf("dimension %d differs: %v vs %v", i, first.BestParams[i], second.BestParams[i])
		}
	}
}

func TestCMAESOptimizerRepeatsAParallelRunBitForBit(t *testing.T) {
	assertSameSolution(t, runSeededCMAES(t, 4), runSeededCMAES(t, 4))
}

// TestCMAESOptimizerMatchesSerialEvaluation pins the library's promise that a
// seeded run does not depend on how many workers evaluate its generations: it
// draws every sample on the calling goroutine before any evaluation starts.
func TestCMAESOptimizerMatchesSerialEvaluation(t *testing.T) {
	assertSameSolution(t, runSeededCMAES(t, 1), runSeededCMAES(t, 4))
}

func TestCMAESOptimizerReportsAChosenSeedThatReproducesTheRun(t *testing.T) {
	const dims = 4

	var resolved ResolvedCMAES

	objective := unitSphere(cmaesOptimum(dims))
	opts := OptimizeOptions{MaxIterations: 40}

	first, err := (&CMAESOptimizer{MaxWorkers: 2, OnResolve: func(r ResolvedCMAES) {
		resolved = r
	}}).Optimize(context.Background(), objective, badStart(dims), UnitBounds(dims), opts)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if resolved.Seed == 0 {
		t.Fatal("a zero seed must be resolved to a concrete one and reported")
	}

	if resolved.Covariance != covarianceSeparable || resolved.Sigma != defaultCMAESSigma {
		t.Fatalf("unexpected defaults: %+v", resolved)
	}

	// Hansen's default at four dimensions is 4 + floor(3 ln 4) = 8.
	if resolved.Lambda != 8 {
		t.Fatalf("expected Hansen's default population 8, got %d", resolved.Lambda)
	}

	second, err := (&CMAESOptimizer{MaxWorkers: 2, Seed: resolved.Seed}).Optimize(
		context.Background(), objective, badStart(dims), UnitBounds(dims), opts,
	)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	assertSameSolution(t, first, second)
}

func TestCMAESOptimizerStopsOnACanceledContextWithTheBestSoFar(t *testing.T) {
	const dims = 4

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	optimum := cmaesOptimum(dims)
	sphere := unitSphere(optimum)

	var evaluations atomic.Int64

	objective := func(x []float64) float64 {
		if evaluations.Add(1) > 200 {
			cancel()
		}

		return sphere(x)
	}

	start := badStart(dims)

	result, err := (&CMAESOptimizer{Seed: 5, MaxWorkers: 1}).Optimize(
		ctx, objective, start, UnitBounds(dims), OptimizeOptions{MaxIterations: 100000},
	)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if result.StopReason != "context_canceled" {
		t.Fatalf("expected context_canceled, got %q", result.StopReason)
	}

	if result.BestCost > sphere(start) {
		t.Fatalf("a canceled run must keep its best so far: got %g", result.BestCost)
	}
}

func TestCMAESOptimizerRestartsUntilTheTimeBudgetIsSpent(t *testing.T) {
	if testing.Short() {
		t.Skip("spends a two-second time budget by design")
	}

	const (
		dims   = 6
		budget = 2 * time.Second
	)

	result, err := (&CMAESOptimizer{Seed: 13, MaxWorkers: 1}).Optimize(
		context.Background(), unitSphere(cmaesOptimum(dims)), badStart(dims),
		UnitBounds(dims), OptimizeOptions{TimeBudget: budget},
	)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if result.Elapsed < time.Duration(0.95*float64(budget)) {
		t.Fatalf("expected the run to spend its budget, got %v of %v", result.Elapsed, budget)
	}

	if result.Restarts < 2 {
		t.Fatalf("expected at least two runs in %v, got %d", budget, result.Restarts)
	}

	if result.StopReason != "time_budget" {
		t.Fatalf("expected time_budget, got %q", result.StopReason)
	}

	// A loop the clock stopped has no claim on convergence, whatever its last
	// run ended on.
	if result.Converged {
		t.Fatal("a budget-bound restart loop must not report convergence")
	}

	t.Logf("elapsed %v of %v over %d runs and %d evaluations",
		result.Elapsed, budget, result.Restarts, result.Evaluations)
}

func TestCMAESOptimizerStopsAfterTheRestartLimit(t *testing.T) {
	const dims = 4

	seen := map[int]bool{}

	result, err := (&CMAESOptimizer{Seed: 17, MaxWorkers: 1, RestartLimit: 3}).Optimize(
		context.Background(), unitSphere(cmaesOptimum(dims)), badStart(dims),
		UnitBounds(dims), OptimizeOptions{
			MaxIterations: 100000,
			ReportEvery:   1,
			Report: func(p Progress) {
				seen[p.Restart] = true
			},
		},
	)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if result.StopReason != "restart_limit" {
		t.Fatalf("expected restart_limit, got %q", result.StopReason)
	}

	if result.Restarts != 3 {
		t.Fatalf("expected exactly three runs, got %d", result.Restarts)
	}

	if len(seen) != 3 || !seen[0] || !seen[2] {
		t.Fatalf("expected progress from runs 0, 1 and 2, got %v", seen)
	}
}

// TestCMAESOptimizerSurvivesANonFiniteRegion pins the objective adapter's
// penalty. A vector that fails to decode costs +Inf, and a cold restart can
// start wholly inside such a region; the run has to keep going and lose rather
// than come back with a best that has no position at all.
func TestCMAESOptimizerSurvivesANonFiniteRegion(t *testing.T) {
	const dims = 4

	optimum := cmaesOptimum(dims)
	sphere := unitSphere(optimum)
	objective := func(x []float64) float64 {
		if x[0] > 0.6 {
			return math.Inf(1)
		}

		return sphere(x)
	}

	result, err := (&CMAESOptimizer{Seed: 23, MaxWorkers: 1, RestartLimit: 2}).Optimize(
		context.Background(), objective, badStart(dims),
		UnitBounds(dims), OptimizeOptions{MaxIterations: 200},
	)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if result.BestCost >= invalidCost {
		t.Fatalf("the invalid-region penalty must never be reported as a cost: got %g", result.BestCost)
	}

	if result.BestCost > 1e-6 {
		t.Fatalf("expected the run to reach the feasible minimum, got %g", result.BestCost)
	}
}

// TestCMAESOptimizerCapsTotalIterationsAcrossRestarts pins that
// OptimizeOptions.MaxIterations is the total across every restart, not each
// run's own cap. The objective is flat, so TolFun ends a run as soon as its
// history window is full and the loop restarts many times inside the budget; a
// per-run cap would let the total run past it.
func TestCMAESOptimizerCapsTotalIterationsAcrossRestarts(t *testing.T) {
	const (
		dims  = 4
		total = 400
	)

	flat := func(_ []float64) float64 { return 1 }

	result, err := (&CMAESOptimizer{Seed: 29, MaxWorkers: 1}).Optimize(
		context.Background(), flat, badStart(dims),
		UnitBounds(dims), OptimizeOptions{MaxIterations: total},
	)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if result.Iterations != total {
		t.Fatalf("expected %d iterations across every run, got %d", total, result.Iterations)
	}

	if result.Restarts < 2 {
		t.Fatalf("expected the cap to span several runs, got %d", result.Restarts)
	}

	if result.StopReason != "max_iterations" {
		t.Fatalf("expected max_iterations, got %q", result.StopReason)
	}
}

// TestCMAESOptimizerRejectsARunWithoutABudget covers the one combination that
// has no stopping rule: no iteration cap, no time budget and no restart limit.
func TestCMAESOptimizerRejectsARunWithoutABudget(t *testing.T) {
	const dims = 4

	_, err := (&CMAESOptimizer{Seed: 31}).Optimize(
		context.Background(), unitSphere(cmaesOptimum(dims)), badStart(dims),
		UnitBounds(dims), OptimizeOptions{},
	)
	if err == nil {
		t.Fatal("a run with no budget of any kind must be rejected")
	}
}

func TestCMAESOptimizerValidateRejectsBadSettings(t *testing.T) {
	cases := []struct {
		name string
		opt  CMAESOptimizer
	}{
		{"unknown covariance", CMAESOptimizer{Covariance: "diagonal"}},
		{"lambda below two", CMAESOptimizer{Lambda: 1}},
		{"sigma above the cube", CMAESOptimizer{InitialSigma: 1.5}},
		{"negative sigma", CMAESOptimizer{InitialSigma: -0.1}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := testCase.opt.Validate(100); err == nil {
				t.Fatal("expected the setting to be rejected")
			}
		})
	}

	if err := (&CMAESOptimizer{Covariance: "block", Lambda: 12}).Validate(100); err != nil {
		t.Fatalf("a valid configuration must pass: %v", err)
	}
}

// TestCMAESOptimizerRejectsBlockModeWithoutGroups covers the check Validate
// cannot make: the partition is only checkable against a known dimension.
func TestCMAESOptimizerRejectsBlockModeWithoutGroups(t *testing.T) {
	const dims = 4

	_, err := (&CMAESOptimizer{Covariance: "block"}).Optimize(
		context.Background(), unitSphere(cmaesOptimum(dims)), badStart(dims),
		UnitBounds(dims), OptimizeOptions{MaxIterations: 10},
	)
	if err == nil {
		t.Fatal("block covariance without groups must be rejected")
	}

	_, err = (&CMAESOptimizer{Covariance: "block", BlockGroups: [][]int{{0, 1}, {2}}}).Optimize(
		context.Background(), unitSphere(cmaesOptimum(dims)), badStart(dims),
		UnitBounds(dims), OptimizeOptions{MaxIterations: 10},
	)
	if err == nil {
		t.Fatal("a partition missing a coordinate must be rejected")
	}
}
