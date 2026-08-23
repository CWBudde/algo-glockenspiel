package optimizer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cwbudde/mayfly"
)

// MayflyOptimizer wraps github.com/cwbudde/mayfly behind the shared optimizer interface.
type MayflyOptimizer struct {
	Variant    string
	Population int

	// Seed selects the random stream. Zero means "pick one and report it"
	// rather than "be unreproducible": see resolveSeed.
	Seed int64

	// MaxWorkers bounds parallel objective evaluation. Zero selects
	// runtime.NumCPU(); one disables parallelism entirely. Parallel evaluation
	// is safe because ObjectiveFunction.Objective hands out per-goroutine
	// render state.
	MaxWorkers int

	// OnResolve reports the settings a run actually chose, once they are known
	// and before the search starts. A nil callback disables the report.
	//
	// It exists because those choices were invisible: a zero seed was resolved
	// inside the library and discarded here, so the run could not be repeated
	// and a resumed run had no stream to continue.
	OnResolve func(ResolvedMayfly)
}

// ResolvedMayfly is what a run settled on once every "choose one for me" input
// has been resolved. The CLI prints it and records the seed in the checkpoint,
// and the HTTP and WASM front ends echo it in their progress snapshots.
type ResolvedMayfly struct {
	// Variant is the dialect the run uses, after defaulting and alias
	// resolution.
	Variant string
	// Seed is the value the run's generator was constructed from, never zero.
	Seed int64
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

	// Resolve every "choose one for me" input before the config is built, so
	// the run and the report cannot disagree about what it used.
	resolved := ResolvedMayfly{Variant: o.variant(), Seed: o.resolveSeed()}

	cfg, err := o.buildConfig(resolved, len(initial), maxInt(1, opts.MaxIterations))
	if err != nil {
		return nil, err
	}

	if o.OnResolve != nil {
		o.OnResolve(resolved)
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

// resolveSeed turns a zero seed into a concrete one.
//
// Leaving cfg.Rand nil and letting mayfly pick is not equivalent: it reports
// its choice in Result.Seed, which upstream documents as the time-based
// fallback and which therefore says nothing about a caller-supplied generator.
// Resolving here gives one value that is both used and reportable.
func (o *MayflyOptimizer) resolveSeed() int64 {
	if o.Seed != 0 {
		return o.Seed
	}

	return time.Now().UnixNano()
}

func (o *MayflyOptimizer) buildConfig(resolved ResolvedMayfly, dims, iters int) (*mayfly.Config, error) {
	cfg, err := newMayflyConfig(resolved.Variant, o.population(), dims, iters)
	if err != nil {
		return nil, err
	}

	cfg.Rand = rand.New(rand.NewSource(resolved.Seed))

	workers := o.MaxWorkers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	cfg.MaxWorkers = workers
	cfg.EnableParallel = workers > 1

	return cfg, nil
}

// Validate builds the configuration a run would use and checks it, without
// running anything.
//
// It exists for callers that decide whether to accept a request before they
// book the work it names. Without it the request is first checked inside
// Optimize, which for the HTTP fit API means a malformed request is accepted,
// claims the single fit slot, and fails asynchronously a moment later instead
// of being rejected as the bad request it always was.
//
// It takes the iteration budget because upstream validates the convergence
// block against it -- a minimum-iterations setting above the cap is an error --
// so a configuration cannot be checked without knowing the budget. The problem
// size is not needed: no validated setting depends on dimensionality, and the
// callers that need this answer know the budget but not yet the encoded
// parameter count.
func (o *MayflyOptimizer) Validate(maxIterations int) error {
	resolved := ResolvedMayfly{Variant: o.variant(), Seed: o.resolveSeed()}

	_, err := o.buildConfig(resolved, 1, maxInt(1, maxIterations))

	return err
}

// resolveVariant looks a variant up in the upstream registry, which is the
// single source of truth for variant names, so new variants are picked up
// without touching this wrapper.
func resolveVariant(name string) (mayfly.AlgorithmVariant, error) {
	selected := mayfly.NewVariant(name)
	if selected == nil {
		// ListVariants ranges over a map, so the order it returns is not
		// stable. Sorting keeps the error message reproducible, which matters
		// because it is asserted on in tests and read by users.
		names := mayfly.ListVariants()
		sort.Strings(names)

		return nil, fmt.Errorf("unsupported mayfly variant %q, want one of %s",
			name, strings.Join(names, ", "))
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

// validateMayflyConfig rejects configurations up front.
//
// Upstream's ValidateConfig owns every range in mayfly.Config -- the shared
// coefficients, the per-variant knobs, the convergence block, and the mating
// rules in validateOffspring that the check here used to approximate -- so the
// only thing left to police is what upstream deliberately allows and this
// wrapper does not: a population of one. A swarm that cannot mate is not a
// search, and the fit command already refuses it at the flag, so accepting it
// here would only let the HTTP and WASM front ends book a run that can never
// make progress.
//
// ValidateConfig tolerates a nil ObjectiveFunc, which is what lets it run at
// config-build time, before Optimize installs the tracker's evaluator.
func validateMayflyConfig(cfg *mayfly.Config) error {
	if cfg.NPop < 2 || cfg.NPopF < 2 {
		return fmt.Errorf("population must be at least 2, got %d males and %d females", cfg.NPop, cfg.NPopF)
	}

	return mayfly.ValidateConfig(cfg)
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
