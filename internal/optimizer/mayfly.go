package optimizer

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/cwbudde/mayfly"
)

// MayflyOptimizer wraps github.com/cwbudde/mayfly behind the shared optimizer interface.
type MayflyOptimizer struct {
	Variant    string
	Population int
	Seed       int64
}

// Optimize runs Mayfly in a normalized [0,1] search space and maps candidates back into bounds.
func (o *MayflyOptimizer) Optimize(objective ObjectiveFunc, initial []float64, bounds Bounds, opts OptimizeOptions) (*Result, error) {
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

	pop := o.population()
	cfg, err := newMayflyConfig(o.variant(), pop, len(initial), maxInt(1, opts.MaxIterations))
	if err != nil {
		return nil, err
	}
	if o.Seed != 0 {
		cfg.Rand = rand.New(rand.NewSource(o.Seed))
	}

	deadline := time.Time{}
	if opts.TimeBudget > 0 {
		deadline = start.Add(opts.TimeBudget)
	}

	bestParams := append([]float64(nil), initial...)
	bestCost := objective(initial)
	evals := 1
	lastReportEval := 0

	cfg.ObjectiveFunc = func(pos []float64) float64 {
		evals++
		actual := denormalizeVector(pos, bounds)
		if !deadline.IsZero() && time.Now().After(deadline) {
			return bestCost + 1
		}
		cost := objective(actual)
		if cost < bestCost {
			bestCost = cost
			bestParams = append(bestParams[:0], actual...)
		}
		if opts.Report != nil && opts.ReportEvery > 0 && evals-lastReportEval >= opts.ReportEvery {
			lastReportEval = evals
			opts.Report(Progress{
				Iteration:   evals,
				CurrentCost: cost,
				BestCost:    bestCost,
				BestParams:  append([]float64(nil), bestParams...),
				Elapsed:     time.Since(start),
				Evaluations: evals,
			})
		}
		if !isFinite(cost) {
			return math.Inf(1)
		}
		return cost
	}

	res, err := runMayfly(cfg)
	if err != nil {
		return nil, err
	}
	if res != nil && res.FuncEvalCount > evals {
		evals = res.FuncEvalCount
	}

	stopReason := "MayflyIterations"
	converged := true
	if !deadline.IsZero() && time.Now().After(deadline) {
		stopReason = "RuntimeLimit"
		converged = false
	}

	return &Result{
		BestParams:  append([]float64(nil), bestParams...),
		BestCost:    bestCost,
		Iterations:  cfg.MaxIterations,
		Elapsed:     time.Since(start),
		Converged:   converged,
		StopReason:  stopReason,
		Evaluations: evals,
	}, nil
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

func newMayflyConfig(variant string, pop int, dims int, iters int) (*mayfly.Config, error) {
	var cfg *mayfly.Config
	switch variant {
	case "ma":
		cfg = mayfly.NewDefaultConfig()
	case "desma":
		cfg = mayfly.NewDESMAConfig()
	case "olce":
		cfg = mayfly.NewOLCEConfig()
	case "eobbma":
		cfg = mayfly.NewEOBBMAConfig()
	case "gsasma":
		cfg = mayfly.NewGSASMAConfig()
	case "mpma":
		cfg = mayfly.NewMPMAConfig()
	case "aoblmoa":
		cfg = mayfly.NewAOBLMOAConfig()
	default:
		return nil, fmt.Errorf("unsupported mayfly variant %q", variant)
	}
	cfg.ProblemSize = dims
	cfg.LowerBound = 0.0
	cfg.UpperBound = 1.0
	cfg.MaxIterations = iters
	cfg.NPop = pop
	cfg.NPopF = pop
	cfg.NC = 2 * pop
	cfg.NM = maxInt(1, int(math.Round(0.05*float64(pop))))
	return cfg, nil
}

func runMayfly(cfg *mayfly.Config) (_ *mayfly.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("mayfly panic: %v", r)
		}
	}()
	return mayfly.Optimize(cfg)
}

func denormalizeVector(pos []float64, bounds Bounds) []float64 {
	out := make([]float64, len(pos))
	for i, v := range pos {
		out[i] = bounds.Ranges[i].Denormalize(v)
	}
	return out
}

func normalizeVector(values []float64, bounds Bounds) []float64 {
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = bounds.Ranges[i].Normalize(v)
	}
	return out
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
