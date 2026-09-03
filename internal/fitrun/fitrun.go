package fitrun

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/cwbudde/algo-glockenspiel/assets"
	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

// preparation is everything the search needs, built once before it starts.
type preparation struct {
	reference   referenceRecord
	samples     []float32
	template    *preset.Preset
	seededModes int
	objective   *optimizer.ObjectiveFunction
	initial     []float64
	profile     optimizer.Profile
}

// Run performs one fit and writes the run directory for it.
//
// The sequence is the fit command's, without the terminal: load the reference,
// measure it, seed the starting modes from the measurement, draw the frequency
// box from it, search, optionally polish, and ship whichever vector is better
// under the primary metric. What differs is the record. Every file of the run
// directory is written whatever happened, including for a run a cancelled
// context cut short, because a campaign job that produced no files is
// indistinguishable from one that never ran.
//
// log receives the progress lines the command would have printed; a nil log
// still gets them in the run's own log.txt.
func Run(ctx context.Context, spec Spec, log io.Writer) (*Outcome, error) {
	spec = spec.withDefaults()

	if err := spec.validate(); err != nil {
		return nil, err
	}

	if ctx == nil {
		ctx = context.Background()
	}

	if err := os.MkdirAll(spec.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("create run directory %q: %w", spec.Dir, err)
	}

	logFile, err := os.Create(filepath.Join(spec.Dir, FileLog))
	if err != nil {
		return nil, fmt.Errorf("create log: %w", err)
	}

	defer func() { _ = logFile.Close() }()

	out := io.Writer(logFile)
	if log != nil {
		out = io.MultiWriter(log, logFile)
	}

	prepared, err := prepare(spec, out)
	if err != nil {
		return nil, err
	}

	return search(ctx, spec, prepared, out)
}

// prepare loads and measures the reference and builds the objective around it.
func prepare(spec Spec, out io.Writer) (*preparation, error) {
	loaded, err := analysis.LoadReference(spec.ReferencePath, spec.Reference)
	if err != nil {
		return nil, err
	}

	if loaded.SampleRate != spec.SampleRate {
		return nil, fmt.Errorf("reference sample rate %d does not match requested sample rate %d",
			loaded.SampleRate, spec.SampleRate)
	}

	sum, err := FileSHA256(spec.ReferencePath)
	if err != nil {
		return nil, err
	}

	_, _ = fmt.Fprintf(out, "reference %s: channel %s of %d, cut %d..%d (%.3f s, %s)\n",
		spec.ReferencePath, loaded.Downmix, loaded.Channels, loaded.Onset, loaded.End, loaded.Seconds, loaded.EndRule)

	// The analysis document is written from the same cut the objective scores,
	// so the partials in it are the ones the fit was actually shaped by rather
	// than a second measurement of the same file under other options.
	document, err := analysis.AnalyzeReference(spec.ReferencePath, loaded, analysis.PartialOptions{})
	if err != nil {
		return nil, err
	}

	if err := document.WriteFile(filepath.Join(spec.Dir, FileAnalysis)); err != nil {
		return nil, err
	}

	// Measured through the optimizer's own entry point rather than reused from
	// the document above: the seed, the frequency box and the partial term all
	// have to read one measurement, and that is the one they read in the fit
	// command too.
	measurement := optimizer.MeasureReference(loaded.Samples, spec.SampleRate)

	template, err := templateFor(spec)
	if err != nil {
		return nil, err
	}

	seeded, seededModes, err := optimizer.SeedPreset(template, measurement, spec.Note, spec.Modes)
	if err != nil {
		return nil, err
	}

	_, _ = fmt.Fprintf(out, "modes: %d seeded from the reference's partials\n", seededModes)

	config := optimizer.DefaultObjectiveConfig(spec.Metric)
	config.Bounds = optimizer.DefaultParamBounds
	config.Bounds.Frequency = optimizer.FrequencyBoundsFor(measurement, spec.SampleRate, seeded.Note, spec.Note)
	config.StrictBounds = false
	config.Analysis = measurement

	objective, err := optimizer.NewObjectiveFunctionWithConfig(
		loaded.Samples, seeded, spec.SampleRate, spec.Note, spec.Velocity, config,
	)
	if err != nil {
		return nil, err
	}

	encoded, err := objective.Codec().EncodeParams(&seeded.Parameters)
	if err != nil {
		return nil, err
	}

	// The seeded preset can sit fractionally outside the encoded box, so the
	// backend is handed a feasible point rather than left to reject one.
	initial, err := objective.Codec().EncodedBounds().Clamp(encoded)
	if err != nil {
		return nil, err
	}

	profile := objective.Profile()
	if !spec.Metric.Composite() {
		profile = optimizer.ProfileBalanced
	}

	return &preparation{
		reference:   referenceRecord{Path: spec.ReferencePath, SHA256: sum, Reference: *loaded},
		samples:     loaded.Samples,
		template:    seeded,
		seededModes: seededModes,
		objective:   objective,
		initial:     initial,
		profile:     profile,
	}, nil
}

// templateFor returns the starting preset, copied so the caller's own value is
// never rewritten by the seeding step.
func templateFor(spec Spec) (*preset.Preset, error) {
	if spec.Template != nil {
		return spec.Template.Clone(), nil
	}

	template, err := assets.DefaultPreset()
	if err != nil {
		return nil, fmt.Errorf("load the default preset: %w", err)
	}

	return template, nil
}

// search runs the backend, the polish stage and every write that follows.
func search(ctx context.Context, spec Spec, prepared *preparation, out io.Writer) (*Outcome, error) {
	chosen := resolved{Seed: spec.Seed, Workers: spec.Workers}
	if chosen.Workers == 0 {
		chosen.Workers = runtime.NumCPU()
	}

	backend, tuning, err := buildOptimizer(spec, prepared, &chosen, out)
	if err != nil {
		return nil, err
	}

	identity := ReadIdentity()
	started := time.Now().UTC()

	// Written before the search so a job killed mid-run still has a directory
	// that says what it was, and written again at the end with the values the
	// backend resolved for itself.
	config := newRunConfig(spec, identity, prepared, started)
	if err := writeJSONFile(filepath.Join(spec.Dir, FileConfig), config); err != nil {
		return nil, err
	}

	trace, err := newTraceWriter(filepath.Join(spec.Dir, FileTrace))
	if err != nil {
		return nil, err
	}

	reporter := &progressReporter{
		spec:     spec,
		prepared: prepared,
		trace:    trace,
		out:      out,
		chosen:   &chosen,
		tuning:   tuning,
	}

	result, err := backend.Optimize(ctx, prepared.objective.Objective(), prepared.initial,
		prepared.objective.Codec().EncodedBounds(), optimizer.OptimizeOptions{
			MaxIterations:  spec.MaxIterations,
			MaxEvaluations: spec.MaxEvaluations,
			TimeBudget:     spec.TimeBudget,
			ReportEvery:    spec.ReportEvery,
			Report:         reporter.report,
		})

	if closeErr := trace.Close(); closeErr != nil && err == nil {
		err = closeErr
	}

	if err != nil {
		return nil, err
	}

	if reporter.err != nil {
		return nil, reporter.err
	}

	return finish(ctx, spec, prepared, result, &chosen, config, tuning, out)
}

// progressReporter is the Report callback's state: the trace line, the
// checkpoint cadence and the progress log all hang off one report.
type progressReporter struct {
	spec     Spec
	prepared *preparation
	trace    *traceWriter
	out      io.Writer
	chosen   *resolved
	tuning   *optimizer.MayflyTuning

	lastCheckpoint int
	err            error
}

func (r *progressReporter) report(progress optimizer.Progress) {
	_, _ = fmt.Fprintf(r.out, "iteration %d: current=%0.6g best=%0.6g evals=%d elapsed=%s\n",
		progress.Iteration, progress.CurrentCost, progress.BestCost, progress.Evaluations,
		progress.Elapsed.Round(time.Millisecond))

	breakdown := func() (optimizer.Metrics, float64, bool) {
		if len(progress.BestParams) == 0 {
			return optimizer.Metrics{}, 0, false
		}

		metrics, err := r.prepared.objective.EvaluateMetrics(progress.BestParams)
		if err != nil {
			return optimizer.Metrics{}, 0, false
		}

		return metrics, metrics.Score(r.prepared.profile), true
	}

	if err := r.trace.append(progress, breakdown); err != nil && r.err == nil {
		r.err = err
	}

	if !shouldCheckpoint(progress.OptimizerIterations, r.lastCheckpoint, r.spec.CheckpointEvery) {
		return
	}

	// A failed checkpoint is reported and survived, for the same reason the
	// final one is: the trace is what the campaign scores from, and a search
	// that ran is worth more than the resume it will not offer. The cadence is
	// left where it was so the next report tries again.
	err := saveCheckpoint(r.spec, r.prepared, r.chosen, r.tuning,
		progress.Iteration, progress.OptimizerIterations, progress.BestParams, progress.BestCost)
	if err != nil {
		_, _ = fmt.Fprintf(r.out, "checkpoint: %v; the search continues without one\n", err)

		return
	}

	r.lastCheckpoint = progress.OptimizerIterations
}

// shouldCheckpoint counts the cadence in the backend's own iterations, the
// unit the budget and the resume both use, rather than in progress reports,
// which mean different amounts of work per backend.
func shouldCheckpoint(optimizerIterations, lastCheckpointed, checkpointEvery int) bool {
	if checkpointEvery <= 0 || optimizerIterations <= 0 {
		return false
	}

	return optimizerIterations-lastCheckpointed >= checkpointEvery
}
