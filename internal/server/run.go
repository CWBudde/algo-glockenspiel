package server

// This file is the bridge between an accepted start request and
// internal/fitrun, which is the one place a fit is actually run. The server
// used to run a loop of its own here; it now describes the request as a
// fitrun.Spec and hands it over, so a fit started from the browser leaves
// exactly the run directory a campaign job leaves.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

// uploadFileName is the uploaded recording, written into the run directory
// exactly as it arrived.
//
// It is a second copy of audio the directory already holds as
// fitrun.FileReference, and it is kept on purpose: fitrun reads its reference
// from a path three times over -- to load it, to hash it, and to analyse it --
// so it needs a file rather than bytes. The two are not the same signal
// either. This one is the original upload, header and all; reference.wav is
// the cut, downmixed, peak-normalised mono the objective actually scored.
const uploadFileName = "upload.wav"

// servedBy marks presets fitted through the HTTP server, so a preset's own
// provenance says which front end produced it.
const servedBy = "glockenspiel serve"

// uploadFileMode is what the run directory's files are written with. The
// directory is the user's own cache, and the campaign tooling reads it as the
// same user.
const uploadFileMode = 0o644

// writeUpload puts the uploaded reference in the run directory, where the spec
// points fitrun at it.
func writeUpload(dir string, upload []byte) error {
	path := filepath.Join(dir, uploadFileName)

	if err := os.WriteFile(path, upload, uploadFileMode); err != nil {
		return fmt.Errorf("the uploaded reference could not be written to %s: %w", path, err)
	}

	return nil
}

// runFit is the body of a job: one fitrun.Run into the job's own directory.
//
// Everything the old loop computed by hand comes from the Outcome instead. The
// one exception is the pinned dimensions, which the Outcome reports only as a
// count: they are read back through the codec the start request already built,
// which is the same codec fitrun built from the same reference, template and
// box.
func (s *Server) runFit(
	ctx context.Context,
	job *fitJob,
	codec *optimizer.ParamCodec,
	template *preset.Preset,
	bounds *optimizer.ParamBounds,
) {
	// The run's own log.txt takes the progress lines. Sending them to the
	// server's log as well would put one line per iteration of every fit on
	// the terminal somebody is using to serve a web app.
	outcome, err := fitrun.Run(ctx, fitSpecFor(job, template, bounds), nil)
	if err != nil {
		// A run stopped by its own context did not fail, it was cancelled --
		// which is what a job cancelled between the worker picking it up and
		// the search starting looks like from here: fitrun returns the
		// context's error before it has an outcome to report. Calling that a
		// failure would put a cancelled job in the history as a broken one.
		state := fitFailed
		if ctx.Err() != nil {
			state = fitCanceled
		}

		job.finish(state, nil, nil, nil, nil, err)

		return
	}

	// A cancelled run still produced the best parameters it found, so the
	// preset is kept either way; only the state says which happened. Losing it
	// would make "cancel" mean "throw away the last ten minutes", which is the
	// opposite of what someone watching a cost curve flatten out wants.
	state := fitSucceeded
	if ctx.Err() != nil {
		state = fitCanceled
	}

	metrics := outcome.Metrics

	// A codec that cannot describe the result is not worth failing a finished
	// run over: the pinned list is a hint about the box, not the result.
	pinned, _ := codec.Pinned(outcome.Encoded)

	// pinned was read back through this package's own codec, built from the
	// same reference, template and box fitrun.prepare used -- but nothing
	// enforces that the two agree. outcome.Summary.Pinned is the count
	// fitrun's codec itself computed while finishing the run, so a mismatch
	// means the two boxes have quietly diverged: serving pinned here would
	// tell the client the wrong dimensions are held, which looks like success
	// and is not. Dropping the list is the honest failure; a caller that
	// asked for pinned and got none can retry rather than trust a bad one.
	if len(pinned) != outcome.Summary.Pinned {
		s.logf("fit %s: pinned dimension count mismatch, server codec found %d but fitrun found %d, dropping the list",
			job.id, len(pinned), outcome.Summary.Pinned)

		pinned = nil
	}

	// The summary before the finish, so a client woken by the terminal event
	// finds the job listing's score already in place.
	job.recordSummary(outcome.Summary)
	job.finish(state, outcome.Preset, outcome.Result, &metrics, pinned, nil)

	s.logf("fit %s finished: state=%s best=%0.6g stop=%s iterations=%d evals=%d dir=%s",
		job.id, state, outcome.Result.BestCost, outcome.Result.StopReason,
		outcome.Result.Iterations, outcome.Result.Evaluations, job.dir)
}

// fitSpecFor describes an accepted request as one fit.
//
// template is the uploaded starting preset rather than the seeded one the
// start request built to validate itself: fitrun seeds from the reference's
// partials itself, and handing it an already seeded preset together with the
// mode count would seed twice.
func fitSpecFor(job *fitJob, template *preset.Preset, bounds *optimizer.ParamBounds) fitrun.Spec {
	settings := job.request

	spec := fitrun.Spec{
		Dir:           job.dir,
		ReferencePath: filepath.Join(job.dir, uploadFileName),
		Reference:     settings.loadOptions(),
		Template:      template,
		Modes:         settings.Modes,
		Note:          settings.Note,
		Velocity:      settings.Velocity,
		// The rate is the uploaded file's own, as it is everywhere else in
		// this package: the file already knows, and fitrun refuses a reference
		// whose rate disagrees with the spec.
		SampleRate:    job.sampleRate,
		Metric:        optimizer.Metric(settings.Metric),
		Engine:        engineFor(settings),
		MaxIterations: settings.MaxIterations,
		TimeBudget:    settings.timeBudget(),
		Seed:          seedFor(settings),
		ReportEvery:   reportEveryFor(settings.ReportEvery),
		GeneratedBy:   servedBy,
		// Bounds the client sent are a hard constraint, exactly as --bounds is
		// on the command line, and an absent document keeps fitrun's own
		// default box.
		Bounds:       bounds,
		StrictBounds: bounds != nil,
		Alignment:    alignmentFor(settings.Align),
		OnProgress:   job.report,
		OnResolve:    job.recordResolved,
	}

	if settings.NormalizeGain {
		spec.Gain = optimizer.GainLeastSquares
	}

	return spec
}

// reportEveryFor translates the request's cadence into a spec's.
//
// The two spell "never" differently: the request follows the fit command,
// where zero is no reports at all, while a fitrun spec's zero is "take the
// default" -- one, because the trace is what a campaign scores from.
func reportEveryFor(cadence int) int {
	if cadence <= 0 {
		return fitrun.ReportNever
	}

	return cadence
}

// alignmentFor turns the request's boolean into the mode fitrun takes.
//
// It always returns a pointer, never nil: false is a request for AlignNone
// rather than "no opinion", and AlignNone is the enum's zero value, so leaving
// the field unset would silently give an unaligned fit the onset correlation
// it asked not to have.
func alignmentFor(align bool) *optimizer.AlignmentMode {
	mode := optimizer.AlignNone
	if align {
		mode = optimizer.AlignOnsetCorrelation
	}

	return &mode
}

// engineFor selects and configures the backend fitrun will build.
//
// The two engine name sets are the same strings by construction: the server's
// "simple", "mayfly" and "cmaes" are fitrun's EngineSimple, EngineMayfly and
// EngineCMAES, and validateFitBackend has already refused anything else at
// parse time.
func engineFor(settings fitRequest) fitrun.Engine {
	engine := fitrun.Engine{Name: settings.Optimizer}

	switch settings.Optimizer {
	case mayflyOptimizerName:
		engine.Mayfly = fitrun.MayflySettings{
			Variant:    settings.MayflyVariant,
			Preset:     settings.MayflyPreset,
			Population: settings.MayflyPopulation,
			// Epochs and Restarts are deliberately left at zero. The request's
			// own mayflyTuning already folds them into the document, together
			// with the stagnation rule, the selection strategy and the
			// crossover knobs, and under the server's precedence: an uploaded
			// document wins over the form fields. Writing them here as well
			// would apply fitrun's opposite precedence on top and let a form
			// field the client never touched override the document it did
			// upload.
			Tuning: settings.mayflyTuning(),
		}
	case cmaesOptimizerName:
		engine.CMAES = fitrun.CMAESSettings{
			Covariance:   settings.CmaesCovariance,
			Lambda:       settings.CmaesLambda,
			Sigma:        settings.CmaesSigma,
			RestartLimit: settings.CmaesRestarts,
		}
	}

	return engine
}

// seedFor picks the seed the chosen backend reads. The request carries one per
// backend, as the fit command's flags do, while a spec carries the single
// stream the run uses.
func seedFor(settings fitRequest) int64 {
	if settings.Optimizer == cmaesOptimizerName {
		return settings.CmaesSeed
	}

	return settings.MayflySeed
}
