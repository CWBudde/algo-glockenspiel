package optimizer

import (
	"context"
	"fmt"
	"time"
)

// The polish engines. PolishEngineNone is what a caller passes when the stage
// is switched off; Polish rejects it rather than silently doing nothing, so a
// caller that forgot to check gets told.
const (
	PolishEngineNone       = "none"
	PolishEngineNelderMead = "nelder-mead"
	PolishEngineCMAES      = "cmaes"

	// defaultPolishSigma is a fiftieth of the normalized box. The stage exists
	// to walk the incumbent downhill, not to search again, so the step has to
	// be small enough that the first simplex or generation still lands in the
	// basin the main search found.
	defaultPolishSigma = 0.02
)

// PolishOptions configures the local refinement stage.
type PolishOptions struct {
	// Engine is PolishEngineNelderMead or PolishEngineCMAES.
	Engine string

	// Sigma is the initial step in the normalized box: the Nelder-Mead simplex
	// size or the CMA-ES step size. Zero takes defaultPolishSigma; anything
	// above one is refused, because a step wider than the box is a global
	// search rather than a polish.
	Sigma float64

	MaxIterations int
	TimeBudget    time.Duration

	// Seed selects the CMA-ES random stream and is ignored by Nelder-Mead,
	// which is deterministic.
	Seed int64

	// MaxWorkers bounds parallel evaluation in the CMA-ES engine. Zero selects
	// runtime.NumCPU().
	MaxWorkers int

	Report func(Progress)
}

// PolishResult is what the stage did, whether or not it changed anything.
//
// Params is the vector the caller should keep: the polished one when Accepted,
// the incumbent otherwise. The four costs are reported either way, so an
// operator can see a polish that lowered the waveform term while raising the
// primary cost, which is exactly the case the acceptance rule rejects.
type PolishResult struct {
	Params []float64

	PrimaryBefore float64
	PrimaryAfter  float64
	PolishBefore  float64
	PolishAfter   float64

	Accepted bool
	Engine   string

	Iterations  int
	Evaluations int
	Elapsed     time.Duration
}

// Polish refines an incumbent locally under the polish profile and keeps the
// result only if it is better under the primary objective.
//
// The stage searches under MetricPolish, which weights the waveform term for a
// local stage, but acceptance is judged under the objective the fit was
// started with. Every report, `distance` and the checkpoint score the preset
// under that primary metric, so a polish that traded primary cost for waveform
// cost would ship a regression the reports go on to show. Only a strictly
// lower primary cost replaces the incumbent.
//
// A cancelled context is not an error: the stage returns the incumbent
// unaccepted, exactly as if the engine had found nothing.
func Polish(ctx context.Context, primary *ObjectiveFunction, incumbent []float64, opts PolishOptions) (*PolishResult, error) {
	if primary == nil {
		return nil, fmt.Errorf("primary objective cannot be nil")
	}

	if len(incumbent) == 0 {
		return nil, fmt.Errorf("incumbent parameters cannot be empty")
	}

	if ctx == nil {
		ctx = context.Background()
	}

	// A step wider than the box is a global search, not a polish, whichever
	// engine runs it. Zero still means "take the default", which is how the
	// stage is asked for without an opinion about the step.
	if opts.Sigma > 1 {
		return nil, fmt.Errorf("polish sigma must be in (0, 1] (got %g)", opts.Sigma)
	}

	sigma := opts.Sigma
	if sigma <= 0 {
		sigma = defaultPolishSigma
	}

	engine, err := polishEngine(opts, sigma)
	if err != nil {
		return nil, err
	}

	// The polish objective is the primary one under another metric: the same
	// template, reference, bounds and codec, so the incumbent means the same
	// vector to both and the reference is not measured again.
	polish, err := primary.WithMetric(MetricPolish)
	if err != nil {
		return nil, err
	}

	result := &PolishResult{
		Params:        append([]float64(nil), incumbent...),
		PrimaryBefore: primary.Evaluate(incumbent),
		PolishBefore:  polish.Evaluate(incumbent),
		Engine:        opts.Engine,
	}
	result.PrimaryAfter = result.PrimaryBefore
	result.PolishAfter = result.PolishBefore

	start := time.Now()

	run, err := engine.Optimize(ctx, polish.Objective(), incumbent, primary.Codec().EncodedBounds(), OptimizeOptions{
		MaxIterations: opts.MaxIterations,
		TimeBudget:    opts.TimeBudget,
		Report:        opts.Report,
	})

	result.Elapsed = time.Since(start)

	if err != nil {
		// A backend that was cut mid-run may report the abort as an error. The
		// incumbent is still the answer, so this is a stage that did nothing
		// rather than a failed fit.
		if ctx.Err() != nil {
			return result, nil
		}

		return nil, err
	}

	result.Iterations = run.Iterations
	result.Evaluations = run.Evaluations

	if len(run.BestParams) != len(incumbent) {
		return result, nil
	}

	candidate := run.BestParams
	result.PrimaryAfter = primary.Evaluate(candidate)
	result.PolishAfter = polish.Evaluate(candidate)

	if result.PrimaryAfter < result.PrimaryBefore {
		result.Accepted = true

		result.Params = append([]float64(nil), candidate...)
	}

	return result, nil
}

// polishEngine builds the backend the stage runs. CMA-ES is given a restart
// limit of one: a cold restart would sample the whole box again, which is the
// global search this stage is not.
func polishEngine(opts PolishOptions, sigma float64) (Optimizer, error) {
	switch opts.Engine {
	case PolishEngineNelderMead:
		return &SimpleOptimizer{SimplexSize: sigma}, nil
	case PolishEngineCMAES:
		return &CMAESOptimizer{
			// Separable on purpose: the stage is a short local descent, and
			// there are not enough generations in it to learn a dense block.
			Covariance:   covarianceSeparable,
			InitialSigma: sigma,
			Seed:         opts.Seed,
			MaxWorkers:   opts.MaxWorkers,
			RestartLimit: 1,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported polish engine %q: use %s or %s", opts.Engine, PolishEngineNelderMead, PolishEngineCMAES)
	}
}
