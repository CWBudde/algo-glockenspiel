package optimizer

import (
	"context"
	"fmt"
	"math"
	"time"

	gonumoptimize "gonum.org/v1/gonum/optimize"
)

const (
	defaultFunctionAbsoluteTolerance = 1e-8
	defaultFunctionRelativeTolerance = 1e-8
	defaultStallIterations           = 50
	defaultSimplexSize               = 0.05
)

// SimpleOptimizer wraps Gonum's Nelder-Mead implementation.
type SimpleOptimizer struct {
	SimplexSize       float64
	AbsoluteTolerance float64
	RelativeTolerance float64
	StallIterations   int
}

// Optimize runs bounded Nelder-Mead optimization over the normalized parameter space.
func (o *SimpleOptimizer) Optimize(ctx context.Context, objective ObjectiveFunc, initial []float64, bounds Bounds, opts OptimizeOptions) (*Result, error) {
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

	// Nelder-Mead runs in the unit cube so that SimplexSize means the same
	// relative step on every axis; in raw encoded units the initial simplex was
	// degenerate along the widest axis.
	normalized, err := bounds.Normalize(initial)
	if err != nil {
		return nil, err
	}

	tracker := newProgressTracker(ctx, initial, objective(initial), start, opts)
	problem := gonumoptimize.Problem{
		Func: func(x []float64) float64 {
			return tracker.evaluate(objective, bounds, x)
		},
	}

	settings := &gonumoptimize.Settings{
		MajorIterations: opts.MaxIterations,
		Runtime:         opts.TimeBudget,
		Converger: &gonumoptimize.FunctionConverge{
			Absolute:   o.absoluteTolerance(),
			Relative:   o.relativeTolerance(),
			Iterations: o.stallIterations(),
		},
		Recorder: tracker,
	}

	method := &gonumoptimize.NelderMead{
		SimplexSize: o.simplexSize(),
	}

	rawResult, err := gonumoptimize.Minimize(problem, normalized, settings, method)
	if err != nil && ctx.Err() == nil {
		return nil, err
	}

	return tracker.result(ctx, rawResult, start), nil
}

func (o *SimpleOptimizer) simplexSize() float64 {
	if o.SimplexSize > 0 {
		return o.SimplexSize
	}

	return defaultSimplexSize
}

func (o *SimpleOptimizer) absoluteTolerance() float64 {
	if o.AbsoluteTolerance > 0 {
		return o.AbsoluteTolerance
	}

	return defaultFunctionAbsoluteTolerance
}

func (o *SimpleOptimizer) relativeTolerance() float64 {
	if o.RelativeTolerance > 0 {
		return o.RelativeTolerance
	}

	return defaultFunctionRelativeTolerance
}

func (o *SimpleOptimizer) stallIterations() int {
	if o.StallIterations > 0 {
		return o.StallIterations
	}

	return defaultStallIterations
}

type progressTracker struct {
	ctx         context.Context
	start       time.Time
	reportEvery int
	report      func(Progress)

	// bestParams and bestCost are only ever written together from evaluate, so
	// the reported cost is always a genuine evaluation of the reported params.
	// The gonum Recorder deliberately does not touch them: its Location holds a
	// search-space point and a penalized cost, which used to race the objective
	// closure for ownership of these fields.
	bestParams []float64
	bestCost   float64
	evals      int
	reports    int
}

func newProgressTracker(ctx context.Context, initial []float64, initialCost float64, start time.Time, opts OptimizeOptions) *progressTracker {
	return &progressTracker{
		ctx:         ctx,
		start:       start,
		reportEvery: opts.ReportEvery,
		report:      opts.Report,
		bestParams:  append([]float64(nil), initial...),
		bestCost:    initialCost,
		evals:       1,
	}
}

// evaluate maps a search-space point back into encoded units and scores it.
//
// Points outside the unit cube are clamped rather than mirrored: mirroring made
// the objective a many-to-one folded map that Nelder-Mead reflects across,
// chasing ghost minima outside the feasible region. Clamping alone leaves a
// plateau, so the distance outside the cube is added as a penalty that grows
// with the excursion and gives the simplex a gradient back inwards.
func (t *progressTracker) evaluate(objective ObjectiveFunc, bounds Bounds, x []float64) float64 {
	bounded, err := bounds.Denormalize(x)
	if err != nil {
		return math.Inf(1)
	}

	cost := objective(bounded)

	t.evals++
	if cost < t.bestCost {
		t.bestCost = cost
		t.bestParams = append(t.bestParams[:0], bounded...)
	}

	excess := unitCubeExcess(x)
	if excess == 0 {
		return cost
	}

	// Scale the penalty with the local cost so it dominates regardless of the
	// objective's magnitude, but never vanishes when the clamped cost is zero.
	return cost + (1+math.Abs(cost))*excess
}

// unitCubeExcess returns the squared distance from x to the unit cube.
func unitCubeExcess(x []float64) float64 {
	excess := 0.0

	for _, v := range x {
		switch {
		case v < 0:
			excess += v * v
		case v > 1:
			excess += (v - 1) * (v - 1)
		}
	}

	return excess
}

func (t *progressTracker) Init() error { return nil }

func (t *progressTracker) Record(loc *gonumoptimize.Location, op gonumoptimize.Operation, stats *gonumoptimize.Stats) error {
	// Gonum has no context support, so cancellation is surfaced by failing the
	// recorder; Minimize still returns the result it has accumulated.
	if err := t.ctx.Err(); err != nil {
		return err
	}

	if op != gonumoptimize.MajorIteration {
		return nil
	}

	if t.report == nil || t.reportEvery <= 0 || stats == nil || stats.MajorIterations%t.reportEvery != 0 {
		return nil
	}

	currentCost := math.NaN()
	if loc != nil {
		currentCost = loc.F
	}

	t.reports++

	t.report(Progress{
		Iteration:   t.reports,
		CurrentCost: currentCost,
		BestCost:    t.bestCost,
		BestParams:  append([]float64(nil), t.bestParams...),
		Elapsed:     stats.Runtime,
		Evaluations: stats.FuncEvaluations,
	})

	return nil
}

func (t *progressTracker) result(ctx context.Context, raw *gonumoptimize.Result, start time.Time) *Result {
	result := &Result{
		BestParams:  append([]float64(nil), t.bestParams...),
		BestCost:    t.bestCost,
		Elapsed:     time.Since(start),
		Evaluations: t.evals,
		StopReason:  "canceled",
	}

	if raw != nil {
		result.Iterations = raw.MajorIterations
		result.Converged = !raw.Status.Early()
		result.StopReason = raw.Status.String()

		if raw.FuncEvaluations > result.Evaluations {
			result.Evaluations = raw.FuncEvaluations
		}
	}

	if ctx.Err() != nil {
		result.Converged = false
		result.StopReason = "context_canceled"
	}

	return result
}
