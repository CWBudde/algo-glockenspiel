package optimizer

import (
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

// Optimize runs bounded Nelder-Mead optimization over the encoded parameter space.
func (o *SimpleOptimizer) Optimize(objective ObjectiveFunc, initial []float64, bounds Bounds, opts OptimizeOptions) (*Result, error) {
	if objective == nil {
		return nil, fmt.Errorf("objective cannot be nil")
	}

	if len(initial) == 0 {
		return nil, fmt.Errorf("initial parameters cannot be empty")
	}

	if err := bounds.CheckVector(initial); err != nil {
		return nil, err
	}

	start := time.Now()

	initial, err := bounds.Clamp(initial)
	if err != nil {
		return nil, err
	}

	tracker := newProgressTracker(initial, objective(initial), opts)
	problem := gonumoptimize.Problem{
		Func: func(x []float64) float64 {
			bounded, err := bounds.Mirror(x)
			if err != nil {
				return math.Inf(1)
			}

			cost := objective(bounded)
			tracker.observeEval(bounded, cost)

			return cost
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

	rawResult, err := gonumoptimize.Minimize(problem, initial, settings, method)
	if err != nil {
		return nil, err
	}

	bestParams, err := bounds.Mirror(rawResult.X)
	if err != nil {
		return nil, err
	}

	bestCost := rawResult.F

	if tracker.bestParams != nil {
		bestParams = append([]float64(nil), tracker.bestParams...)
		bestCost = tracker.bestCost
	}

	return &Result{
		BestParams:  bestParams,
		BestCost:    bestCost,
		Iterations:  rawResult.MajorIterations,
		Elapsed:     time.Since(start),
		Converged:   !rawResult.Status.Early(),
		StopReason:  rawResult.Status.String(),
		Evaluations: rawResult.FuncEvaluations,
	}, nil
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
	start       time.Time
	reportEvery int
	report      func(Progress)

	bestParams []float64
	bestCost   float64
	evals      int
}

func newProgressTracker(initial []float64, initialCost float64, opts OptimizeOptions) *progressTracker {
	return &progressTracker{
		start:       time.Now(),
		reportEvery: opts.ReportEvery,
		report:      opts.Report,
		bestParams:  append([]float64(nil), initial...),
		bestCost:    initialCost,
		evals:       1,
	}
}

func (t *progressTracker) Init() error { return nil }

func (t *progressTracker) Record(loc *gonumoptimize.Location, op gonumoptimize.Operation, stats *gonumoptimize.Stats) error {
	if op != gonumoptimize.MajorIteration {
		return nil
	}

	if loc != nil && loc.F < t.bestCost {
		t.bestCost = loc.F
		t.bestParams = append(t.bestParams[:0], loc.X...)
	}

	if t.report == nil || t.reportEvery <= 0 || stats == nil || stats.MajorIterations%t.reportEvery != 0 {
		return nil
	}

	currentCost := math.NaN()
	if loc != nil {
		currentCost = loc.F
	}

	t.report(Progress{
		Iteration:   stats.MajorIterations,
		CurrentCost: currentCost,
		BestCost:    t.bestCost,
		BestParams:  append([]float64(nil), t.bestParams...),
		Elapsed:     stats.Runtime,
		Evaluations: stats.FuncEvaluations,
	})

	return nil
}

func (t *progressTracker) observeEval(x []float64, cost float64) {
	t.evals++
	if cost < t.bestCost {
		t.bestCost = cost
		t.bestParams = append(t.bestParams[:0], x...)
	}
}
