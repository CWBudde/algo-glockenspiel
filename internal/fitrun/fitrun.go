package fitrun

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/cwbudde/algo-glockenspiel/assets"
	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
	"github.com/cwbudde/algo-glockenspiel/model"
)

// analysisMinFrameSize is the smallest window the reference's partials are
// measured with, the same floor optimizer.measurePartials holds itself to: a
// window below it resolves nothing a mode could be read off.
const analysisMinFrameSize = 256

// preparation is everything the search needs, built once before it starts.
//
// notes holds one entry per reference and is never empty. reference and samples
// are its first entry, kept as fields because every single-note path -- the
// config record, the provenance block, the render length -- reads them and a
// multi-reference run has no single answer for them.
type preparation struct {
	reference   referenceRecord
	samples     []float32
	notes       []notePreparation
	template    *preset.Preset
	seededModes int
	objective   *optimizer.ObjectiveFunction
	initial     []float64
	profile     optimizer.Profile
}

// notePreparation is one loaded, measured recording.
type notePreparation struct {
	record      referenceRecord
	samples     []float32
	note        int
	measurement *analysis.Measurement
	dir         string
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

// prepare loads and measures every reference and builds the objective around
// them.
func prepare(spec Spec, out io.Writer) (*preparation, error) {
	notes, err := loadReferences(spec, out)
	if err != nil {
		return nil, err
	}

	// The seed, the frequency box and the partial term all read one
	// measurement. For a multi-reference fit that is the authored note's, which
	// is the only one whose partials are already in the preset's own frame: a
	// mode seeded from another note would have to be transposed here, and the
	// seed does that transposition once rather than twice.
	seedNote := notes[0]

	for _, note := range notes {
		if note.note == spec.Note {
			seedNote = note

			break
		}
	}

	template, err := templateFor(spec)
	if err != nil {
		return nil, err
	}

	// Authoring the preset where the caller asked rather than wherever the
	// template happened to sit. The transposition is the model's own, so the
	// template still describes the same sound; what changes is the frame its
	// numbers are written in, and therefore whether a fitted mode frequency can
	// be read as a partial of the recording.
	if spec.AuthoredNote != 0 && spec.AuthoredNote != template.Note {
		model.TransposeToNote(&template.Parameters, template.Note, spec.AuthoredNote)
		template.Note = spec.AuthoredNote

		if err := preset.Validate(template); err != nil {
			return nil, fmt.Errorf("the template cannot be authored at note %d: %w", spec.AuthoredNote, err)
		}
	}

	seeded, seededModes, err := optimizer.SeedPreset(template, seedNote.measurement, seedNote.note, spec.Modes)
	if err != nil {
		return nil, err
	}

	writeSeededModes(out, seeded, seededModes, spec.Modes)

	config := optimizer.DefaultObjectiveConfig(spec.Metric)
	config.Bounds = optimizer.DefaultParamBounds
	config.Bounds.Frequency = frequencyBounds(notes, spec, seeded.Note)
	config.StrictBounds = false
	config.Analysis = seedNote.measurement

	if spec.Bounds != nil {
		// A caller's own box replaces the whole thing, frequency included: it
		// is asking for a specific search, not a widening of the default one.
		config.Bounds = *spec.Bounds
		config.StrictBounds = spec.StrictBounds
	}

	if spec.Alignment != nil {
		config.Alignment = *spec.Alignment
	}

	config.Gain = spec.Gain

	inputs := make([]optimizer.ReferenceInput, 0, len(notes))
	for _, note := range notes {
		inputs = append(inputs, optimizer.ReferenceInput{
			Samples: note.samples, Note: note.note, Analysis: note.measurement,
		})
	}

	var objective *optimizer.ObjectiveFunction

	if len(spec.References) > 0 {
		objective, err = optimizer.NewMultiNoteObjective(inputs, seeded, spec.SampleRate, spec.Velocity, config)
	} else {
		objective, err = optimizer.NewObjectiveFunctionWithConfig(
			notes[0].samples, seeded, spec.SampleRate, spec.Note, spec.Velocity, config)
	}

	if err != nil {
		return nil, err
	}

	encoded, err := objective.Codec().EncodeParams(&seeded.Parameters)
	if err != nil {
		return nil, err
	}

	// A resumed run continues from the vector the checkpoint holds rather than
	// from the seeded template. The length is checked rather than trusted: a
	// checkpoint written against a differently shaped preset (another harmonic
	// count, Chebyshev toggled) encodes a different number of coordinates, and
	// resuming was asked for explicitly, so it is refused loudly instead of
	// quietly starting over.
	if spec.Resume != nil {
		if len(spec.Resume.BestParams) != len(encoded) {
			return nil, fmt.Errorf(
				"the checkpoint holds %d parameters but the preset encodes %d: use the preset the checkpoint was written with, or do not resume",
				len(spec.Resume.BestParams), len(encoded))
		}

		encoded = append(encoded[:0], spec.Resume.BestParams...)
	}

	// The seeded preset can sit fractionally outside the encoded box, so the
	// backend is handed a feasible point rather than left to reject one. With a
	// caller's own strict box the starting point can sit well outside it, which
	// is the case this clamp was written for.
	initial, err := objective.Codec().EncodedBounds().Clamp(encoded)
	if err != nil {
		return nil, err
	}

	// A clamp worth mentioning is one against a box the caller wrote: the
	// default box is widened to hold the template, so anything it moves is
	// rounding, while a strict box that had to be pulled in means the run is
	// not starting from the preset that was handed to it. Only the strict case
	// is reported, and only once, before the search.
	if spec.Bounds != nil && spec.StrictBounds && !slices.Equal(initial, encoded) {
		_, _ = fmt.Fprintln(out,
			"warning: the starting preset lies outside the requested bounds and was clamped into them")
	}

	profile := objective.Profile()
	if !spec.Metric.Composite() {
		profile = optimizer.ProfileBalanced
	}

	return &preparation{
		reference:   notes[0].record,
		samples:     notes[0].samples,
		notes:       notes,
		template:    seeded,
		seededModes: seededModes,
		objective:   objective,
		initial:     initial,
		profile:     profile,
	}, nil
}

// analysisFrameSize is the window analysis.json is measured with: the default
// one, halved until it fits the signal.
//
// It is the rule optimizer.measurePartials already follows, and it exists for
// the same reason: a reference shorter than the default window is not an error
// but a short reference. A fit against a fifth of a second -- which is what a
// single strike auditioned through the server can be -- would otherwise write
// no analysis at all and fail before the search ever started. Zero, for a
// signal shorter than any usable window, is PartialOptions' own "take the
// default", which then reports the reference as too short exactly as before.
func analysisFrameSize(samples int) int {
	frameSize := analysis.DefaultFrameSize
	for frameSize > samples {
		frameSize /= 2
	}

	if frameSize < analysisMinFrameSize {
		return 0
	}

	return frameSize
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
	chosen := Resolved{Seed: spec.Seed, Workers: spec.Workers}
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
			ReportEvery:    spec.reportEvery(),
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
	chosen   *Resolved
	tuning   *optimizer.MayflyTuning

	lastCheckpoint int
	err            error
}

func (r *progressReporter) report(progress optimizer.Progress) {
	_, _ = fmt.Fprintf(r.out, "iteration %d: current=%0.6g best=%0.6g evals=%d elapsed=%s\n",
		progress.Iteration, progress.CurrentCost, progress.BestCost, progress.Evaluations,
		progress.Elapsed.Round(time.Millisecond))

	// Measured at most once per report and only when the trace line actually
	// carries a breakdown (an improved line), so OnProgress costs nothing
	// beyond what the trace already spends: it shares the one EvaluateMetrics
	// call rather than asking for a second.
	var (
		measured    bool
		metrics     optimizer.Metrics
		metricsOkay bool
	)

	breakdown := func() (optimizer.Metrics, float64, bool) {
		measured = true

		if len(progress.BestParams) == 0 {
			return optimizer.Metrics{}, 0, false
		}

		m, err := r.prepared.objective.EvaluateMetrics(progress.BestParams)
		if err != nil {
			return optimizer.Metrics{}, 0, false
		}

		metrics = m
		metricsOkay = true

		return m, m.Score(r.prepared.profile), true
	}

	if err := r.trace.append(progress, breakdown); err != nil && r.err == nil {
		r.err = err
	}

	if r.spec.OnProgress != nil {
		if measured && metricsOkay {
			r.spec.OnProgress(progress, &metrics)
		} else {
			r.spec.OnProgress(progress, nil)
		}
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

// loadReferences loads, measures and records every recording the spec names,
// writing each one's analysis.json and reference.wav where a reader will look
// for it.
//
// A single-reference run writes them at the top of the run directory, exactly
// where they have always been; a multi-reference run writes them under
// notes/<nnn>/ and writes nothing at the top at all. The asymmetry is
// deliberate: a top-level reference.wav beside a twenty-note fit would be one
// note's recording under a name that promises the fit's, and every consumer
// reading it by name -- the server's comparison, a campaign collect -- would
// draw a picture that disagreed with the score for a reason nothing said.
func loadReferences(spec Spec, out io.Writer) ([]notePreparation, error) {
	type source struct {
		path string
		note int
		dir  string
	}

	sources := make([]source, 0, max(1, len(spec.References)))

	if len(spec.References) == 0 {
		sources = append(sources, source{path: spec.ReferencePath, note: spec.Note})
	} else {
		for _, ref := range spec.References {
			sources = append(sources, source{
				path: ref.Path,
				note: ref.Note,
				dir:  filepath.Join(NotesDir, fmt.Sprintf("%03d", ref.Note)),
			})
		}
	}

	notes := make([]notePreparation, 0, len(sources))

	for _, src := range sources {
		loaded, err := analysis.LoadReference(src.path, spec.Reference)
		if err != nil {
			return nil, err
		}

		if loaded.SampleRate != spec.SampleRate {
			return nil, fmt.Errorf("reference %s has sample rate %d, which does not match the requested %d",
				src.path, loaded.SampleRate, spec.SampleRate)
		}

		sum, err := FileSHA256(src.path)
		if err != nil {
			return nil, err
		}

		_, _ = fmt.Fprintf(out, "reference %s at note %d: channel %s of %d, cut %d..%d (%.3f s, %s)\n",
			src.path, src.note, loaded.Downmix, loaded.Channels, loaded.Onset, loaded.End,
			loaded.Seconds, loaded.EndRule)

		dir := filepath.Join(spec.Dir, src.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create %q: %w", dir, err)
		}

		// The analysis document is written from the same cut the objective
		// scores, so the partials in it are the ones the fit was actually
		// shaped by rather than a second measurement under other options.
		document, err := analysis.AnalyzeReference(src.path, loaded,
			analysis.PartialOptions{FrameSize: analysisFrameSize(len(loaded.Samples))})
		if err != nil {
			return nil, err
		}

		if err := document.WriteFile(filepath.Join(dir, FileAnalysis)); err != nil {
			return nil, err
		}

		// Written next to analysis.json rather than derived later from
		// reference_options, because this is the signal the objective actually
		// scored: loaded, cut, downmixed and peak-normalised.
		if err := wavio.WriteMono(filepath.Join(dir, FileReference), spec.SampleRate, loaded.Samples); err != nil {
			return nil, err
		}

		// Measured through the optimizer's own entry point rather than reused
		// from the document above, because the seed, the frequency box and the
		// partial term all have to read one measurement. A caller that brought
		// its own document is fitting against that document, so it replaces the
		// measurement everywhere at once -- which is why it applies only to a
		// single reference, there being no way to name one of twenty.
		measurement := spec.Analysis
		if measurement == nil || len(spec.References) > 0 {
			measurement = optimizer.MeasureReference(loaded.Samples, spec.SampleRate)
		}

		notes = append(notes, notePreparation{
			record:      referenceRecord{Path: src.path, SHA256: sum, Reference: *loaded},
			samples:     loaded.Samples,
			note:        src.note,
			measurement: measurement,
			dir:         src.dir,
		})
	}

	return notes, nil
}

// frequencyBounds draws the mode-frequency box the search runs in.
//
// For one reference it is optimizer.FrequencyBoundsFor unchanged. For several
// it is their intersection, and the two ends bind at opposite notes: the
// ceiling at the highest, because a mode legal at the authored note can still
// land past Nyquist once transposed up, and the floor at whichever note is most
// permissive, because the floor is an octave of slack against the analysis
// having placed the fundamental on a higher partial and one note's mistake must
// not cut the box for the rest.
func frequencyBounds(notes []notePreparation, spec Spec, authoredNote int) optimizer.Range {
	box := optimizer.FrequencyBoundsFor(notes[0].measurement, spec.SampleRate, authoredNote, notes[0].note)

	for _, note := range notes[1:] {
		other := optimizer.FrequencyBoundsFor(note.measurement, spec.SampleRate, authoredNote, note.note)

		box.Max = math.Min(box.Max, other.Max)
		box.Min = math.Min(box.Min, other.Min)
	}

	return box
}
