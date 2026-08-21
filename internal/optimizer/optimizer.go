package optimizer

import (
	"context"
	"time"
)

// ObjectiveFunc evaluates an encoded parameter vector and returns its cost.
//
// Implementations must be safe for concurrent use: optimizer backends are free
// to evaluate several candidates in parallel. Use ObjectiveFunction.Objective,
// which hands out per-goroutine render state.
type ObjectiveFunc func(params []float64) float64

// Optimizer runs a search over encoded parameters.
//
// Optimize must return as soon as ctx is done, reporting whatever it has found
// so far rather than an error. It must also respect opts.TimeBudget; callers
// may rely on either mechanism.
type Optimizer interface {
	Optimize(ctx context.Context, objective ObjectiveFunc, initial []float64, bounds Bounds, opts OptimizeOptions) (*Result, error)
}

// OptimizeOptions controls shared optimizer behavior.
type OptimizeOptions struct {
	MaxIterations int
	TimeBudget    time.Duration
	ReportEvery   int
	Report        func(Progress)
}

// Result describes the outcome of an optimization run.
//
// BestParams and BestCost always correspond: BestCost is the value the
// objective returned for BestParams, and BestParams satisfies the bounds.
type Result struct {
	BestParams []float64
	BestCost   float64

	// Iterations is the number of iterations actually completed, which is less
	// than OptimizeOptions.MaxIterations for an aborted or early-stopped run.
	Iterations int

	Elapsed time.Duration

	// Converged reports that the run stopped because a convergence criterion
	// fired, not because it exhausted its iteration or time budget. A
	// metaheuristic cannot prove optimality, so this is never a quality claim.
	Converged bool

	// StopReason names the criterion that ended the run, using the backend's
	// own vocabulary plus "context_canceled" and "time_budget" for aborts.
	StopReason  string
	Evaluations int
}

// Progress describes one optimizer progress update.
type Progress struct {
	// Iteration counts progress callbacks, not optimizer iterations and not
	// objective evaluations. Backends differ in how often they can report, so
	// this is the only value callers can use for checkpoint cadence and for
	// subtracting resumed work from a budget.
	Iteration   int
	CurrentCost float64
	BestCost    float64
	BestParams  []float64
	Elapsed     time.Duration
	Evaluations int
}
