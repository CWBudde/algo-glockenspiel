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
	// cadence loses the improvements between reports.
	ReportEvery int

	// CheckpointEvery is the checkpoint cadence in backend iterations. Zero
	// writes the final checkpoint only.
	CheckpointEvery int

	// Polish is the optional local refinement. Nil runs none.
	Polish *optimizer.PolishOptions

	// GeneratedBy is the marker written into the preset's provenance block.
	// Zero takes "glockenspiel fitrun".
	GeneratedBy string

	// Name is the fitted preset's name. Zero keeps the template's.
	Name string
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
	Variant    string                  `json:"variant,omitempty"`
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
}

// resolved holds the values a backend chose for itself, reported through
// OnResolve before the search starts. They are recorded rather than derived,
// because a drawn seed and a machine-sized worker count are the difference
// between a run that can be repeated and one that cannot.
type resolved struct {
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
