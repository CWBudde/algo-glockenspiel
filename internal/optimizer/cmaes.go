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

	cmaes "github.com/CWBudde/go-cma-es"
)

// CMAESOptimizer wraps github.com/CWBudde/go-cma-es behind the shared
// optimizer interface.
//
// The dependency is pinned to v0.1.0. That version has a measured defect above
// a population of 256 in separable mode and 1024 in block mode, which does not
// bite here: the fit runs at Hansen's default, twelve at eighteen dimensions,
// and --cmaes-lambda is not expected past 64. v0.2.0 changes the sampling
// trajectory, so upgrading would make the numbers recorded before and after
// incomparable; it is deferred until the 8.6 campaign figures are on record.
type CMAESOptimizer struct {
	// Covariance selects the covariance representation: "separable" (the
	// default) learns the diagonal only, "block" learns a dense matrix per
	// group in BlockGroups.
	Covariance string

	// BlockGroups partitions the encoded dimensions into covariance blocks. It
	// is required in block mode and ignored otherwise;
	// ParamCodec.BlockGroups() builds the partition this model wants.
	BlockGroups [][]int

	// OnResolve reports the settings a run actually chose, once before the
	// first run starts. A nil callback disables the report. It exists for the
	// same reason MayflyOptimizer.OnResolve does: a zero seed is otherwise
	// chosen inside the wrapper and discarded, and the run cannot be repeated.
	OnResolve func(ResolvedCMAES)

	// Lambda is the population size. Zero takes Hansen's default,
	// 4 + floor(3 ln n), which is twelve at eighteen dimensions.
	Lambda int

	// InitialSigma is the step size in the unit cube. Zero takes 0.3, which is
	// the library's default and covers a third of the box.
	InitialSigma float64

	// Seed selects the random stream. Zero means "pick one and report it"
	// rather than "be unreproducible": see resolveSeed.
	Seed int64

	// MaxWorkers bounds parallel objective evaluation. Zero selects
	// runtime.NumCPU(). Parallel evaluation is safe because
	// ObjectiveFunction.Objective hands out per-goroutine render state, and it
	// is deterministic because the library draws every sample on the calling
	// goroutine before any evaluation starts.
	MaxWorkers int

	// RestartLimit bounds the number of runs. Zero restarts until the budget
	// is spent, N runs at most N times.
	RestartLimit int
}

// ResolvedCMAES is what a run settled on once every "choose one for me" input
// has been resolved. The CLI prints it and records the seed, the same way
// ResolvedMayfly is used.
type ResolvedCMAES struct {
	// Covariance is the covariance mode the run uses, after defaulting.
	Covariance string
	// Lambda is the population size, after Hansen's default is filled in.
	Lambda int
	// Seed is the value run zero's generator was constructed from, never zero.
	// Run k uses Seed + k.
	Seed int64
	// Sigma is the initial step size in the unit cube.
	Sigma float64
	// Workers is the number of goroutines evaluating one generation.
	Workers int
}

// The covariance modes this wrapper offers. The library also has a dense mode,
// which is not exposed: eighteen dimensions of audio render per evaluation
// make the dense update's extra work irrelevant, but its extra samples are
// not, and the block mode below is the structured middle ground this model has
// an actual grouping for.
const (
	covarianceSeparable = "separable"
	covarianceBlock     = "block"
)

const (
	// defaultCMAESSigma covers a third of the unit cube, which is Hansen's
	// recommendation for a box-bounded problem and the library's own default.
	defaultCMAESSigma = 0.3

	// uncappedRunIterations is the per-run iteration cap used when the caller
	// set no total. The library rejects a non-positive MaxIterations, so an
	// uncapped run still needs a number; this is the library's own default and
	// is far more than a run of this objective survives before a Hansen
	// criterion ends it and the next restart begins.
	uncappedRunIterations = 1000

	// minRestartBudgetFraction is the share of the time budget a fresh run
	// needs to be worth starting. A CMA-ES run spends its first generations
	// with a step size of sigma, sampling the whole box; one cut off after a
	// handful of generations returns nothing the previous runs did not already
	// have, and it delays the result past the deadline the caller set.
	minRestartBudgetFraction = 0.05
)

// Optimize runs CMA-ES in a normalized [0,1] search space and maps candidates
// back into bounds.
//
// The restart loop is the wrapper's own. The library's
// OptimizeWithRestartsContext implements IPOP and BIPOP against an evaluation
// budget, but the fit is bounded by wall-clock time, so the loop below runs
// cold restarts until the time budget rather than a sample count is spent.
//
// A run must be given a budget of its own: opts.MaxIterations, opts.TimeBudget
// or RestartLimit. A deadline on ctx is a stopping rule too, but it is not one
// of the three, and it is not accepted in their place: with none of the three
// the restart loop has no stopping rule it can see, so a caller that forgot
// all of them gets a search that only a cancelled context ever ends, which as
// a default is a hang rather than a search. A caller that genuinely wants
// "until I cancel" can say so with a restart or iteration count it does not
// expect to reach.
func (o *CMAESOptimizer) Optimize(ctx context.Context, objective ObjectiveFunc, initial []float64, bounds Bounds, opts OptimizeOptions) (*Result, error) {
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

	if opts.MaxIterations <= 0 && opts.TimeBudget <= 0 && o.RestartLimit <= 0 {
		return nil, fmt.Errorf(
			"cmaes needs a budget of its own: set max iterations, a time budget or a " +
				"restart limit, a context deadline alone is not one",
		)
	}

	start := time.Now()

	initial, err := bounds.Clamp(initial)
	if err != nil {
		return nil, err
	}

	normalizedInitial, err := bounds.Normalize(initial)
	if err != nil {
		return nil, err
	}

	resolved, err := o.resolve(len(initial))
	if err != nil {
		return nil, err
	}

	// The partition is only checkable here, where the dimension is known.
	if err = o.checkBlockGroups(resolved.Covariance, len(initial)); err != nil {
		return nil, err
	}

	if o.OnResolve != nil {
		o.OnResolve(resolved)
	}

	tracker := newCMAESTracker(objective, bounds, start, opts)
	tracker.seedBaseline(initial)

	return o.runRestarts(ctx, tracker, resolved, normalizedInitial, opts)
}

// runRestarts drives the restart loop and builds the result.
func (o *CMAESOptimizer) runRestarts(
	ctx context.Context,
	tracker *cmaesTracker,
	resolved ResolvedCMAES,
	normalizedInitial []float64,
	opts OptimizeOptions,
) (*Result, error) {
	completed := 0

	for {
		reason, stop := o.stopReason(ctx, tracker, opts, completed)
		if stop {
			return tracker.result(reason, completed), nil
		}

		// Run zero starts from the caller's point, which is what carries a
		// preset or a resumed checkpoint into the search. Every later run is
		// cold: a uniform mean makes it independent of the basin the previous
		// runs settled in, which is the whole point of a restart.
		//
		// The mean's seed counts downwards from below the resolved seed, which
		// keeps it clear of the library's own streams: those count upwards from
		// resolved.Seed, and two generators built from one seed produce one
		// sequence rather than two independent ones. It is still derived from
		// the reported seed, so a restart is reproducible on its own.
		mean := normalizedInitial
		if completed > 0 {
			mean = uniformMean(len(normalizedInitial), resolved.Seed-int64(completed)-1)
		}

		res, err := o.runOnce(ctx, tracker, resolved, mean, opts, completed)
		if err != nil {
			return tracker.abortedResult(ctx, err, completed)
		}

		tracker.finishRun(res)

		completed++
	}
}

// stopReason reports whether the loop should start another run, and why not.
func (o *CMAESOptimizer) stopReason(
	ctx context.Context,
	tracker *cmaesTracker,
	opts OptimizeOptions,
	completed int,
) (string, bool) {
	if ctx.Err() != nil {
		return "context_canceled", true
	}

	if o.RestartLimit > 0 && completed >= o.RestartLimit {
		return "restart_limit", true
	}

	if opts.MaxIterations > 0 && tracker.completedIterationCount() >= opts.MaxIterations {
		return "max_iterations", true
	}

	if opts.TimeBudget > 0 {
		remaining := opts.TimeBudget - tracker.elapsed()
		if float64(remaining) < minRestartBudgetFraction*float64(opts.TimeBudget) {
			return "time_budget", true
		}
	}

	return "", false
}

// runOnce performs one CMA-ES run under its own deadline.
func (o *CMAESOptimizer) runOnce(
	ctx context.Context,
	tracker *cmaesTracker,
	resolved ResolvedCMAES,
	mean []float64,
	opts OptimizeOptions,
	restart int,
) (*cmaes.Result, error) {
	// The time budget is expressed as a derived context so the library stops
	// mid-run rather than finishing a generation past the deadline.
	runCtx := ctx

	if opts.TimeBudget > 0 {
		var cancel context.CancelFunc

		runCtx, cancel = context.WithTimeout(ctx, opts.TimeBudget-tracker.elapsed())
		defer cancel()
	}

	cfg := o.config(resolved, len(mean), o.runIterations(tracker, opts), restart)
	cfg.ObjectiveFunc = tracker.evaluate

	tracker.beginRun(restart)

	return cmaes.OptimizeContext(runCtx, cfg,
		cmaes.WithInitialMean(mean, resolved.Sigma),
		cmaes.WithProgressObserver(tracker.observe),
	)
}

// runIterations is one run's share of the total iteration budget. The library
// rejects a non-positive cap, and stopReason has already established that the
// remainder is positive when a total was given.
func (o *CMAESOptimizer) runIterations(tracker *cmaesTracker, opts OptimizeOptions) int {
	if opts.MaxIterations <= 0 {
		return uncappedRunIterations
	}

	return opts.MaxIterations - tracker.completedIterationCount()
}

// config builds the configuration for one run.
func (o *CMAESOptimizer) config(resolved ResolvedCMAES, dims, iterations, restart int) *cmaes.Config {
	var cfg *cmaes.Config

	if resolved.Covariance == covarianceBlock {
		// BlockGroups takes precedence over BlockSize, which is why the block
		// size passed here is only the fallback the constructor demands.
		cfg = cmaes.NewBlockDiagonalConfig(dims, 1)
		cfg.BlockGroups = o.BlockGroups
	} else {
		cfg = cmaes.NewSeparableConfig(dims)
	}

	cfg.LowerBound = 0
	cfg.UpperBound = 1
	cfg.InitialSigma = resolved.Sigma
	cfg.Lambda = resolved.Lambda

	// Mu comes from the constructor's default lambda, so it has to follow an
	// overridden one; Hansen's ratio is half the population.
	cfg.Mu = maxInt(1, resolved.Lambda/2)
	cfg.MaxIterations = iterations
	cfg.MaxWorkers = resolved.Workers
	cfg.EnableParallel = true

	// Each run gets its own seed so that a restart is reproducible on its own,
	// and so that two cold runs do not replay one trajectory.
	seed := resolved.Seed + int64(restart)
	cfg.Seed = &seed

	return cfg
}

// uniformMean draws a cold run's starting mean uniformly in the unit cube. The
// caller derives the seed from the resolved one without colliding with the
// library's stream for any run, so the draw is reproducible from the reported
// seed alone and is not a replay of the samples the library then draws.
func uniformMean(dims int, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))

	mean := make([]float64, dims)
	for i := range mean {
		mean[i] = rng.Float64()
	}

	return mean
}

// resolveSeed turns a zero seed into a concrete one, for the same reason
// MayflyOptimizer.resolveSeed does: a reported seed has to be one the run
// actually used.
func (o *CMAESOptimizer) resolveSeed() int64 {
	if o.Seed != 0 {
		return o.Seed
	}

	return time.Now().UnixNano()
}

// resolve settles the covariance mode, the population, the step size, the seed
// and the worker count.
func (o *CMAESOptimizer) resolve(dims int) (ResolvedCMAES, error) {
	covariance := strings.ToLower(strings.TrimSpace(o.Covariance))
	if covariance == "" {
		covariance = covarianceSeparable
	}

	if covariance != covarianceSeparable && covariance != covarianceBlock {
		return ResolvedCMAES{}, fmt.Errorf(
			"unsupported cmaes covariance %q, want one of %s, %s",
			covariance, covarianceBlock, covarianceSeparable,
		)
	}

	if o.Lambda != 0 && o.Lambda < 2 {
		return ResolvedCMAES{}, fmt.Errorf("cmaes lambda must be at least 2 (got %d)", o.Lambda)
	}

	sigma := o.InitialSigma
	if sigma == 0 {
		sigma = defaultCMAESSigma
	}

	// A step size above the box is not a wider search, only a distribution
	// that spends its first generations outside the bounds being penalised.
	if !isFinite(sigma) || sigma <= 0 || sigma > 1 {
		return ResolvedCMAES{}, fmt.Errorf(
			"cmaes initial sigma must be in (0, 1] (got %v)", o.InitialSigma,
		)
	}

	lambda := o.Lambda
	if lambda == 0 {
		lambda = hansenPopulationSize(dims)
	}

	workers := o.MaxWorkers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	return ResolvedCMAES{
		Covariance: covariance,
		Lambda:     lambda,
		Seed:       o.resolveSeed(),
		Sigma:      sigma,
		Workers:    workers,
	}, nil
}

// hansenPopulationSize is 4 + floor(3 ln n), the default population the
// library uses and the tutorial recommends. It is twelve at the eighteen
// dimensions the phase 8.3 fit encodes to.
func hansenPopulationSize(dims int) int {
	if dims <= 0 {
		return 0
	}

	return 4 + int(math.Floor(3*math.Log(float64(dims))))
}

// checkBlockGroups verifies that block mode was given a partition of the
// encoded dimensions. The library checks the same thing, but only once a run
// starts; failing here keeps a misconfigured request from spending a baseline
// evaluation first.
func (o *CMAESOptimizer) checkBlockGroups(covariance string, dims int) error {
	if covariance != covarianceBlock {
		return nil
	}

	if len(o.BlockGroups) == 0 {
		return fmt.Errorf("cmaes block covariance requires block groups")
	}

	seen := make([]bool, dims)

	for groupIndex, group := range o.BlockGroups {
		if len(group) == 0 {
			return fmt.Errorf("cmaes block group %d is empty", groupIndex)
		}

		for _, coordinate := range group {
			if coordinate < 0 || coordinate >= dims {
				return fmt.Errorf(
					"cmaes block group %d holds coordinate %d outside [0, %d)",
					groupIndex, coordinate, dims,
				)
			}

			if seen[coordinate] {
				return fmt.Errorf("cmaes block groups hold coordinate %d twice", coordinate)
			}

			seen[coordinate] = true
		}
	}

	for coordinate, present := range seen {
		if !present {
			return fmt.Errorf("cmaes block groups are missing coordinate %d", coordinate)
		}
	}

	return nil
}

// Validate checks the settings a run would use, without running anything.
//
// It exists for the same callers MayflyOptimizer.Validate does: the HTTP fit
// API decides whether to accept a request before it books the single fit slot.
// It takes the iteration budget for signature symmetry with the other
// backends; no CMA-ES setting depends on it, because the per-run cap is
// derived from it rather than validated against it.
//
// The block-mode partition is deliberately not checked here. It has to
// partition [0, Dimension()), and the dimension is only known inside Optimize,
// where checkBlockGroups runs.
func (o *CMAESOptimizer) Validate(maxIterations int) error {
	// A dimension is needed to resolve Hansen's default population, and any
	// positive one answers every question this function can answer.
	_, err := o.resolve(1)

	return err
}

// cmaesTracker owns every mutable value shared with the library.
//
// The evaluation counter is written from several goroutines at once, so it is
// guarded. The best-so-far state is not: the library calls the progress
// observer synchronously on the goroutine that called OptimizeContext, which
// is the restart loop's own, so the best comes from a single writer. That is
// deliberate. Picking the best inside the parallel objective adapter would let
// two equally good candidates win in whichever order the workers finished,
// which is exactly the non-determinism the library's own sampling order avoids.
type cmaesTracker struct {
	objective ObjectiveFunc
	bounds    Bounds
	start     time.Time
	opts      OptimizeOptions

	mu    sync.Mutex
	evals int

	bestParams []float64
	bestCost   float64

	iterations int
	reports    int
	restart    int

	// completedIterations accumulates what earlier runs spent. Each run
	// numbers its iterations from one, so without it the run total would be
	// the last restart's. Evaluations need no such accumulator: the adapter
	// above is called once per library evaluation of every run.
	completedIterations int

	// converged records whether the last completed run ended on one of
	// Hansen's stopping criteria rather than on its own budget.
	converged bool
}

func newCMAESTracker(objective ObjectiveFunc, bounds Bounds, start time.Time, opts OptimizeOptions) *cmaesTracker {
	return &cmaesTracker{
		objective: objective,
		bounds:    bounds,
		start:     start,
		opts:      opts,
		bestCost:  math.Inf(1),
	}
}

// seedBaseline evaluates the caller's starting point so a run can never report
// a result worse than what it was given.
func (t *cmaesTracker) seedBaseline(initial []float64) {
	cost := t.objective(initial)

	t.mu.Lock()
	defer t.mu.Unlock()

	t.evals++

	t.bestParams = append([]float64(nil), initial...)
	t.bestCost = cost
}

// evaluate is the objective adapter the library calls, possibly in parallel.
//
// A non-finite cost is reported as invalidCost. The library itself survives
// one: it ranks candidates rather than arithmetically combining their costs,
// and its TolFun window skips non-finite scores. What it does not survive
// cleanly is a whole run inside an invalid region, where every candidate ties
// at +Inf, GlobalBest keeps its sentinel and comes back with no position at
// all. A large finite penalty keeps such a run ranking and losing, and the
// best-so-far bookkeeping below refuses invalidCost, so a penalised candidate
// can never be reported as the answer.
func (t *cmaesTracker) evaluate(pos []float64) float64 {
	// Counted before anything can go wrong with it: the library spent a
	// candidate on this call whether or not the wrapper can turn it into
	// encoded units, and Result.Evaluations is what the caller budgets with.
	t.mu.Lock()
	t.evals++
	t.mu.Unlock()

	actual, err := t.bounds.Denormalize(pos)
	if err != nil {
		return invalidCost
	}

	cost := t.objective(actual)

	if !isFinite(cost) {
		return invalidCost
	}

	return cost
}

// beginRun records which restart is about to start, for Progress.Restart.
func (t *cmaesTracker) beginRun(restart int) {
	t.restart = restart
}

// observe forwards one run's per-iteration progress. Progress.Iteration counts
// the callbacks this wrapper emits, not the library's iterations, per the
// Progress contract, and the cadence is taken on the run-spanning iteration
// count so a restart shorter than ReportEvery still reports.
func (t *cmaesTracker) observe(progress cmaes.Progress) {
	t.iterations = t.completedIterations + progress.Iteration
	t.updateBest(progress.Best)

	if t.opts.Report == nil || t.opts.ReportEvery <= 0 || t.iterations%t.opts.ReportEvery != 0 {
		return
	}

	t.reports++

	t.mu.Lock()
	evals := t.evals
	t.mu.Unlock()

	t.opts.Report(Progress{
		Iteration:           t.reports,
		OptimizerIterations: t.iterations,
		// The library reports the run's own best rather than the generation's,
		// so this is the current restart's best so far: it resets at every
		// restart, which is what makes it different from BestCost below.
		CurrentCost: progress.Best.Cost,
		BestCost:    t.bestCost,
		BestParams:  append([]float64(nil), t.bestParams...),
		Elapsed:     time.Since(t.start),
		Evaluations: evals,
		Restart:     t.restart,
	})
}

// updateBest folds one of the library's global bests into the run's.
//
// invalidCost is refused: it is the penalty evaluate hands back for a vector
// that does not decode, not a cost the objective ever returned.
func (t *cmaesTracker) updateBest(best cmaes.Best) {
	if len(best.Position) == 0 || best.Cost >= invalidCost || !(best.Cost < t.bestCost) {
		return
	}

	actual, err := t.bounds.Denormalize(best.Position)
	if err != nil {
		return
	}

	t.bestCost = best.Cost
	t.bestParams = actual
}

// finishRun folds one completed run's totals into the run's, so the next
// restart's per-run numbering continues where this one stopped.
func (t *cmaesTracker) finishRun(res *cmaes.Result) {
	if res == nil {
		return
	}

	t.updateBest(res.GlobalBest)

	t.completedIterations += res.IterationCount
	t.iterations = t.completedIterations
	t.converged = hansenCriterion(res.TerminationReason)
}

// hansenCriterion reports whether a termination reason is one of the
// distribution-derived stopping criteria rather than an exhausted budget or a
// cancellation. Those are the only reasons a restart loop can call a stop
// anything but "out of budget".
func hansenCriterion(reason cmaes.TerminationReason) bool {
	switch reason {
	case cmaes.TerminationTargetCost,
		cmaes.TerminationStagnation,
		cmaes.TerminationTolX,
		cmaes.TerminationTolFun,
		cmaes.TerminationTolXUp,
		cmaes.TerminationConditionNumber,
		cmaes.TerminationNoEffectAxis,
		cmaes.TerminationNoEffectCoord:
		return true
	default:
		// Everything else is a budget that ran out or an abort:
		// maximum_iterations, maximum_evaluations and cancelled.
		return false
	}
}

func (t *cmaesTracker) elapsed() time.Duration {
	return time.Since(t.start)
}

func (t *cmaesTracker) completedIterationCount() int {
	return t.completedIterations
}

// result builds the run's outcome. The stop reason is what separates a
// finished search from one that merely ran out of budget: a loop that restarts
// until the clock stops it has no claim on convergence, whatever its last run
// ended on, so only "restart_limit" can report one.
func (t *cmaesTracker) result(reason string, completed int) *Result {
	t.mu.Lock()
	evals := t.evals
	t.mu.Unlock()

	return &Result{
		BestParams:  append([]float64(nil), t.bestParams...),
		BestCost:    t.bestCost,
		Iterations:  t.iterations,
		Elapsed:     time.Since(t.start),
		Converged:   reason == "restart_limit" && t.converged,
		StopReason:  reason,
		Evaluations: evals,
		Restarts:    completed,
	}
}

// abortedResult reports the best found before a run failed. A cancelled run
// comes back as a result rather than an error, so the only errors reaching
// here are a context that was already done when the run started and a rejected
// configuration; the latter is the caller's to see.
func (t *cmaesTracker) abortedResult(ctx context.Context, runErr error, completed int) (*Result, error) {
	switch {
	case ctx.Err() != nil:
		return t.result("context_canceled", completed), nil
	case errors.Is(runErr, context.DeadlineExceeded):
		return t.result("time_budget", completed), nil
	default:
		return nil, runErr
	}
}
