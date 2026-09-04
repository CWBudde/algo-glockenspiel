package fitrun

import (
	"fmt"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

// The engine names Spec.Engine accepts.
const (
	EngineSimple = "simple"
	EngineMayfly = "mayfly"
	EngineCMAES  = "cmaes"
)

// The defaults a zero Spec field asks for. They are the fit command's own, so
// a campaign job and a hand-run fit of the same reference search the same
// problem.
const (
	defaultNote        = 69
	defaultVelocity    = 100
	defaultSampleRate  = 44100
	defaultReportEvery = 1
	defaultGeneratedBy = "glockenspiel fitrun"

	// defaultMayflyPopulation mirrors MayflyOptimizer's own default. The
	// wrapper resolves the population privately, so the summary has to say
	// what it would have chosen rather than read it back.
	defaultMayflyPopulation = 10
)

// The file names of the run directory. They are a contract: the campaign's
// collect step reads them by name out of directories it did not write.
const (
	FileConfig     = "config.json"
	FileAnalysis   = "analysis.json"
	FileTrace      = "trace.jsonl"
	FileCheckpoint = "checkpoint.json"
	FilePreset     = "preset.json"
	FileRender     = "render.wav"
	FileResult     = "result.json"
	FileLog        = "log.txt"

	// FileReference is the reference signal the objective actually scored:
	// loaded, cut, downmixed and peak-normalised, the same samples
	// analysis.json was measured from. It is what makes an honest A/B against
	// render.wav possible without re-deriving the loader's own policy from
	// the reference_options block.
	FileReference = "reference.wav"
)

// Spec is one fit: what to fit against, with what, and under which budget.
//
// Every field's zero value is a documented default rather than an error,
// except Dir, ReferencePath and Engine.Name, because a run that silently chose
// a backend for itself would be a run whose recorded design says one thing and
// whose numbers say another.
type Spec struct {
	// Dir is the run directory. It is created if it does not exist.
	Dir string

	// ReferencePath is the recording to fit against.
	ReferencePath string

	// Reference is the loader's policy. The zero value is the fit command's
	// default: channel zero, the automatic cut, peak-normalised.
	Reference analysis.LoadOptions

	// Template is the starting preset. Nil takes the embedded default.
	Template *preset.Preset

	// Modes is how many modes to seed from the reference's partials. Zero
	// takes every measured partial; optimizer.KeepTemplateModes keeps the
	// template's own modes and seeds nothing.
	Modes int

	// Analysis is the measurement the seed, the frequency box and the partial
	// term all read, in place of measuring the reference here. Nil measures it,
	// which is what a campaign job and a served fit both want.
	//
	// It exists for the fit command's --analysis flag: an operator who has
	// already run `glockenspiel analyze`, and edited the partial list by hand
	// or measured it under other options, is fitting against that document
	// rather than against whatever a fresh measurement of the same file would
	// say. The run's own analysis.json still records the reference as this
	// package measures it, because that file describes the recording rather
	// than the objective.
	Analysis *analysis.Measurement

	// Resume starts the search from a checkpoint's best vector instead of from
	// the seeded template. Nil starts from the template, which is every run
	// that is not a continuation.
	//
	// Only the vector is taken. Everything else a checkpoint records -- the
	// backend, the metric, the mode count, the tuning document -- decides how
	// the objective and the backend are built, so it has to be folded into the
	// rest of the spec by the caller before the run starts rather than
	// discovered here, half way through building them.
	Resume *optimizer.Checkpoint

	// Note, Velocity and SampleRate are the render the objective scores. Zero
	// takes 69, 100 and 44100.
	Note, Velocity, SampleRate int

	// Metric is the objective. Zero takes optimizer.MetricBalanced.
	Metric optimizer.Metric

	// Engine selects and configures the backend.
	Engine Engine

	// MaxIterations, MaxEvaluations and TimeBudget are the budget, passed to
	// the backend unchanged. A campaign matches MaxEvaluations across arms,
	// because an evaluation is one render whichever backend spends it.
	MaxIterations, MaxEvaluations int
	TimeBudget                    time.Duration

	// Seed selects the random stream. Zero asks the backend to draw one and
	// report it, which the summary and the config then record.
	Seed int64

	// Workers bounds parallel evaluation. Zero follows the machine.
	Workers int

	// ReportEvery is the progress cadence in backend iterations. Zero takes 1,
	// because the trace is the record a campaign scores from and a coarse
	// cadence loses the improvements between reports. ReportNever asks for no
	// reports at all.
	ReportEvery int

	// CheckpointEvery is the checkpoint cadence in backend iterations. Zero
	// writes the final checkpoint only. CheckpointNever writes none at all.
	CheckpointEvery int

	// Polish is the optional local refinement. Nil runs none.
	Polish *optimizer.PolishOptions

	// GeneratedBy is the marker written into the preset's provenance block.
	// Zero takes "glockenspiel fitrun".
	GeneratedBy string

	// Name is the fitted preset's name. Zero keeps the template's.
	Name string

	// OnProgress is called from the same report every trace line comes from,
	// after that line is written, so a live caller sees exactly what the
	// trace records and no faster. Nil runs none, which is every campaign
	// job's Spec today.
	OnProgress func(optimizer.Progress, *optimizer.Metrics)

	// OnResolve is called once, before the search starts, with the values the
	// backend chose for itself: the drawn seed, the machine-sized worker
	// count, and (mayfly or CMA-ES) the variant, population, covariance mode
	// or lambda. It fires before the first OnProgress call. Nil runs none.
	OnResolve func(Resolved)

	// Bounds replaces the whole search box in place of the default one
	// widened around the seeded template's frequency. Nil keeps today's
	// behaviour exactly: optimizer.DefaultParamBounds with Frequency drawn
	// from the reference's measured fundamental, and the box widened rather
	// than enforced (StrictBounds false).
	//
	// A non-nil value is a caller's own box, mirroring
	// internal/server/fit.go's readBoundsPart: with StrictBounds it is a hard
	// constraint the fitted preset must not leave, exactly as the fit
	// command's --bounds flag is.
	Bounds *optimizer.ParamBounds

	// StrictBounds keeps Bounds as written instead of widening it to contain
	// the seeded template. Read only when Bounds is non-nil.
	StrictBounds bool

	// Alignment selects how a candidate is time-aligned to the reference
	// before the error is computed. Nil keeps today's behaviour: the
	// objective's own default, optimizer.AlignOnsetCorrelation.
	//
	// A pointer rather than a bare optimizer.AlignmentMode, because
	// optimizer.AlignNone is the type's zero value: a value field could not
	// tell "the caller wants no alignment" apart from "the caller left this
	// blank", and the server needs exactly that distinction (Align:false
	// asks for AlignNone, mirroring internal/server/fit.go:759,772, while a
	// campaign job that never mentions Alignment still gets onset
	// correlation as it does today).
	Alignment *optimizer.AlignmentMode

	// Gain selects how a candidate's level is matched to the reference before
	// the error is computed. The zero value is optimizer.GainNone, which is
	// both the objective's own default and what every campaign job asks for,
	// so this needs no pointer the way Alignment does: "unset" and "no gain
	// normalisation" are the same request.
	//
	// optimizer.GainLeastSquares divides out the scalar gain that minimises
	// the residual, which is what internal/server/fit.go's normalizeGain field
	// asks for.
	Gain optimizer.GainMode
}

// StopReasonCanceled is the stop reason every backend records for a run that
// was stopped by its context rather than by its own criteria. It is spelled
// here, beside result.json's own Summary, because it is a value of that file:
// anything reading a run directory back -- the campaign's status command, the
// server's restart recovery -- has to tell a cancelled run from a finished one
// and must not do it by its own copy of the string.
const StopReasonCanceled = "context_canceled"

// ReportNever is the cadence that reports nothing: no progress line, no trace
// line, no OnProgress call. It exists because zero already means "take the
// default", and a caller that genuinely wants a silent run -- which is what
// internal/server's reportEvery=0 asks for, and what the fit command's own
// flag has always meant -- has no other way to say so.
const ReportNever = -1

// CheckpointNever asks for no checkpoint at all, not even the final one. It
// exists for the same reason ReportNever does: zero already means "the
// default", which here is the single final checkpoint, and the fit command's
// --checkpoint-interval 0 has always meant "write nothing", including nothing
// at the end. A run that asks for it cannot be resumed, which is the whole of
// what the flag buys -- a fit that leaves no state behind.
const CheckpointNever = -1

// checkpoints reports whether this run writes checkpoints at all.
func (s Spec) checkpoints() bool {
	return s.CheckpointEvery >= 0
}

// reportEvery is the cadence the backend is given: a negative one is the
// optimizer's own "never", which is how it spells the same thing.
func (s Spec) reportEvery() int {
	if s.ReportEvery < 0 {
		return 0
	}

	return s.ReportEvery
}

// Engine selects the search backend and carries its settings. Only the block
// matching Name is read.
type Engine struct {
	Name   string         `json:"name"`
	Mayfly MayflySettings `json:"mayfly,omitzero"`
	CMAES  CMAESSettings  `json:"cmaes,omitzero"`
}

// MayflySettings configures the mayfly backend. Epochs and Restarts become a
// schedule overlay on Tuning, exactly as the fit command's flags do, so a
// document that already named a schedule still wins.
type MayflySettings struct {
	Variant string `json:"variant,omitempty"`

	// Preset names one of the wrapper's own parameter sets. It selects a
	// dialect of its own, so a run that names a preset leaves Variant empty
	// and learns which dialect ran from the resolution report.
	Preset string `json:"preset,omitempty"`

	Population int                     `json:"population,omitempty"`
	Epochs     int                     `json:"epochs,omitempty"`
	Restarts   int                     `json:"restarts,omitempty"`
	Tuning     *optimizer.MayflyTuning `json:"tuning,omitempty"`
}

// CMAESSettings configures the CMA-ES backend. RunEvaluations and LambdaGrowth
// are the restart ladder's shape: a per-run cap with no restart limit is cold
// restarts until the budget is spent, and a growth of two is IPOP.
type CMAESSettings struct {
	Covariance     string  `json:"covariance,omitempty"`
	Lambda         int     `json:"lambda,omitempty"`
	Sigma          float64 `json:"sigma,omitempty"`
	RestartLimit   int     `json:"restart_limit,omitempty"`
	RunEvaluations int     `json:"run_evaluations,omitempty"`
	LambdaGrowth   float64 `json:"lambda_growth,omitempty"`
}

// Summary is result.json: what the run spent and what it found, in the fields
// the campaign's collect step turns into one CSV row.
type Summary struct {
	Score             float64                 `json:"score"`
	Profile           string                  `json:"profile"`
	Terms             optimizer.Metrics       `json:"terms"`
	Evaluations       int                     `json:"evaluations"`
	Iterations        int                     `json:"iterations"`
	Restarts          int                     `json:"restarts"`
	StopReason        string                  `json:"stop_reason"`
	Converged         bool                    `json:"converged"`
	ElapsedSeconds    float64                 `json:"elapsed_seconds"`
	Seed              int64                   `json:"seed"`
	Workers           int                     `json:"workers"`
	Lambda            int                     `json:"lambda,omitempty"`
	Population        int                     `json:"population,omitempty"`
	Pinned            int                     `json:"pinned"`
	Dimension         int                     `json:"dimension"`
	Matched           int                     `json:"matched"`
	ReferencePartials int                     `json:"reference_partials"`
	SeededModes       int                     `json:"seeded_modes"`
	Polish            *optimizer.PolishResult `json:"polish,omitempty"`
	Identity          Identity                `json:"identity"`
}

// Outcome is what Run found, for a caller that wants the values rather than
// the files.
type Outcome struct {
	Result  *optimizer.Result
	Summary Summary
	Preset  *preset.Preset
	Metrics optimizer.Metrics
	Encoded []float64

	// Profile is the profile Metrics were scored under. The summary records
	// its name, which is enough to read a finished run back, but a caller
	// printing the breakdown has to weigh the terms by the same numbers the
	// score used, and looking the profile up again by name is how the two
	// quietly stop agreeing.
	Profile optimizer.Profile

	// Pinned are the dimensions of the shipped vector that sit on an edge of
	// the search box, which is where the search wanted to go further and could
	// not. The summary keeps only the count, because that is what a campaign
	// puts in a column; the names are what an operator needs to widen the box.
	Pinned []optimizer.PinnedDimension
}

// Resolved holds the values a backend chose for itself, reported through
// OnResolve before the search starts. They are recorded rather than derived,
// because a drawn seed and a machine-sized worker count are the difference
// between a run that can be repeated and one that cannot.
//
// Exported, with the tags config.json already wrote, so a caller that wants
// to know what the backend chose (a live server watching a fit run) reads the
// same struct the run directory records rather than a second copy of it.
type Resolved struct {
	Seed       int64  `json:"seed"`
	Workers    int    `json:"workers"`
	Lambda     int    `json:"lambda,omitempty"`
	Population int    `json:"population,omitempty"`
	Variant    string `json:"variant,omitempty"`
	Covariance string `json:"covariance,omitempty"`
}

// withDefaults returns the spec with every documented zero replaced, so the
// rest of the package reads one set of values and the config file records the
// same ones the run used.
func (s Spec) withDefaults() Spec {
	if s.Note == 0 {
		s.Note = defaultNote
	}

	if s.Velocity == 0 {
		s.Velocity = defaultVelocity
	}

	if s.SampleRate == 0 {
		s.SampleRate = defaultSampleRate
	}

	if s.Metric == "" {
		s.Metric = optimizer.MetricBalanced
	}

	if s.ReportEvery == 0 {
		s.ReportEvery = defaultReportEvery
	}

	if s.GeneratedBy == "" {
		s.GeneratedBy = defaultGeneratedBy
	}

	return s
}

// validate rejects the three inputs that have no sensible default.
func (s Spec) validate() error {
	if s.Dir == "" {
		return fmt.Errorf("run directory cannot be empty")
	}

	if s.ReferencePath == "" {
		return fmt.Errorf("reference path cannot be empty")
	}

	switch s.Engine.Name {
	case EngineSimple, EngineMayfly, EngineCMAES:
		return nil
	case "":
		return fmt.Errorf("engine name cannot be empty, want %q, %q or %q", EngineSimple, EngineMayfly, EngineCMAES)
	default:
		return fmt.Errorf("unknown engine %q, want %q, %q or %q", s.Engine.Name, EngineSimple, EngineMayfly, EngineCMAES)
	}
}
