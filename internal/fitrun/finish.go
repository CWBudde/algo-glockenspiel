package fitrun

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

// newRunConfig builds the pre-search config.json. The resolved block is filled
// in by the second write, once the backend has reported what it chose.
func newRunConfig(spec Spec, identity Identity, prepared *preparation, started time.Time) *runConfig {
	config := &runConfig{
		Dir:              spec.Dir,
		ReferenceOptions: newLoadOptionsRecord(spec.Reference),
		Template:         templateRecord{Name: prepared.template.Name, Note: prepared.template.Note},
		Modes:            spec.Modes,
		Note:             spec.Note,
		Velocity:         spec.Velocity,
		SampleRate:       spec.SampleRate,
		Metric:           spec.Metric,
		Engine:           spec.Engine,
		MaxIterations:    spec.MaxIterations,
		MaxEvaluations:   spec.MaxEvaluations,
		TimeBudget:       spec.TimeBudget.String(),
		Seed:             spec.Seed,
		Workers:          spec.Workers,
		ReportEvery:      spec.ReportEvery,
		CheckpointEvery:  spec.CheckpointEvery,
		Polish:           newPolishRecord(spec.Polish),
		GeneratedBy:      spec.GeneratedBy,
		Name:             spec.Name,
		Identity:         identity,
		Reference:        prepared.reference,
		Started:          started,
		StrictBounds:     spec.StrictBounds,
		Gain:             gainName(spec.Gain),
	}

	if spec.Bounds != nil {
		record := newBoundsRecord(*spec.Bounds)
		config.Bounds = &record
	}

	if spec.Alignment != nil {
		name := alignmentName(*spec.Alignment)
		config.Alignment = &name
	}

	return config
}

// finish ships a vector: it polishes, decodes, measures, and writes the four
// files that describe the result.
func finish(
	ctx context.Context,
	spec Spec,
	prepared *preparation,
	result *optimizer.Result,
	chosen *Resolved,
	config *runConfig,
	tuning *optimizer.MayflyTuning,
	out io.Writer,
) (*Outcome, error) {
	bestEncoded, bestCost, polished := polishStage(ctx, spec, prepared, result, chosen, out)

	bestParams, err := prepared.objective.Codec().DecodeParams(bestEncoded)
	if err != nil {
		return nil, err
	}

	metrics, err := prepared.objective.EvaluateMetrics(bestEncoded)
	if err != nil {
		return nil, err
	}

	pinned, err := prepared.objective.Codec().Pinned(bestEncoded)
	if err != nil {
		return nil, err
	}

	summary := Summary{
		Score:             bestCost,
		Profile:           prepared.profile.Name,
		Terms:             metrics,
		Evaluations:       result.Evaluations,
		Iterations:        result.Iterations,
		Restarts:          result.Restarts,
		StopReason:        result.StopReason,
		Converged:         result.Converged,
		ElapsedSeconds:    result.Elapsed.Seconds(),
		Seed:              chosen.Seed,
		Workers:           chosen.Workers,
		Lambda:            chosen.Lambda,
		Population:        chosen.Population,
		Pinned:            len(pinned),
		Dimension:         prepared.objective.Codec().Dimension(),
		Matched:           metrics.Matched,
		ReferencePartials: metrics.ReferencePartials,
		SeededModes:       prepared.seededModes,
		Polish:            polished,
		Identity:          config.Identity,
	}

	fitted := *prepared.template
	fitted.Parameters = *bestParams

	if spec.Name != "" {
		fitted.Name = spec.Name
	}

	fitted.Provenance, err = provenanceFor(spec, config.Identity, prepared.reference, summary, *chosen)
	if err != nil {
		return nil, err
	}

	// The search result rather than the polished vector: the polish stage
	// neither resumes from a checkpoint nor writes one, so the checkpoint stays
	// the record of the search a resume would continue.
	//
	// A checkpoint that cannot be written is reported and survived. It is the
	// file a resume would read, not one the campaign scores from, and throwing
	// away an hour of search because a resume will not be possible is the
	// larger loss. A trace write is the opposite and stays fatal: the campaign
	// scores from the trace, and a run whose trace is short is a wrong number
	// rather than a missing convenience.
	err = saveCheckpoint(spec, prepared, chosen, tuning,
		result.Iterations, result.Iterations, result.BestParams, result.BestCost)
	if err != nil {
		_, _ = fmt.Fprintf(out, "checkpoint: %v; the run is finished without one\n", err)
	}

	if err := preset.Save(&fitted, filepath.Join(spec.Dir, FilePreset)); err != nil {
		return nil, err
	}

	err = renderPreset(filepath.Join(spec.Dir, FileRender), &fitted,
		spec.SampleRate, spec.Note, spec.Velocity, len(prepared.samples))
	if err != nil {
		return nil, err
	}

	if err := writeJSONFile(filepath.Join(spec.Dir, FileResult), summary); err != nil {
		return nil, err
	}

	finished := time.Now().UTC()
	config.Resolved = *chosen
	config.Finished = &finished

	if err := writeJSONFile(filepath.Join(spec.Dir, FileConfig), config); err != nil {
		return nil, err
	}

	_, _ = fmt.Fprintf(out, "Finished: best=%0.6g stop=%s iterations=%d evals=%d restarts=%d\n",
		bestCost, result.StopReason, result.Iterations, result.Evaluations, result.Restarts)

	return &Outcome{
		Result:  result,
		Summary: summary,
		Preset:  &fitted,
		Metrics: metrics,
		Encoded: bestEncoded,
	}, nil
}

// polishStage runs the optional local refinement and returns the vector the run
// should ship, its primary cost, and what the stage did.
//
// Acceptance is judged under the primary metric even though the stage searches
// under the polish profile, because the trace, the checkpoint and the summary
// all score under the primary metric: a polish that lowered the waveform term
// while raising the primary cost would ship a regression those records show.
// A failing stage is reported and ignored, since the search has already run by
// the time it starts and its result is worth more than an optional refinement.
func polishStage(
	ctx context.Context,
	spec Spec,
	prepared *preparation,
	result *optimizer.Result,
	chosen *Resolved,
	out io.Writer,
) ([]float64, float64, *optimizer.PolishResult) {
	if spec.Polish == nil || spec.Polish.Engine == "" || spec.Polish.Engine == optimizer.PolishEngineNone {
		return result.BestParams, result.BestCost, nil
	}

	options := *spec.Polish
	// The stream and the width the search resolved, so the polish of a run
	// started with a zero seed is as repeatable as the search was.
	options.Seed = chosen.Seed
	options.MaxWorkers = chosen.Workers

	polished, err := optimizer.Polish(ctx, prepared.objective, result.BestParams, options)
	if err != nil {
		_, _ = fmt.Fprintf(out, "polish (%s) failed: %v; keeping the search result\n", options.Engine, err)

		return result.BestParams, result.BestCost, nil
	}

	verdict := "rejected"
	if polished.Accepted {
		verdict = "accepted"
	}

	_, _ = fmt.Fprintf(out, "polish (%s): primary %0.6g -> %0.6g, polish %0.6g -> %0.6g, %s\n",
		options.Engine, polished.PrimaryBefore, polished.PrimaryAfter,
		polished.PolishBefore, polished.PolishAfter, verdict)

	if !polished.Accepted {
		return result.BestParams, result.BestCost, polished
	}

	return polished.Params, polished.PrimaryAfter, polished
}
