package optimizer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cwbudde/mayfly"
)

// MayflyOptimizer wraps github.com/cwbudde/mayfly behind the shared optimizer interface.
type MayflyOptimizer struct {
	Variant    string
	Population int
	Seed       int64

	// MaxWorkers bounds parallel objective evaluation. Zero selects
	// runtime.NumCPU(); one disables parallelism entirely. Parallel evaluation
	// is safe because ObjectiveFunction.Objective hands out per-goroutine
	// render state.
	MaxWorkers int
}

// Optimize runs Mayfly in a normalized [0,1] search space and maps candidates back into bounds.
func (o *MayflyOptimizer) Optimize(ctx context.Context, objective ObjectiveFunc, initial []float64, bounds Bounds, opts OptimizeOptions) (*Result, error) {
	if objective == nil {
		return nil, fmt.Errorf("objective cannot be nil")
	}

	if len(initial) == 0 {
		return nil, fmt.Errorf("initial parameters cannot be empty")
	}

	if err := bounds.CheckVector(initial); err != nil {
		return nil, err
	}

	if ctx == nil {
		ctx = context.Background()
	}

	start := time.Now()

	initial, err := bounds.Clamp(initial)
	if err != nil {
		return nil, err
	}

	seed, err := bounds.Normalize(initial)
	if err != nil {
		return nil, err
	}

	cfg, err := o.buildConfig(len(initial), maxInt(1, opts.MaxIterations))
	if err != nil {
		return nil, err
	}

	// The time budget is expressed as a derived context so that mayfly stops
	// the run itself. The previous approach - returning bestCost+1 from the
	// objective past the deadline - fed a moving, fabricated cost back into
	// DESMA's elite selection and search-range adaptation.
	runCtx := ctx

	if opts.TimeBudget > 0 {
		var cancel context.CancelFunc

		runCtx, cancel = context.WithTimeout(ctx, opts.TimeBudget)
		defer cancel()
	}

	tracker := newMayflyTracker(objective, bounds, start, opts)
	tracker.seedBaseline(initial)
	cfg.ObjectiveFunc = tracker.evaluate

	res, runErr := mayfly.OptimizeContext(
		runCtx, cfg,
		// Without this the preset or resumed checkpoint is thrown away and both
		// populations start uniformly at random.
		mayfly.WithInitialPopulation([][]float64{seed}, [][]float64{seed}),
		mayfly.WithProgressObserver(tracker.observe),
	)
	if runErr != nil {
		return tracker.abortedResult(ctx, runCtx, runErr)
	}

	return tracker.result(res), nil
}

func (o *MayflyOptimizer) variant() string {
	v := strings.ToLower(strings.TrimSpace(o.Variant))
	if v == "" {
		return "desma"
	}

	return v
}

func (o *MayflyOptimizer) population() int {
	if o.Population >= 2 {
		return o.Population
	}

	return 10
}

func (o *MayflyOptimizer) buildConfig(dims, iters int) (*mayfly.Config, error) {
	cfg, err := newMayflyConfig(o.variant(), o.population(), dims, iters)
	if err != nil {
		return nil, err
	}

	if o.Seed != 0 {
		cfg.Rand = rand.New(rand.NewSource(o.Seed))
	}

	workers := o.MaxWorkers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	cfg.MaxWorkers = workers
	cfg.EnableParallel = workers > 1

	return cfg, nil
}

// Validate resolves the configured variant without running anything.
//
// It exists for callers that decide whether to accept a request before they
// book the work it names. Without it the name is first resolved inside
// Optimize, which for the HTTP fit API means a malformed request is accepted,
// claims the single fit slot, and fails asynchronously a moment later instead
// of being rejected as the bad request it always was.
func (o *MayflyOptimizer) Validate() error {
	_, err := resolveVariant(o.variant())

	return err
}

// resolveVariant looks a variant up in the upstream registry, which is the
// single source of truth for variant names, so new variants are picked up
// without touching this wrapper.
func resolveVariant(name string) (mayfly.AlgorithmVariant, error) {
	selected := mayfly.NewVariant(name)
	if selected == nil {
		return nil, fmt.Errorf("unsupported mayfly variant %q, want one of %s",
			name, strings.Join(mayfly.ListVariants(), ", "))
	}

	return selected, nil
}

func newMayflyConfig(variant string, pop, dims, iters int) (*mayfly.Config, error) {
	selected, err := resolveVariant(variant)
	if err != nil {
		return nil, err
	}

	cfg := selected.GetConfig()

	cfg.ProblemSize = dims
	cfg.LowerBound = 0.0
	cfg.UpperBound = 1.0
	cfg.MaxIterations = iters
	cfg.NPop = pop
	cfg.NPopF = pop
	cfg.NC = 2 * pop
	cfg.NM = maxInt(1, int(math.Round(0.05*float64(pop))))

	if err := validateMayflyConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// validateMayflyConfig rejects configurations up front. The wrapper used to
// wrap the library call in recover(), which turned genuine library bugs into
// opaque "mayfly panic" errors; upstream validates and returns errors, so the
// only thing worth checking here is what this wrapper derives itself.
func validateMayflyConfig(cfg *mayfly.Config) error {
	if cfg.ProblemSize <= 0 {
		return fmt.Errorf("problem size must be positive, got %d", cfg.ProblemSize)
	}

	if cfg.MaxIterations <= 0 {
		return fmt.Errorf("max iterations must be positive, got %d", cfg.MaxIterations)
	}

	if cfg.NPop < 2 || cfg.NPopF < 2 {
		return fmt.Errorf("population must be at least 2, got %d males and %d females", cfg.NPop, cfg.NPopF)
	}

	// Mating pairs the k-th best male with the k-th best female, so NC/2 must
	// not exceed either population.
	if pairs := cfg.NC / 2; pairs > cfg.NPop || pairs > cfg.NPopF {
		return fmt.Errorf("offspring count %d needs %d parent pairs, exceeding populations %d/%d",
			cfg.NC, pairs, cfg.NPop, cfg.NPopF)
	}

	return nil
}

// mayflyTracker owns every mutable value shared with the library. Parallel
// evaluation calls evaluate from several goroutines at once, so the best-so-far
// state and the evaluation counter must be guarded.
type mayflyTracker struct {
	objective ObjectiveFunc
	bounds    Bounds
	start     time.Time
	opts      OptimizeOptions

	mu         sync.Mutex
	bestParams []float64
	bestCost   float64
	evals      int
	iterations int
	reports    int
}

func newMayflyTracker(objective ObjectiveFunc, bounds Bounds, start time.Time, opts OptimizeOptions) *mayflyTracker {
	return &mayflyTracker{
		objective: objective,
		bounds:    bounds,
		start:     start,
		opts:      opts,
		bestCost:  math.Inf(1),
	}
}

// seedBaseline evaluates the caller's starting point so a run can never report
// a result worse than what it was given.
func (t *mayflyTracker) seedBaseline(initial []float64) {
	cost := t.objective(initial)

	t.mu.Lock()
	defer t.mu.Unlock()

	t.evals++

	t.bestParams = append([]float64(nil), initial...)
	t.bestCost = cost
}

func (t *mayflyTracker) evaluate(pos []float64) float64 {
	actual, err := t.bounds.Denormalize(pos)
	if err != nil {
		return math.Inf(1)
	}

	cost := t.objective(actual)

	t.mu.Lock()
	t.evals++

	if cost < t.bestCost {
		t.bestCost = cost
		t.bestParams = append(t.bestParams[:0], actual...)
	}

	t.mu.Unlock()

	if !isFinite(cost) {
		return math.Inf(1)
	}

	return cost
}

// observe forwards mayfly's per-iteration progress. Progress.Iteration counts
// the callbacks this wrapper emits, not mayfly's iterations, per the Progress
// contract.
func (t *mayflyTracker) observe(progress mayfly.Progress) {
	t.mu.Lock()

	t.iterations = progress.Iteration

	if t.opts.Report == nil || t.opts.ReportEvery <= 0 || progress.Iteration%t.opts.ReportEvery != 0 {
		t.mu.Unlock()

		return
	}

	t.reports++
	update := Progress{
		Iteration:           t.reports,
		OptimizerIterations: progress.Iteration,
		CurrentCost:         progress.Best.Cost,
		BestCost:            t.bestCost,
		BestParams:          append([]float64(nil), t.bestParams...),
		Elapsed:             time.Since(t.start),
		Evaluations:         t.evals,
	}

	t.mu.Unlock()

	t.opts.Report(update)
}

func (t *mayflyTracker) result(res *mayfly.Result) *Result {
	t.mu.Lock()
	defer t.mu.Unlock()

	iterations := t.iterations
	evals := t.evals
	reason := "unknown"
	converged := false

	if res != nil {
		iterations = res.IterationCount
		reason = string(res.TerminationReason)

		// A metaheuristic never proves convergence; the only honest signal is
		// that the run stopped for a convergence criterion instead of
		// exhausting its iteration budget.
		converged = res.TerminationReason == mayfly.TerminationTargetCost ||
			res.TerminationReason == mayfly.TerminationStagnation

		if res.FuncEvalCount > evals {
			evals = res.FuncEvalCount
		}
	}

	return &Result{
		BestParams:  append([]float64(nil), t.bestParams...),
		BestCost:    t.bestCost,
		Iterations:  iterations,
		Elapsed:     time.Since(t.start),
		Converged:   converged,
		StopReason:  reason,
		Evaluations: evals,
	}
}

// abortedResult reports the best solution found before cancellation. Mayfly
// returns a nil result plus the context error in that case, but the caller
// still wants whatever the truncated run achieved.
func (t *mayflyTracker) abortedResult(ctx, runCtx context.Context, runErr error) (*Result, error) {
	if runCtx.Err() == nil {
		return nil, runErr
	}

	res := t.result(nil)

	switch {
	case ctx.Err() != nil:
		res.StopReason = "context_canceled"
	case errors.Is(runErr, context.DeadlineExceeded):
		res.StopReason = "time_budget"
	default:
		res.StopReason = "canceled"
	}

	return res, nil
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}

	return b
}
