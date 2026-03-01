package optimizer

import (
	"fmt"
	"math"

	"github.com/cwbudde/glockenspiel/internal/preset"
	"github.com/cwbudde/glockenspiel/internal/synth"
)

const defaultLogErrorFloor = 1e-20

// Metric selects the objective error metric.
type Metric string

const (
	MetricRMS Metric = "rms"
	MetricLog Metric = "log"
)

// ObjectiveFunction evaluates synthesized audio against a reference signal.
type ObjectiveFunction struct {
	reference  []float32
	template   preset.Preset
	codec      *ParamCodec
	sampleRate int
	note       int
	velocity   int
	duration   float64
	metric     Metric
	logFloor   float64
	costOffset float64
}

// NewObjectiveFunction creates an objective using a preset as synthesis template.
func NewObjectiveFunction(reference []float32, template *preset.Preset, sampleRate, note, velocity int, metric Metric) (*ObjectiveFunction, error) {
	return NewObjectiveFunctionWithBounds(reference, template, sampleRate, note, velocity, metric, DefaultParamBounds)
}

// NewObjectiveFunctionWithBounds creates an objective using explicit model-space bounds.
func NewObjectiveFunctionWithBounds(reference []float32, template *preset.Preset, sampleRate, note, velocity int, metric Metric, bounds ParamBounds) (*ObjectiveFunction, error) {
	if template == nil {
		return nil, fmt.Errorf("template preset cannot be nil")
	}

	if sampleRate <= 0 {
		return nil, fmt.Errorf("sample rate must be positive: %d", sampleRate)
	}

	if note < 0 || note > 127 {
		return nil, fmt.Errorf("note must be in [0,127], got %d", note)
	}

	if velocity < 0 || velocity > 127 {
		return nil, fmt.Errorf("velocity must be in [0,127], got %d", velocity)
	}

	if len(reference) == 0 {
		return nil, fmt.Errorf("reference audio cannot be empty")
	}

	if metric != MetricRMS && metric != MetricLog {
		return nil, fmt.Errorf("unsupported metric %q", metric)
	}

	if err := preset.Validate(template); err != nil {
		return nil, err
	}

	codec, err := NewParamCodecWithBounds(&template.Parameters, bounds)
	if err != nil {
		return nil, err
	}

	return &ObjectiveFunction{
		reference:  append([]float32(nil), reference...),
		template:   *template,
		codec:      codec,
		sampleRate: sampleRate,
		note:       note,
		velocity:   velocity,
		duration:   float64(len(reference)) / float64(sampleRate),
		metric:     metric,
		logFloor:   defaultLogErrorFloor,
		costOffset: 0,
	}, nil
}

// Codec returns the parameter codec used by the objective.
func (o *ObjectiveFunction) Codec() *ParamCodec {
	return o.codec
}

// ComputeRMSError returns the RMS difference between signals after truncation to the shorter length.
func ComputeRMSError(synth, ref []float32) float64 {
	n := minInt(len(synth), len(ref))
	if n == 0 {
		return math.Inf(1)
	}

	sum := 0.0

	for i := 0; i < n; i++ {
		d := float64(synth[i] - ref[i])
		sum += d * d
	}

	return math.Sqrt(sum / float64(n))
}

// ComputeLogError returns log10 of RMS error with a small floor and optional offset.
func ComputeLogError(synth, ref []float32, floor, offset float64) float64 {
	if floor <= 0 {
		floor = defaultLogErrorFloor
	}

	return math.Log10(floor+ComputeRMSError(synth, ref)) - offset
}

// Evaluate decodes parameters, renders audio, and returns the selected cost.
func (o *ObjectiveFunction) Evaluate(encoded []float64) float64 {
	params, err := o.codec.DecodeParams(encoded)
	if err != nil {
		return math.Inf(1)
	}

	workingPreset := o.template
	workingPreset.Parameters = *params

	engine, err := synth.NewSynthesizer(&workingPreset, o.sampleRate)
	if err != nil {
		return math.Inf(1)
	}

	rendered := engine.RenderNote(o.note, o.velocity, o.duration)
	switch o.metric {
	case MetricRMS:
		return ComputeRMSError(rendered, o.reference)
	case MetricLog:
		return ComputeLogError(rendered, o.reference, o.logFloor, o.costOffset)
	default:
		return math.Inf(1)
	}
}

// Objective returns the objective as an optimizer-compatible callback.
func (o *ObjectiveFunction) Objective() ObjectiveFunc {
	return o.Evaluate
}

func minInt(a, b int) int {
	if a < b {
		return a
	}

	return b
}
