package optimizer

import "time"

// ObjectiveFunc evaluates an encoded parameter vector and returns its cost.
type ObjectiveFunc func(params []float64) float64

// Optimizer runs a search over encoded parameters.
type Optimizer interface {
	Optimize(objective ObjectiveFunc, initial []float64, bounds Bounds, opts OptimizeOptions) (*Result, error)
}

// OptimizeOptions controls shared optimizer behavior.
type OptimizeOptions struct {
	MaxIterations int
	TimeBudget    time.Duration
	ReportEvery   int
}

// Result describes the outcome of an optimization run.
type Result struct {
	BestParams  []float64
	BestCost    float64
	Iterations  int
	Elapsed     time.Duration
	Converged   bool
	StopReason  string
	Evaluations int
}
