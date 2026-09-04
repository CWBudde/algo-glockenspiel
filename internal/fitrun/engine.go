package fitrun

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// buildOptimizer selects and configures the backend, and returns the merged
// mayfly tuning document alongside it so the checkpoints can record the
// configuration the run actually had rather than the flags that asked for it.
//
// The callbacks write the resolved seed, width, variant and population back
// into chosen before the search starts. Without them a run started with a zero
// seed cannot be repeated: the wrapper draws one, uses it, and would otherwise
// throw it away.
func buildOptimizer(spec Spec, prepared *preparation, chosen *Resolved, out io.Writer) (optimizer.Optimizer, *optimizer.MayflyTuning, error) {
	switch spec.Engine.Name {
	case EngineSimple:
		// The simple backend draws nothing of its own: chosen is already
		// final (Seed and Workers were resolved before this call), so
		// OnResolve fires here rather than from a callback that will never
		// come.
		if spec.OnResolve != nil {
			spec.OnResolve(*chosen)
		}

		return &optimizer.SimpleOptimizer{}, nil, nil
	case EngineMayfly:
		return buildMayfly(spec, chosen, out)
	case EngineCMAES:
		return buildCMAES(spec, prepared, chosen, out)
	default:
		return nil, nil, fmt.Errorf("unknown engine %q", spec.Engine.Name)
	}
}

func buildMayfly(spec Spec, chosen *Resolved, out io.Writer) (optimizer.Optimizer, *optimizer.MayflyTuning, error) {
	settings := spec.Engine.Mayfly
	tuning := scheduleOverlay(settings)

	chosen.Population = mayflyPopulation(settings, tuning)
	chosen.Variant = settings.Variant

	backend := &optimizer.MayflyOptimizer{
		Variant:    settings.Variant,
		Preset:     settings.Preset,
		Population: settings.Population,
		Seed:       spec.Seed,
		MaxWorkers: spec.Workers,
		Tuning:     tuning,
		OnResolve: func(report optimizer.ResolvedMayfly) {
			chosen.Seed = report.Seed
			chosen.Workers = report.Workers
			chosen.Variant = report.Variant

			_, _ = fmt.Fprintf(out, "mayfly: variant=%s seed=%d rounds=%dx%d workers=%d\n",
				report.Variant, report.Seed, report.Rounds, report.IterationsPerRound, report.Workers)

			if spec.OnResolve != nil {
				spec.OnResolve(*chosen)
			}
		},
	}

	if err := backend.Validate(spec.MaxIterations); err != nil {
		return nil, nil, err
	}

	return backend, tuning, nil
}

func buildCMAES(spec Spec, prepared *preparation, chosen *Resolved, out io.Writer) (optimizer.Optimizer, *optimizer.MayflyTuning, error) {
	settings := spec.Engine.CMAES
	chosen.Covariance = settings.Covariance

	backend := &optimizer.CMAESOptimizer{
		Covariance: settings.Covariance,
		// The partition can only come from the codec: block mode learns one
		// dense matrix per mode, and the codec is what knows which encoded
		// coordinates a mode owns.
		BlockGroups:    prepared.objective.Codec().BlockGroups(),
		Lambda:         settings.Lambda,
		InitialSigma:   settings.Sigma,
		Seed:           spec.Seed,
		MaxWorkers:     spec.Workers,
		RestartLimit:   settings.RestartLimit,
		RunEvaluations: settings.RunEvaluations,
		LambdaGrowth:   settings.LambdaGrowth,
		OnResolve: func(report optimizer.ResolvedCMAES) {
			chosen.Seed = report.Seed
			chosen.Workers = report.Workers
			chosen.Lambda = report.Lambda
			chosen.Covariance = report.Covariance

			_, _ = fmt.Fprintf(out, "cmaes: covariance=%s lambda=%d sigma=%g seed=%d workers=%d\n",
				report.Covariance, report.Lambda, report.Sigma, report.Seed, report.Workers)

			if spec.OnResolve != nil {
				spec.OnResolve(*chosen)
			}
		},
	}

	if err := backend.Validate(spec.MaxIterations); err != nil {
		return nil, nil, err
	}

	return backend, nil, nil
}

// scheduleOverlay folds the scalar epoch and restart counts into the tuning
// document, the way the fit command's flags do. One epoch and no restarts is
// what a single search already is, so untouched settings write no schedule
// block at all and the document is handed on as it stands. A setting that was
// touched wins over the document: the overlay carries only the fields the
// caller set, and Overlay lets those replace the document's own, so a spec
// that names five epochs runs five epochs whatever the tuning file says.
func scheduleOverlay(settings MayflySettings) *optimizer.MayflyTuning {
	if settings.Epochs <= 1 && settings.Restarts <= 0 {
		return settings.Tuning
	}

	overlay := &optimizer.MayflyTuning{Schedule: &optimizer.MayflySchedule{}}

	if settings.Epochs > 1 {
		epochs := settings.Epochs
		overlay.Schedule.Epochs = &epochs
	}

	if settings.Restarts > 0 {
		restarts := settings.Restarts
		overlay.Schedule.Restarts = &restarts
	}

	return settings.Tuning.Overlay(overlay)
}

// mayflyPopulation is the swarm size the run will use. The wrapper resolves it
// privately and reports no population back, so the summary reproduces the same
// two rules: a tuning document's npop wins, and a population below two takes
// the wrapper's own default.
func mayflyPopulation(settings MayflySettings, tuning *optimizer.MayflyTuning) int {
	if tuning != nil && tuning.NPop != nil && *tuning.NPop > 0 {
		return *tuning.NPop
	}

	if settings.Population >= 2 {
		return settings.Population
	}

	return defaultMayflyPopulation
}

// saveCheckpoint writes the run's single checkpoint file. It is overwritten
// rather than numbered: a campaign job resumes from the newest state or not at
// all, and one file per report would be thousands of files per campaign.
func saveCheckpoint(
	spec Spec,
	prepared *preparation,
	chosen *Resolved,
	tuning *optimizer.MayflyTuning,
	iteration, optimizerIterations int,
	params []float64,
	cost float64,
) error {
	if len(params) == 0 {
		return nil
	}

	return optimizer.SaveCheckpoint(filepath.Join(spec.Dir, FileCheckpoint), &optimizer.Checkpoint{
		Version:             optimizer.CheckpointVersion,
		Iteration:           iteration,
		OptimizerIterations: optimizerIterations,
		BestCost:            cost,
		BestParams:          append([]float64(nil), params...),
		Optimizer:           spec.Engine.Name,
		Metric:              string(spec.Metric),
		State:               checkpointState(spec, prepared, chosen, tuning),
	})
}

// checkpointState records what a resume would have to reproduce: the backend's
// configuration, the mode choice the codec was built around, and the width the
// run resolved.
func checkpointState(spec Spec, prepared *preparation, chosen *Resolved, tuning *optimizer.MayflyTuning) *optimizer.OptimizerState {
	modes := optimizer.KeepTemplateModes
	if prepared.seededModes > 0 {
		modes = prepared.seededModes
	}

	state := &optimizer.OptimizerState{
		Kind:    spec.Engine.Name,
		Modes:   modes,
		Workers: chosen.Workers,
	}

	switch spec.Engine.Name {
	case EngineMayfly:
		state.Mayfly = &optimizer.MayflyCheckpointEnv{
			Variant:    chosen.Variant,
			Population: spec.Engine.Mayfly.Population,
			Seed:       chosen.Seed,
			Epochs:     spec.Engine.Mayfly.Epochs,
			Restarts:   spec.Engine.Mayfly.Restarts,
			Tuning:     tuning,
		}
	case EngineCMAES:
		state.CMAES = &optimizer.CMAESCheckpointEnv{
			Covariance:     chosen.Covariance,
			Lambda:         spec.Engine.CMAES.Lambda,
			Sigma:          spec.Engine.CMAES.Sigma,
			Seed:           chosen.Seed,
			Restarts:       spec.Engine.CMAES.RestartLimit,
			RunEvaluations: spec.Engine.CMAES.RunEvaluations,
			LambdaGrowth:   spec.Engine.CMAES.LambdaGrowth,
		}
	}

	return state
}
