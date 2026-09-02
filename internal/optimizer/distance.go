package optimizer

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

// Measurement is every term the objective knows how to score, taken from one
// render of one candidate under one alignment and gain policy.
//
// Evaluate returns exactly one of these numbers, chosen by the metric the
// objective was built with. Measure returns all of them from the same render,
// through the same functions, so a report can show what the other metrics
// would have said about the same candidate without a second search.
type Measurement struct {
	RMS      float64 `json:"rms"`
	Log      float64 `json:"log"`
	Spectral float64 `json:"spectral"`

	// Lag is the number of samples the candidate trailed the reference by
	// before alignment; positive means the candidate started late. It is zero
	// under AlignNone, where nothing is shifted.
	Lag int `json:"lag"`

	// Overlap is the number of samples the terms were computed over.
	Overlap int `json:"overlap"`

	// Gain is the least-squares scalar that best matches the candidate to the
	// reference over the overlap. It is measured under every policy; whether
	// the terms above were computed with it divided out is GainApplied.
	Gain        float64 `json:"gain"`
	GainApplied bool    `json:"gain_applied"`
}

// MarshalJSON writes a non-finite term as null. The spectral term is +Inf for
// a reference shorter than one frame, and encoding/json refuses to encode
// that rather than choosing a representation.
func (m Measurement) MarshalJSON() ([]byte, error) {
	type wire struct {
		RMS         *float64 `json:"rms"`
		Log         *float64 `json:"log"`
		Spectral    *float64 `json:"spectral"`
		Lag         int      `json:"lag"`
		Overlap     int      `json:"overlap"`
		Gain        *float64 `json:"gain"`
		GainApplied bool     `json:"gain_applied"`
	}

	return json.Marshal(wire{
		RMS:         finiteOrNil(m.RMS),
		Log:         finiteOrNil(m.Log),
		Spectral:    finiteOrNil(m.Spectral),
		Lag:         m.Lag,
		Overlap:     m.Overlap,
		Gain:        finiteOrNil(m.Gain),
		GainApplied: m.GainApplied,
	})
}

func finiteOrNil(value float64) *float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}

	return &value
}

// Render decodes a candidate and renders it exactly as Evaluate would, at the
// objective's note, velocity and duration. The returned slice is the caller's.
func (o *ObjectiveFunction) Render(encoded []float64) ([]float32, error) {
	params, err := o.codec.DecodeParams(encoded)
	if err != nil {
		return nil, err
	}

	state, ok := o.states.Get().(*renderState)
	if !ok || state == nil {
		return nil, fmt.Errorf("render state unavailable")
	}
	defer o.states.Put(state)

	state.working.Parameters = *params

	return state.engine.RenderNote(o.note, o.velocity, o.duration), nil
}

// Measure scores a candidate under every metric at once.
//
// The alignment, the gain policy and each term go through the same code as
// Evaluate, so for the metric the objective was built with the corresponding
// field equals Evaluate's return value exactly.
func (o *ObjectiveFunction) Measure(encoded []float64) (Measurement, error) {
	rendered, err := o.Render(encoded)
	if err != nil {
		return Measurement{}, err
	}

	lag := o.align.BestLag(rendered)
	aligned, target := alignSlices(rendered, o.reference, lag)
	rms := alignedRMSError(aligned, target, o.gain)

	return Measurement{
		RMS:         rms,
		Log:         math.Log10(o.logFloor + rms),
		Spectral:    spectralErrorWithGain(aligned, target, o.sampleRate, o.spectralGainDB(aligned, target)),
		Lag:         lag,
		Overlap:     minInt(len(aligned), len(target)),
		Gain:        OptimalGain(aligned, target),
		GainApplied: o.gain == GainLeastSquares,
	}, nil
}

// DistanceConfig says how a preset is rendered against a reference for a
// distance report: the same things a fit is told, minus the search.
type DistanceConfig struct {
	SampleRate int
	Note       int
	Velocity   int

	// Bounds is the search box the report judges pinned dimensions against.
	// Zero-valued bounds select DefaultParamBounds.
	Bounds ParamBounds

	// StrictBounds keeps Bounds as written, as a fit with --bounds does. Off,
	// the box is widened to contain the preset and the report lists what moved.
	StrictBounds bool

	// Analysis is the reference's measured partials for the partial term, or
	// nil to measure them here.
	Analysis *analysis.Measurement
}

// LevelStats describes a signal's level in dBFS. Both are -Inf for silence.
type LevelStats struct {
	PeakDBFS float64 `json:"peak_dbfs"`
	RMSDBFS  float64 `json:"rms_dbfs"`
}

// MarshalJSON writes a silent signal's -Inf levels as null.
func (l LevelStats) MarshalJSON() ([]byte, error) {
	type wire struct {
		PeakDBFS *float64 `json:"peak_dbfs"`
		RMSDBFS  *float64 `json:"rms_dbfs"`
	}

	return json.Marshal(wire{PeakDBFS: finiteOrNil(l.PeakDBFS), RMSDBFS: finiteOrNil(l.RMSDBFS)})
}

// MeasureLevel returns the peak and RMS level of a signal in dBFS.
func MeasureLevel(samples []float32) LevelStats {
	if len(samples) == 0 {
		return LevelStats{PeakDBFS: math.Inf(-1), RMSDBFS: math.Inf(-1)}
	}

	var peak, energy float64

	for _, sample := range samples {
		value := math.Abs(float64(sample))
		peak = math.Max(peak, value)
		energy += value * value
	}

	return LevelStats{
		PeakDBFS: levelDB(peak),
		RMSDBFS:  levelDB(math.Sqrt(energy / float64(len(samples)))),
	}
}

// levelDB is an unfloored dB conversion: silence is -Inf, not the -100 dB the
// spectral metric clamps to, because a level report should not invent a floor.
func levelDB(value float64) float64 {
	if value <= 0 {
		return math.Inf(-1)
	}

	return 20 * math.Log10(value)
}

// DistanceReport is what one written preset scores against one reference
// under each alignment and gain policy the objective offers. It is the number
// a fit would start from, or end at, for that preset -- measured through the
// objective's own code rather than reconstructed from a log line.
type DistanceReport struct {
	SampleRate int `json:"sample_rate"`
	Note       int `json:"note"`
	Velocity   int `json:"velocity"`

	ReferenceSamples int        `json:"reference_samples"`
	Reference        LevelStats `json:"reference"`

	// Render is the level of the preset rendered over the reference's length,
	// before any alignment or gain.
	Render LevelStats `json:"render"`

	Modes     int `json:"modes"`
	Dimension int `json:"dimension"`

	// Raw compares sample for sample at natural level: AlignNone, GainNone.
	Raw Measurement `json:"raw"`
	// Aligned is what a default fit scores: AlignOnsetCorrelation, GainNone.
	Aligned Measurement `json:"aligned"`
	// AlignedGain is a --normalize-gain fit: AlignOnsetCorrelation, GainLeastSquares.
	AlignedGain Measurement `json:"aligned_gain"`

	// Metrics is every term of the composite objective, aligned and with the
	// level gain solved, and Scores is what each named profile makes of them.
	Metrics Metrics            `json:"metrics"`
	Scores  map[string]float64 `json:"scores"`

	// Pinned lists the dimensions of the written preset that sit on an edge
	// of the search box the report was built with.
	Pinned []PinnedDimension `json:"pinned"`

	// Widened lists the edges of the requested box that had to move to contain
	// the preset. Empty under StrictBounds.
	Widened []WidenedBound `json:"widened"`

	// Clamped reports that the written preset lies outside a strict box, so
	// what was scored is the preset pulled into it, not the preset as written.
	Clamped bool `json:"clamped"`
}

// Distance scores a written preset against a reference the way a fit would,
// under every alignment and gain policy, and reports where the preset sits in
// the search box. Nothing is searched: the preset is encoded, rendered and
// measured through the objective exactly once per policy.
func Distance(reference []float32, written *preset.Preset, config DistanceConfig) (*DistanceReport, error) {
	if written == nil {
		return nil, fmt.Errorf("preset cannot be nil")
	}

	bounds := config.Bounds
	if bounds == (ParamBounds{}) {
		bounds = DefaultParamBounds
	}

	policies := []struct {
		alignment AlignmentMode
		gain      GainMode
		target    *Measurement
	}{
		{AlignNone, GainNone, nil},
		{AlignOnsetCorrelation, GainNone, nil},
		{AlignOnsetCorrelation, GainLeastSquares, nil},
	}

	report := &DistanceReport{
		SampleRate:       config.SampleRate,
		Note:             config.Note,
		Velocity:         config.Velocity,
		ReferenceSamples: len(reference),
		Reference:        MeasureLevel(reference),
		Modes:            len(written.Parameters.Modes),
	}
	policies[0].target = &report.Raw
	policies[1].target = &report.Aligned
	policies[2].target = &report.AlignedGain

	for i, policy := range policies {
		objectiveConfig := ObjectiveConfig{
			Metric:       MetricRMS,
			Bounds:       bounds,
			Alignment:    policy.alignment,
			Gain:         policy.gain,
			StrictBounds: config.StrictBounds,
		}

		objective, err := NewObjectiveFunctionWithConfig(reference, written, config.SampleRate, config.Note, config.Velocity, objectiveConfig)
		if err != nil {
			return nil, err
		}

		encoded, err := objective.Codec().EncodeParams(&written.Parameters)
		if err != nil {
			return nil, err
		}

		measurement, err := objective.Measure(encoded)
		if err != nil {
			return nil, err
		}

		*policy.target = measurement

		// The box does not depend on the policy, so read it off the first
		// objective; the raw one also renders exactly the reference's length,
		// which is the length the level figures should describe.
		if i > 0 {
			continue
		}

		codec := objective.Codec()
		report.Dimension = codec.Dimension()
		report.Clamped = !codec.EncodedBounds().Contains(encoded)
		report.Widened = codec.Widened(bounds)

		report.Pinned, err = codec.Pinned(encoded)
		if err != nil {
			return nil, err
		}

		rendered, err := objective.Render(encoded)
		if err != nil {
			return nil, err
		}

		report.Render = MeasureLevel(rendered[:minInt(len(rendered), len(reference))])
	}

	// The composite terms, through an objective built the way a composite fit
	// builds one: aligned, with the gain solved inside the terms.
	objective, err := NewObjectiveFunctionWithConfig(reference, written, config.SampleRate, config.Note, config.Velocity,
		ObjectiveConfig{
			Metric:       MetricBalanced,
			Bounds:       bounds,
			Alignment:    AlignOnsetCorrelation,
			StrictBounds: config.StrictBounds,
			Analysis:     config.Analysis,
		})
	if err != nil {
		return nil, err
	}

	encoded, err := objective.Codec().EncodeParams(&written.Parameters)
	if err != nil {
		return nil, err
	}

	report.Metrics, err = objective.EvaluateMetrics(encoded)
	if err != nil {
		return nil, err
	}

	report.Scores = make(map[string]float64, 3)

	for _, metric := range []Metric{MetricBalanced, MetricPlacement, MetricPolish} {
		profile, _ := ProfileFor(metric)
		report.Scores[string(metric)] = report.Metrics.Score(profile)
	}

	return report, nil
}
