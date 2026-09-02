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

	// Restarts is the number of independent searches the run completed. Only
	// the CMA-ES backend restarts, so it is zero for every other backend and
	// for a run that was cut before its first search finished. "Completed" is
	// not the same as "converged": a run the clock or the context ended is
	// counted too whenever the backend hands back its partial result instead of
	// an error, because the loop records that result like any other.
	Restarts int
}

// Progress describes one optimizer progress update.
type Progress struct {
	// Iteration counts progress callbacks, not optimizer iterations and not
	// objective evaluations. It orders the reports a run produced, which is
	// what the fit command's checkpoint file names are derived from. It is not
	// a budget: subtracting it from an iteration cap would charge a run with
	// --report-every 10 a tenth of the work it did, and it cannot pace a
	// checkpoint cadence either, because backends differ in how often they
	// report. Use OptimizerIterations for both.
	Iteration int

	// OptimizerIterations is the backend's own iteration count at the time of
	// the report, in the same unit as Result.Iterations and
	// OptimizeOptions.MaxIterations. Checkpoints persist this so a resumed run
	// can subtract the work already done from its budget; Iteration cannot
	// serve that purpose because it counts reports, not iterations.
	OptimizerIterations int

	CurrentCost float64
	BestCost    float64
	BestParams  []float64
	Elapsed     time.Duration
	Evaluations int

	// Restart is the zero-based index of the search in progress. Only the
	// CMA-ES backend restarts, so it is zero for every other backend.
	Restart int
}
