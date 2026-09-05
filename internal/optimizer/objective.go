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
	// refs holds one entry per note the candidate is scored at, ascending. It
	// is never empty; a single-note objective is the one-entry case and every
	// accessor that used to read a scalar field reads refs[0] instead.
	refs []*noteReference

	template   preset.Preset
	codec      *ParamCodec
	states     sync.Pool
	sampleRate int
	velocity   int
	metric     Metric
	gain       GainMode
	logFloor   float64

	// profile is the composite profile for a composite metric.
	profile Profile

	// config is the configuration the objective was built with, kept so that
	// WithMetric can rebuild the same objective under another metric.
	config ObjectiveConfig
}

// noteReference is one recording and everything derived from it: the note it
// sounds, the render length that covers it, its alignment plan and the
// reference side of the composite terms.
//
// All four are properties of that recording alone, which is what makes a
// multi-note objective a slice of these rather than a different kind of object.
// A one-entry slice behaves exactly as the scalar fields it replaced.
type noteReference struct {
	samples  []float32
	note     int
	duration float64
	align    *AlignmentPlan
	analysis *analysis.Measurement

	// composite is built at construction for a composite metric and on first
	// use for a legacy one, so EvaluateMetrics works under every metric
	// without every legacy objective paying for it.
	composite *compositeReference
	once      sync.Once
}

// ReferenceInput is one recording handed to a multi-note objective: the samples,
// the note they sound, and optionally a measurement to use in place of taking
// one.
type ReferenceInput struct {
	Samples  []float32
	Note     int
	Analysis *analysis.Measurement
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
	return newObjectiveFunction(
		[]ReferenceInput{{Samples: reference, Note: note}}, template, sampleRate, velocity, config, nil)
}

// checkNotesAreDistinct refuses two references at the same note. They would be
// rendered identically and averaged, which silently doubles that note's weight.
func checkNotesAreDistinct(refs []ReferenceInput) error {
	seen := make(map[int]int, len(refs))

	for index, ref := range refs {
		if first, ok := seen[ref.Note]; ok {
			return fmt.Errorf(
				"references %d and %d are both at note %d: the candidate renders once for both, so "+
					"averaging them would give that note twice the weight of every other",
				first, index, ref.Note)
		}

		seen[ref.Note] = index
	}

	return nil
}

// NewMultiNoteObjective scores one candidate against several recordings at
// once, one per note, and returns the mean of their scores.
//
// This is what fitting a whole instrument means rather than one of its bars.
// The candidate is a single preset authored at template.Note; each reference is
// rendered by transposing it to that reference's own note, so the search is
// looking for the one bar whose transposition covers the whole range rather
// than for the bar that best fits any one recording.
//
// Only a composite metric is accepted. The legacy metrics return a raw error in
// units that depend on the recording's own level and length, so averaging them
// across notes would weight the notes by their loudness; the composite terms are
// scaled by their norms and saturated first, which is what puts every note on
// one scale.
func NewMultiNoteObjective(
	refs []ReferenceInput,
	template *preset.Preset,
	sampleRate, velocity int,
	config ObjectiveConfig,
) (*ObjectiveFunction, error) {
	if len(refs) == 0 {
		return nil, fmt.Errorf("a multi-note objective needs at least one reference")
	}

	if !config.Metric.Composite() {
		return nil, fmt.Errorf(
			"metric %q is a legacy single-value metric, whose scale depends on the recording's own level "+
				"and length; averaging it across notes would weight them by loudness, so a multi-note "+
				"objective needs a composite profile", config.Metric)
	}

	return newObjectiveFunction(refs, template, sampleRate, velocity, config, nil)
}

// newObjectiveFunction is the shared constructor. A non-nil shared composite
// reference is adopted instead of measuring the reference again, which is what
// WithMetric hands over: the composite reference depends only on the reference
// signal and is immutable once built. It is per note, so the slice is parallel
// to refs.
func newObjectiveFunction(
	refs []ReferenceInput,
	template *preset.Preset,
	sampleRate, velocity int,
	config ObjectiveConfig,
	shared []*compositeReference,
) (*ObjectiveFunction, error) {
	if len(refs) == 0 {
		return nil, fmt.Errorf("an objective needs a reference")
	}

	for _, ref := range refs {
		if err := validateObjectiveInputs(ref.Samples, template, sampleRate, ref.Note, velocity, config.Metric); err != nil {
			return nil, err
		}
	}

	if err := checkNotesAreDistinct(refs); err != nil {
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

	profile, _ := ProfileFor(config.Metric)

	built := make([]*noteReference, 0, len(refs))

	for index, ref := range refs {
		// The reference is kept at full precision. Quantising it to 16 bits
		// here used to throw away eight bits of a 24-bit recording for no
		// benefit whatsoever, and quantising every candidate made the objective
		// piecewise constant, so a 1e-8 convergence test compared values below
		// the quantisation step and declared victory on a plateau.
		plan := NewAlignmentPlan(ref.Samples, sampleRate, config.Alignment, config.Gain, config.MaxLagSamples)

		if config.Metric == MetricSpectral {
			if err := ValidateSpectralInput(len(ref.Samples)-plan.MaxLag(), sampleRate); err != nil {
				return nil, err
			}
		}

		measurement := ref.Analysis
		if measurement == nil && len(refs) == 1 {
			// A single-note objective takes its measurement from the config,
			// which is where the fit command's --analysis flag lands. There is
			// no config field that could mean "the third note's measurement",
			// so a multi-note objective carries them on the references.
			measurement = config.Analysis
		}

		entry := &noteReference{
			samples:  append([]float32(nil), ref.Samples...),
			note:     ref.Note,
			align:    plan,
			analysis: measurement,
			// Render a little past the reference so that a candidate which the
			// alignment shifts forward still covers the whole reference.
			duration: float64(len(ref.Samples)+plan.MaxLag()) / float64(sampleRate),
		}

		if index < len(shared) && shared[index] != nil {
			entry.once.Do(func() { entry.composite = shared[index] })
		}

		built = append(built, entry)
	}

	obj := &ObjectiveFunction{
		refs:       built,
		template:   *template,
		codec:      codec,
		sampleRate: sampleRate,
		velocity:   velocity,
		metric:     config.Metric,
		gain:       config.Gain,
		logFloor:   defaultLogErrorFloor,
		profile:    profile,
		config:     config,
	}

	if config.Metric.Composite() {
		for _, ref := range obj.refs {
			ref.compositeReference(sampleRate)
		}
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
// For a multi-note objective it is the lowest note's, which is the one a
// single-note caller would have got.
func (o *ObjectiveFunction) Alignment() *AlignmentPlan {
	return o.refs[0].align
}

// Notes lists the notes the objective scores at, in the order it was given
// them.
func (o *ObjectiveFunction) Notes() []int {
	notes := make([]int, 0, len(o.refs))
	for _, ref := range o.refs {
		notes = append(notes, ref.note)
	}

	return notes
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

	if o.metric.Composite() {
		// The mean of the per-note scores, not the mean of the per-note terms
		// scored once. Each Score saturates its own terms first, so one
		// hopeless note cannot swamp a term's average across the others, and
		// each renormalises over the terms *it* could measure, so a note whose
		// recording is too short for a spectral frame is scored on the terms it
		// has rather than handing its missing term's weight to its neighbours.
		total := 0.0

		for _, ref := range o.refs {
			rendered := state.engine.RenderNote(ref.note, o.velocity, ref.duration)

			score := ref.metrics(o, rendered, params).Score(o.profile)
			if math.IsInf(score, 1) {
				return math.Inf(1)
			}

			total += score
		}

		return total / float64(len(o.refs))
	}

	ref := o.refs[0]
	rendered := state.engine.RenderNote(ref.note, o.velocity, ref.duration)
	aligned, target := ref.align.Align(rendered, ref.samples)

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

// compositeReference builds this note's reference side of the composite terms
// once.
func (r *noteReference) compositeReference(sampleRate int) *compositeReference {
	r.once.Do(func() {
		r.composite = newCompositeReference(r.samples, sampleRate, r.analysis)
	})

	return r.composite
}

// metrics takes every composite term for a render of params at this note.
func (r *noteReference) metrics(o *ObjectiveFunction, rendered []float32, params *model.BarParams) Metrics {
	composite := r.compositeReference(o.sampleRate)

	return composite.measure(rendered, r.samples, r.align,
		modelPartials(params, o.template.Note, r.note, o.sampleRate, composite.partialFloor))
}

// Profile returns the composite profile the objective scores with, which is
// the zero Profile for a legacy metric.
func (o *ObjectiveFunction) Profile() Profile {
	return o.profile
}

// Config returns the configuration the objective was built with.
func (o *ObjectiveFunction) Config() ObjectiveConfig {
	return o.config
}

// WithMetric returns an objective over the same reference, template, sample
// rate and configuration, scored under another metric. The reference is not
// measured again: the composite terms' reference side is derived from the
// reference signal alone, so the new objective shares the one this objective
// already holds. Because the bounds and the template are unchanged, the new
// objective's codec has the same dimension and the same encoded bounds, and an
// encoded vector means the same thing to both.
//
// Asking for the metric the objective already has returns the objective
// itself, since there is nothing to rebuild.
func (o *ObjectiveFunction) WithMetric(metric Metric) (*ObjectiveFunction, error) {
	if metric == o.metric {
		return o, nil
	}

	config := o.config
	config.Metric = metric

	var shared []*compositeReference

	if o.metric.Composite() || metric.Composite() {
		shared = make([]*compositeReference, 0, len(o.refs))
		for _, ref := range o.refs {
			shared = append(shared, ref.compositeReference(o.sampleRate))
		}
	}

	template := o.template
	inputs := make([]ReferenceInput, 0, len(o.refs))

	for _, ref := range o.refs {
		inputs = append(inputs, ReferenceInput{Samples: ref.samples, Note: ref.note, Analysis: ref.analysis})
	}

	return newObjectiveFunction(inputs, &template, o.sampleRate, o.velocity, config, shared)
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

	ref := o.refs[0]
	rendered := state.engine.RenderNote(ref.note, o.velocity, ref.duration)

	return ref.metrics(o, rendered, params), nil
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
