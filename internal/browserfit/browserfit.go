// Package browserfit assembles an optimizer run from the in-memory inputs a
// browser can provide. It is the filesystem- and HTTP-free counterpart of the
// fit command and fit service.
package browserfit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	embeddedassets "github.com/cwbudde/algo-glockenspiel/assets"
	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
	"github.com/cwbudde/algo-glockenspiel/model"
)

const (
	// MaxReferenceBytes matches the HTTP fit service's upload limit.
	MaxReferenceBytes = 16 << 20

	maxIterations = 100_000
	maxTimeBudget = time.Hour

	// maxMayflyRounds bounds the schedule the same way the fit service does:
	// every round costs at least one iteration, so an unbounded count is a
	// request to split the budget into slices too thin to search.
	maxMayflyRounds = 1000

	minSampleRate = 4000
	maxSampleRate = 192000
	maxRenderTime = 60 * time.Second

	// yieldInterval bounds how long objective evaluation may run before the
	// fit hands control back to its caller.
	yieldInterval = 25 * time.Millisecond
)

// Request is the browser worker's JSON description of one fit.
type Request struct {
	Note          int    `json:"note"`
	Velocity      int    `json:"velocity"`
	Optimizer     string `json:"optimizer"`
	Metric        string `json:"metric"`
	MaxIterations int    `json:"maxIterations"`
	TimeBudget    string `json:"timeBudget"`
	ReportEvery   int    `json:"reportEvery"`

	Align         bool `json:"align"`
	NormalizeGain bool `json:"normalizeGain"`

	// Modes is how the starting modes are chosen, as --modes is on the
	// command line: zero seeds one per partial the reference's analysis
	// lists, a positive count the strongest that many, -1 keeps the starting
	// preset's own.
	Modes int `json:"modes,omitempty"`

	MayflyVariant    string `json:"mayflyVariant"`
	MayflyPopulation int    `json:"mayflyPopulation"`
	MayflySeed       string `json:"mayflySeed"`

	MayflyPreset     string   `json:"mayflyPreset,omitempty"`
	MayflyEpochs     int      `json:"mayflyEpochs,omitempty"`
	MayflyRestarts   int      `json:"mayflyRestarts,omitempty"`
	MayflyStagnation int      `json:"mayflyStagnation,omitempty"`
	MayflyTargetCost *float64 `json:"mayflyTargetCost,omitempty"`
	MayflyNC         *int     `json:"mayflyNc,omitempty"`
	MayflyNCRatio    *float64 `json:"mayflyNcRatio,omitempty"`
	MayflySelection  string   `json:"mayflySelection,omitempty"`

	// MayflyTuning is the full tuning document, inline in the request rather
	// than a separate input, which is where this front end departs from the
	// HTTP one. Two reasons:
	//
	//  1. fitStart has a fixed five-argument contract -- request, reference,
	//     preset, bounds, callback -- mirrored by the worker in
	//     web/src/features/optimize/fit.worker.ts. A sixth argument would break
	//     that contract on both sides for no gain.
	//  2. DecodeRequest's DisallowUnknownFields reaches into nested structs, so
	//     a misspelled knob is rejected here exactly as
	//     optimizer.DecodeMayflyTuning rejects it in a standalone document.
	//
	// The bounds document is a separate argument only because the HTTP path
	// mirrors it as a multipart file part and this path has no multipart to
	// mirror; the asymmetry is the transport's, not a decision about tuning.
	MayflyTuning *optimizer.MayflyTuning `json:"mayflyTuning,omitempty"`
}

// DecodeRequest parses the worker request without accepting misspelled fields
// or trailing documents.
func DecodeRequest(data []byte) (Request, error) {
	var request Request

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
		return request, fmt.Errorf("decode fit request: %w", err)
	}

	if err := decoder.Decode(new(json.RawMessage)); err == nil {
		return request, errors.New("decode fit request: unexpected content after the request object")
	} else if !errors.Is(err, io.EOF) {
		return request, fmt.Errorf("decode fit request: unexpected content after the request object: %w", err)
	}

	return request, nil
}

// Prepared is a validated fit ready to run. It owns all input memory.
type Prepared struct {
	request     Request
	template    *preset.Preset
	objective   *optimizer.ObjectiveFunction
	initial     []float64
	backend     optimizer.Optimizer
	timeBudget  time.Duration
	sampleRate  int
	reference   int
	seededModes int
}

// New validates and decodes a browser fit. Empty presetJSON and boundsJSON
// select the embedded preset and default search bounds respectively.
func New(request Request, referenceWAV, presetJSON, boundsJSON []byte) (*Prepared, error) {
	budget, seed, err := validateRequest(request)
	if err != nil {
		return nil, err
	}

	backend, err := selectOptimizer(request, seed)
	if err != nil {
		return nil, err
	}

	metric, err := optimizer.ParseMetric(request.Metric)
	if err != nil {
		return nil, err
	}

	if len(referenceWAV) == 0 {
		return nil, errors.New("the reference is empty")
	}

	if len(referenceWAV) > MaxReferenceBytes {
		return nil, fmt.Errorf("the reference is %d bytes, above the %d byte limit", len(referenceWAV), MaxReferenceBytes)
	}

	// The reference is prepared the way the fit command prepares one: one
	// channel, cut to its first strike, peak-normalised.
	loaded, err := analysis.DecodeReference(bytes.NewReader(referenceWAV), "the uploaded reference", analysis.LoadOptions{})
	if err != nil {
		return nil, err
	}

	reference, sampleRate := loaded.Samples, loaded.SampleRate

	if sampleRate < minSampleRate || sampleRate > maxSampleRate {
		return nil, fmt.Errorf("the reference declares a sample rate of %d Hz, outside the supported [%d,%d] range",
			sampleRate, minSampleRate, maxSampleRate)
	}

	template, err := decodePreset(presetJSON)
	if err != nil {
		return nil, err
	}

	if request.Modes < optimizer.KeepTemplateModes || request.Modes > model.MaxModes {
		return nil, fmt.Errorf("modes must be between %d and %d, got %d", optimizer.KeepTemplateModes, model.MaxModes, request.Modes)
	}

	// The same sequence the fit command runs: measure the reference once,
	// seed the starting modes from it, and draw the frequency box from its
	// fundamental unless a box was uploaded.
	measurement := optimizer.MeasureReference(reference, sampleRate)

	template, seededModes, err := optimizer.SeedPreset(template, measurement, request.Note, request.Modes)
	if err != nil {
		return nil, err
	}

	config := optimizer.DefaultObjectiveConfig(metric)
	config.Alignment = optimizer.AlignNone
	config.Analysis = measurement
	config.Bounds.Frequency = optimizer.FrequencyBoundsFor(measurement, sampleRate, template.Note, request.Note)

	if len(boundsJSON) > 0 {
		bounds, decodeErr := optimizer.DecodeParamBounds(boundsJSON, "the uploaded bounds")
		if decodeErr != nil {
			return nil, decodeErr
		}

		config.Bounds = bounds
		config.StrictBounds = true
	}

	if request.Align {
		config.Alignment = optimizer.AlignOnsetCorrelation
	}

	if request.NormalizeGain {
		config.Gain = optimizer.GainLeastSquares
	}

	objective, err := optimizer.NewObjectiveFunctionWithConfig(
		reference, template, sampleRate, request.Note, request.Velocity, config,
	)
	if err != nil {
		return nil, err
	}

	encoded, err := objective.Codec().EncodeParams(&template.Parameters)
	if err != nil {
		return nil, err
	}

	initial, err := objective.Codec().EncodedBounds().Clamp(encoded)
	if err != nil {
		return nil, err
	}

	return &Prepared{
		request:     request,
		template:    template,
		objective:   objective,
		initial:     initial,
		backend:     backend,
		timeBudget:  budget,
		sampleRate:  sampleRate,
		seededModes: seededModes,
		reference:   len(reference),
	}, nil
}

// Request returns the validated request.
func (p *Prepared) Request() Request { return p.request }

// SampleRate returns the reference's declared sample rate.
func (p *Prepared) SampleRate() int { return p.sampleRate }

// ReferenceSeconds returns the reference duration.
func (p *Prepared) ReferenceSeconds() float64 {
	return float64(p.reference) / float64(p.sampleRate)
}

// OnMayflyResolve installs the callback that reports what a Mayfly run settled
// on -- the dialect and the seed -- once those are known and before the search
// starts. It must be called before Run, and a fit that does not use Mayfly
// never calls back.
//
// Without it a preset's dialect and a resolved zero seed are invisible to the
// browser: both are settled inside the run and would otherwise be discarded
// here, leaving the UI unable to say what it actually ran.
func (p *Prepared) OnMayflyResolve(report func(optimizer.ResolvedMayfly)) {
	if backend, ok := p.backend.(*optimizer.MayflyOptimizer); ok {
		backend.OnResolve = report
	}
}

// Run performs the fit. Progress is deliberately requested every optimizer
// iteration even when the UI displays fewer reports: the WASM bridge uses the
// callback as a cooperative yield point so Cancel and worker messages remain
// responsive during CPU-bound fitting.
//
// yield, when non-nil, is the same cooperative yield applied between objective
// evaluations. A single optimizer iteration holds the caller for as long as it
// takes to evaluate the whole population -- thousands of renders for a large
// Mayfly swarm -- so yielding only between iterations is not enough.
func (p *Prepared) Run(ctx context.Context, report func(optimizer.Progress), yield func()) (*optimizer.Result, error) {
	return p.backend.Optimize(ctx, p.yielding(yield), p.initial,
		p.objective.Codec().EncodedBounds(), optimizer.OptimizeOptions{
			MaxIterations: p.request.MaxIterations,
			TimeBudget:    p.timeBudget,
			ReportEvery:   1,
			Report:        report,
		})
}

// yielding wraps the objective so that yield runs at most every yieldInterval,
// keeping the cost of handing control back a small fraction of the evaluation
// it interrupts. The lock is what makes the wrapper safe for the parallel
// evaluation the optimizers are allowed to do; yield itself runs unlocked so a
// yielding goroutine never stalls the others.
func (p *Prepared) yielding(yield func()) optimizer.ObjectiveFunc {
	objective := p.objective.Objective()
	if yield == nil {
		return objective
	}

	var (
		mu   sync.Mutex
		last = time.Now()
	)

	return func(params []float64) float64 {
		cost := objective(params)

		mu.Lock()

		due := time.Since(last) >= yieldInterval
		if due {
			last = time.Now()
		}
		mu.Unlock()

		if due {
			yield()
		}

		return cost
	}
}

// Metrics is the breakdown of an optimizer point: every term of the
// composite objective, whatever metric the fit scores by.
func (p *Prepared) Metrics(params []float64) (optimizer.Metrics, error) {
	return p.objective.EvaluateMetrics(params)
}

// SeededModes is how many of the starting modes came from the reference's
// partials, zero when the starting preset's own were kept.
func (p *Prepared) SeededModes() int { return p.seededModes }

// Pinned lists the dimensions of an optimizer point that sit on a bound of
// the search box.
func (p *Prepared) Pinned(params []float64) ([]optimizer.PinnedDimension, error) {
	return p.objective.Codec().Pinned(params)
}

// Preset decodes an optimizer point into a fitted preset.
func (p *Prepared) Preset(params []float64) (*preset.Preset, error) {
	decoded, err := p.objective.Codec().DecodeParams(params)
	if err != nil {
		return nil, err
	}

	fitted := p.template.Clone()
	fitted.Parameters = *decoded

	return fitted, nil
}

// MarshalPreset returns a downloadable, human-readable preset document.
func MarshalPreset(fitted *preset.Preset) ([]byte, error) {
	if err := preset.Validate(fitted); err != nil {
		return nil, err
	}

	data, err := json.MarshalIndent(fitted, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode fitted preset: %w", err)
	}

	return append(data, '\n'), nil
}

// Render returns a mono WAV audition of a fitted preset.
func Render(fitted *preset.Preset, sampleRate, note, velocity int, duration time.Duration) ([]byte, error) {
	if note < 0 || note > 127 {
		return nil, fmt.Errorf("note must be in [0,127], got %d", note)
	}

	if velocity < 0 || velocity > 127 {
		return nil, fmt.Errorf("velocity must be in [0,127], got %d", velocity)
	}

	if duration <= 0 || duration > maxRenderTime {
		return nil, fmt.Errorf("duration must be above zero and at most %s", maxRenderTime)
	}

	engine, err := synth.NewSynthesizer(fitted, sampleRate)
	if err != nil {
		return nil, fmt.Errorf("build fitted synthesizer: %w", err)
	}

	return wavio.MarshalMono(sampleRate,
		engine.RenderNote(note, velocity, duration.Seconds()))
}

func decodePreset(data []byte) (*preset.Preset, error) {
	if len(data) == 0 {
		return embeddedassets.DefaultPreset()
	}

	return preset.Decode(data, "the uploaded starting preset")
}

func validateRequest(request Request) (time.Duration, int64, error) {
	if request.Note < 0 || request.Note > 127 {
		return 0, 0, fmt.Errorf("note must be in [0,127], got %d", request.Note)
	}

	if request.Velocity < 0 || request.Velocity > 127 {
		return 0, 0, fmt.Errorf("velocity must be in [0,127], got %d", request.Velocity)
	}

	if request.MaxIterations < 1 || request.MaxIterations > maxIterations {
		return 0, 0, fmt.Errorf("maxIterations must be in [1,%d], got %d", maxIterations, request.MaxIterations)
	}

	if request.ReportEvery < 0 || request.ReportEvery > maxIterations {
		return 0, 0, fmt.Errorf("reportEvery must be in [0,%d], got %d", maxIterations, request.ReportEvery)
	}

	budget, err := parseDuration(request.TimeBudget)
	if err != nil {
		return 0, 0, err
	}

	if budget <= 0 || budget > maxTimeBudget {
		return 0, 0, fmt.Errorf("timeBudget must be above zero and at most %s, got %s", maxTimeBudget, budget)
	}

	seed, err := strconv.ParseInt(strings.TrimSpace(request.MayflySeed), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("mayflySeed must be a 64-bit whole number, got %q", request.MayflySeed)
	}

	if request.Optimizer == "mayfly" && (request.MayflyPopulation < 2 || request.MayflyPopulation > 4096) {
		return 0, 0, fmt.Errorf("mayflyPopulation must be in [2,4096], got %d", request.MayflyPopulation)
	}

	if err := validateMayflyTuningScalars(request); err != nil {
		return 0, 0, err
	}

	return budget, seed, nil
}

// validateMayflyTuningScalars checks the scalar tuning settings against the
// same ranges the fit service applies, so a browser fit and a server fit reject
// the same request. The values are checked before they reach a tuning document
// because mayfly reports an unusable configuration only once the run starts,
// and by then the caller has already waited for the WASM module and the fit.
//
// A zero epoch or restart count is the omitted key, not a request for zero
// rounds: both fields are omitempty on the wire and the schedule already
// defaults to one warm round and no cold ones.
func validateMayflyTuningScalars(request Request) error {
	if request.MayflyEpochs != 0 && (request.MayflyEpochs < 1 || request.MayflyEpochs > maxMayflyRounds) {
		return fmt.Errorf("mayflyEpochs must be in [1,%d], got %d", maxMayflyRounds, request.MayflyEpochs)
	}

	if request.MayflyRestarts < 0 || request.MayflyRestarts > maxMayflyRounds {
		return fmt.Errorf("mayflyRestarts must be in [0,%d], got %d", maxMayflyRounds, request.MayflyRestarts)
	}

	if request.MayflyStagnation < 0 || request.MayflyStagnation > maxIterations {
		return fmt.Errorf("mayflyStagnation must be in [0,%d], got %d", maxIterations, request.MayflyStagnation)
	}

	// -1 is mayfly.NCAuto, which derives the offspring count from the ratio, so
	// it is the floor rather than an error.
	if request.MayflyNC != nil && *request.MayflyNC < -1 {
		return fmt.Errorf("mayflyNc must be at least -1, got %d", *request.MayflyNC)
	}

	// NaN is rejected explicitly because mayfly sanitises a non-finite knob
	// mid-run: it would not fail, it would quietly become something else.
	if request.MayflyNCRatio != nil && (*request.MayflyNCRatio < 0 || math.IsNaN(*request.MayflyNCRatio)) {
		return fmt.Errorf("mayflyNcRatio must be at least 0, got %g", *request.MayflyNCRatio)
	}

	return nil
}

// mayflyTuning turns the request's scalar settings into a tuning document and
// lets the inline document override it, key by key. Precedence is one sentence:
// the document wins.
//
// The scalars are never written onto a mayfly.Config here. Building a document
// instead leaves optimizer.MayflyOptimizer with a single applier, so the two
// ways a browser fit can name a knob cannot drift into two ways of applying it.
func mayflyTuning(request Request) *optimizer.MayflyTuning {
	scalars := &optimizer.MayflyTuning{
		NC:      request.MayflyNC,
		NCRatio: request.MayflyNCRatio,
	}

	if request.MayflySelection != "" {
		selection := request.MayflySelection
		scalars.Selection = &selection
	}

	// The blocks stay nil unless something asked for them: a convergence block
	// that exists is what turns mayfly's early exit on, and an empty one would
	// turn it on with Go zero values.
	if request.MayflyTargetCost != nil || request.MayflyStagnation > 0 {
		convergence := &optimizer.MayflyConvergence{TargetCost: request.MayflyTargetCost}

		if request.MayflyStagnation > 0 {
			stagnation := request.MayflyStagnation
			convergence.StagnationIterations = &stagnation
		}

		scalars.Convergence = convergence
	}

	if request.MayflyEpochs > 0 || request.MayflyRestarts > 0 {
		schedule := &optimizer.MayflySchedule{}

		if request.MayflyEpochs > 0 {
			epochs := request.MayflyEpochs
			schedule.Epochs = &epochs
		}

		if request.MayflyRestarts > 0 {
			restarts := request.MayflyRestarts
			schedule.Restarts = &restarts
		}

		scalars.Schedule = schedule
	}

	return scalars.Overlay(request.MayflyTuning)
}

func parseDuration(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if parsed, err := time.ParseDuration(trimmed); err == nil {
		return parsed, nil
	}

	seconds, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 0, fmt.Errorf("timeBudget must be a duration such as 30s or 10m, got %q", raw)
	}

	return time.Duration(seconds * float64(time.Second)), nil
}

func selectOptimizer(request Request, seed int64) (optimizer.Optimizer, error) {
	switch request.Optimizer {
	case "simple":
		return &optimizer.SimpleOptimizer{}, nil
	case "mayfly":
		// MaxWorkers is one because a WASM build has no threads: anything above
		// one would serialise anyway, at the cost of the goroutine handover.
		backend := &optimizer.MayflyOptimizer{
			Variant:    request.MayflyVariant,
			Preset:     request.MayflyPreset,
			Population: request.MayflyPopulation,
			Seed:       seed,
			MaxWorkers: 1,
			Tuning:     mayflyTuning(request),
		}

		if err := backend.Validate(request.MaxIterations); err != nil {
			return nil, err
		}

		return backend, nil
	default:
		return nil, fmt.Errorf("unsupported optimizer %q", request.Optimizer)
	}
}
