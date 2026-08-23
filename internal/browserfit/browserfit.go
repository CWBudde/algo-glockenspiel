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
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
)

const (
	// MaxReferenceBytes matches the HTTP fit service's upload limit.
	MaxReferenceBytes = 16 << 20

	maxIterations = 100_000
	maxTimeBudget = time.Hour
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

	MayflyVariant    string `json:"mayflyVariant"`
	MayflyPopulation int    `json:"mayflyPopulation"`
	MayflySeed       string `json:"mayflySeed"`
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
	request    Request
	template   *preset.Preset
	objective  *optimizer.ObjectiveFunction
	initial    []float64
	backend    optimizer.Optimizer
	timeBudget time.Duration
	sampleRate int
	reference  int
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

	reference, sampleRate, err := wavio.DecodeMono(bytes.NewReader(referenceWAV), "the uploaded reference")
	if err != nil {
		return nil, err
	}

	if len(reference) == 0 {
		return nil, errors.New("the reference contains no samples")
	}

	if sampleRate < minSampleRate || sampleRate > maxSampleRate {
		return nil, fmt.Errorf("the reference declares a sample rate of %d Hz, outside the supported [%d,%d] range",
			sampleRate, minSampleRate, maxSampleRate)
	}

	template, err := decodePreset(presetJSON)
	if err != nil {
		return nil, err
	}

	config := optimizer.DefaultObjectiveConfig(metric)
	config.Alignment = optimizer.AlignNone

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
		request:    request,
		template:   template,
		objective:  objective,
		initial:    initial,
		backend:    backend,
		timeBudget: budget,
		sampleRate: sampleRate,
		reference:  len(reference),
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

	return budget, seed, nil
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
		backend := &optimizer.MayflyOptimizer{
			Variant:    request.MayflyVariant,
			Population: request.MayflyPopulation,
			Seed:       seed,
			MaxWorkers: 1,
		}

		if err := backend.Validate(); err != nil {
			return nil, err
		}

		return backend, nil
	default:
		return nil, fmt.Errorf("unsupported optimizer %q", request.Optimizer)
	}
}
