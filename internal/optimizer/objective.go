package optimizer

import (
	"fmt"
	"math"
	"sync"

	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
	"github.com/cwbudde/algo-glockenspiel/model"
)

const (
	defaultLogErrorFloor = 1e-20

	// pcm16Scale is used in *both* directions so the round trip is an identity
	// to within half a quantisation step. The previous code scaled by 32767 on
	// the way in and by 32768 on the way out, a silent 0.99997x gain.
	pcm16Scale = 32767.0
)

// Metric selects the objective error metric.
type Metric string

const (
	MetricRMS Metric = "rms"

	// MetricLog is log10(floor + rms). It is a strictly monotone transform of
	// MetricRMS, so it cannot move the location of the optimum - the two
	// metrics have exactly the same minimiser. What it changes is the shape of
	// the cost near that minimum, and therefore what an absolute convergence
	// tolerance means: differences that vanish below tolerance in linear RMS
	// stay resolvable in the log domain. Choose it for tolerance semantics,
	// never in the expectation of a different fit.
	MetricLog Metric = "log"

	MetricSpectral Metric = "spectral"

	// MetricBalanced, MetricPlacement and MetricPolish are the composite
	// objective under the profile of that name: every term in Metrics,
	// scored by Metrics.Score. Balanced is the default; placement weights
	// the partials for a global stage; polish weights the waveform for a
	// local one.
	MetricBalanced  Metric = "balanced"
	MetricPlacement Metric = "placement"
	MetricPolish    Metric = "polish"
)

// ObjectiveConfig carries the optional knobs of an objective function.
type ObjectiveConfig struct {
	Metric    Metric
	Bounds    ParamBounds
	Alignment AlignmentMode
	Gain      GainMode

	// MaxLagSamples bounds the time shift alignment may apply. Zero selects a
	// sample-rate derived default.
	MaxLagSamples int

	// StrictBounds keeps Bounds as a hard constraint. By default the codec
	// widens the box until it contains the template parameters, which is
	// convenient for the built-in defaults but silently discards a range the
	// caller asked for. Set this whenever the bounds came from the user.
	StrictBounds bool

	// Analysis is the reference's measured partials for the composite
	// objective's partial term, as `glockenspiel analyze` writes them. Nil
	// measures the reference at construction.
	Analysis *analysis.Measurement
}

// DefaultObjectiveConfig returns the configuration used by the plain
// constructors.
//
// Alignment is on by default because it can only help: for signals that already
// line up, zero lag wins the correlation, while for a recorded reference with
// any leading silence an unaligned comparison is not merely noisier but
// actively wrong - seven samples of offset invert the phase of a 1756 Hz
// partial, so the correct parameters score worse than incorrect ones.
//
// Gain normalisation is off by default because it removes the absolute
// amplitude from the fit, which makes the model's amplitude parameters
// unidentifiable. Turn it on when the reference level is unknown.
func DefaultObjectiveConfig(metric Metric) ObjectiveConfig {
	return ObjectiveConfig{
		Metric:    metric,
		Bounds:    DefaultParamBounds,
		Alignment: AlignOnsetCorrelation,
		Gain:      GainNone,
	}
}

// renderState is the mutable half of an evaluation: a preset the caller may
// overwrite and a synthesizer bound to it. Each concurrent evaluation needs its
// own, because Synthesizer reads the preset it was constructed with.
type renderState struct {
	working *preset.Preset
	engine  *synth.Synthesizer
}

// ObjectiveFunction evaluates synthesized audio against a reference signal.
//
// Evaluate is safe for concurrent use: render state is drawn from a pool, so
// optimizer backends may evaluate candidates in parallel. Every other field is
// immutable after construction, including the alignment plan.
type ObjectiveFunction struct {
	reference  []float32
	template   preset.Preset
	codec      *ParamCodec
	align      *AlignmentPlan
	states     sync.Pool
	sampleRate int
	note       int
	velocity   int
	duration   float64
	metric     Metric
	gain       GainMode
	logFloor   float64

	// profile is the composite profile for a composite metric.
	profile Profile

	// composite is the reference side of the composite terms. It is built at
	// construction for a composite metric and on first use for a legacy one,
	// so EvaluateMetrics works under every metric without every legacy
	// objective paying for it.
	composite     *compositeReference
	compositeOnce sync.Once
	analysis      *analysis.Measurement
}

// newRenderState builds an independent preset/synthesizer pair from the template.
func (o *ObjectiveFunction) newRenderState() (*renderState, error) {
	working := o.template
	working.Parameters = o.template.Parameters

	engine, err := synth.NewSynthesizer(&working, o.sampleRate)
	if err != nil {
		return nil, err
	}

	return &renderState{working: &working, engine: engine}, nil
}

// NewObjectiveFunction creates an objective using a preset as synthesis template.
func NewObjectiveFunction(reference []float32, template *preset.Preset, sampleRate, note, velocity int, metric Metric) (*ObjectiveFunction, error) {
	return NewObjectiveFunctionWithConfig(reference, template, sampleRate, note, velocity, DefaultObjectiveConfig(metric))
}

// NewObjectiveFunctionWithBounds creates an objective using explicit model-space bounds.
func NewObjectiveFunctionWithBounds(reference []float32, template *preset.Preset, sampleRate, note, velocity int, metric Metric, bounds ParamBounds) (*ObjectiveFunction, error) {
	config := DefaultObjectiveConfig(metric)
	config.Bounds = bounds

	return NewObjectiveFunctionWithConfig(reference, template, sampleRate, note, velocity, config)
}

// NewObjectiveFunctionWithConfig creates an objective with full control over
// metric, bounds, time alignment and level normalisation.
func NewObjectiveFunctionWithConfig(reference []float32, template *preset.Preset, sampleRate, note, velocity int, config ObjectiveConfig) (*ObjectiveFunction, error) {
	if err := validateObjectiveInputs(reference, template, sampleRate, note, velocity, config.Metric); err != nil {
		return nil, err
	}

	newCodec := NewParamCodecWithBounds
	if config.StrictBounds {
		newCodec = NewParamCodecWithStrictBounds
	}

	// The decay ceiling depends on the note the preset is authored at, which
	// the bounds cannot know and the codec does not: a search that could
	// write 2000 ms at note 69 would produce a preset the file refuses.
	bounds, err := narrowDecayBounds(config.Bounds, template.Note)
	if err != nil {
		return nil, err
	}

	codec, err := newCodec(&template.Parameters, bounds)
	if err != nil {
		return nil, err
	}

	// The reference is kept at full precision. Quantising it to 16 bits here
	// used to throw away eight bits of a 24-bit recording for no benefit
	// whatsoever, and quantising every candidate made the objective piecewise
	// constant, so a 1e-8 convergence test compared values below the
	// quantisation step and declared victory on a plateau.
	plan := NewAlignmentPlan(reference, sampleRate, config.Alignment, config.Gain, config.MaxLagSamples)

	if config.Metric == MetricSpectral {
		if err := ValidateSpectralInput(len(reference)-plan.MaxLag(), sampleRate); err != nil {
			return nil, err
		}
	}

	profile, _ := ProfileFor(config.Metric)

	obj := &ObjectiveFunction{
		reference:  append([]float32(nil), reference...),
		template:   *template,
		codec:      codec,
		align:      plan,
		sampleRate: sampleRate,
		note:       note,
		velocity:   velocity,
		// Render a little past the reference so that a candidate which the
		// alignment shifts forward still covers the whole reference.
		duration: float64(len(reference)+plan.MaxLag()) / float64(sampleRate),
		metric:   config.Metric,
		gain:     config.Gain,
		logFloor: defaultLogErrorFloor,
		profile:  profile,
		analysis: config.Analysis,
	}

	if config.Metric.Composite() {
		obj.compositeReference()
	}

	// Fail here rather than inside a pooled allocation, where there is nowhere
	// to report the error.
	probe, err := obj.newRenderState()
	if err != nil {
		return nil, err
	}

	obj.states.New = func() any {
		state, err := obj.newRenderState()
		if err != nil {
			return nil
		}

		return state
	}
	obj.states.Put(probe)

	return obj, nil
}

func validateObjectiveInputs(reference []float32, template *preset.Preset, sampleRate, note, velocity int, metric Metric) error {
	if template == nil {
		return fmt.Errorf("template preset cannot be nil")
	}

	if sampleRate <= 0 {
		return fmt.Errorf("sample rate must be positive: %d", sampleRate)
	}

	if note < 0 || note > 127 {
		return fmt.Errorf("note must be in [0,127], got %d", note)
	}

	if velocity < 0 || velocity > 127 {
		return fmt.Errorf("velocity must be in [0,127], got %d", velocity)
	}

	if len(reference) == 0 {
		return fmt.Errorf("reference audio cannot be empty")
	}

	if _, err := ParseMetric(string(metric)); err != nil {
		return err
	}

	return preset.Validate(template)
}

// Codec returns the parameter codec used by the objective.
func (o *ObjectiveFunction) Codec() *ParamCodec {
	return o.codec
}

// Alignment returns the immutable alignment plan derived from the reference.
func (o *ObjectiveFunction) Alignment() *AlignmentPlan {
	return o.align
}

// ComputeLogError returns log10 of RMS error with a small floor and optional offset.
func ComputeLogError(synth, ref []float32, floor, offset float64) float64 {
	if floor <= 0 {
		floor = defaultLogErrorFloor
	}

	return math.Log10(floor+ComputeRMSError(synth, ref)) - offset
}

// Evaluate decodes parameters, renders audio, and returns the selected cost.
//
// It is safe to call concurrently from several goroutines.
func (o *ObjectiveFunction) Evaluate(encoded []float64) float64 {
	params, err := o.codec.DecodeParams(encoded)
	if err != nil {
		return math.Inf(1)
	}

	state, ok := o.states.Get().(*renderState)
	if !ok || state == nil {
		return math.Inf(1)
	}
	defer o.states.Put(state)

	state.working.Parameters = *params
	rendered := state.engine.RenderNote(o.note, o.velocity, o.duration)

	if o.metric.Composite() {
		return o.metrics(rendered, params).Score(o.profile)
	}

	aligned, target := o.align.Align(rendered, o.reference)

	switch o.metric {
	case MetricRMS:
		return alignedRMSError(aligned, target, o.gain)
	case MetricLog:
		return math.Log10(o.logFloor + alignedRMSError(aligned, target, o.gain))
	case MetricSpectral:
		return spectralErrorWithGain(aligned, target, o.sampleRate, o.spectralGainDB(aligned, target))
	default:
		return math.Inf(1)
	}
}

// compositeReference builds the reference side of the composite terms once.
func (o *ObjectiveFunction) compositeReference() *compositeReference {
	o.compositeOnce.Do(func() {
		o.composite = newCompositeReference(o.reference, o.sampleRate, o.analysis)
	})

	return o.composite
}

// metrics takes every composite term for a render of params.
func (o *ObjectiveFunction) metrics(rendered []float32, params *model.BarParams) Metrics {
	composite := o.compositeReference()

	return composite.measure(rendered, o.reference, o.align,
		modelPartials(params, o.template.Note, o.note, o.sampleRate, composite.partialFloor))
}

// Profile returns the composite profile the objective scores with, which is
// the zero Profile for a legacy metric.
func (o *ObjectiveFunction) Profile() Profile {
	return o.profile
}

// Metric returns the metric the objective was built with.
func (o *ObjectiveFunction) Metric() Metric {
	return o.metric
}

// EvaluateMetrics decodes and renders a candidate exactly as Evaluate does and
// returns every composite term for it. Under a composite metric,
// Metrics.Score(Profile()) is what Evaluate returns for the same candidate.
// It works under a legacy metric too, where it is a report rather than the
// cost.
func (o *ObjectiveFunction) EvaluateMetrics(encoded []float64) (Metrics, error) {
	params, err := o.codec.DecodeParams(encoded)
	if err != nil {
		return Metrics{}, err
	}

	state, ok := o.states.Get().(*renderState)
	if !ok || state == nil {
		return Metrics{}, fmt.Errorf("render state unavailable")
	}
	defer o.states.Put(state)

	state.working.Parameters = *params
	rendered := state.engine.RenderNote(o.note, o.velocity, o.duration)

	return o.metrics(rendered, params), nil
}

// spectralGainDB converts the least-squares optimal time-domain gain into the
// constant dB offset the spectral metric applies to the candidate.
func (o *ObjectiveFunction) spectralGainDB(cand, ref []float32) float64 {
	if o.gain != GainLeastSquares {
		return 0
	}

	gain := OptimalGain(cand, ref)
	if gain <= 0 {
		return 0
	}

	return 20 * math.Log10(gain)
}

// ProjectToPCM16Domain quantises samples to the 16-bit grid in place.
//
// This is a reporting aid, not part of the objective: it lets a caller show the
// error a listener would actually get from a 16-bit render. It is deliberately
// *not* applied inside Evaluate, because quantising the candidate turns the
// cost into a step function whose plateaus defeat any small convergence
// tolerance, and quantising the reference discards precision the reference may
// legitimately have.
func ProjectToPCM16Domain(samples []float32) {
	for i, sample := range samples {
		samples[i] = pcm16ToFloat32(float32ToPCM16(sample))
	}
}

func float32ToPCM16(sample float32) int16 {
	v := math.Max(-1, math.Min(1, float64(sample)))
	return int16(math.Round(v * pcm16Scale))
}

func pcm16ToFloat32(sample int16) float32 {
	return float32(float64(sample) / pcm16Scale)
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
