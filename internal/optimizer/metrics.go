package optimizer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// Term names one raw measurement of the composite objective.
type Term string

const (
	// TermPartialCents is the level-weighted RMS pitch error, in cents, over
	// the reference partials that found a model partial within
	// partialMatchCents.
	TermPartialCents Term = "partial_cents"

	// TermPartialLevel is the level-weighted RMS error, in dB, between the
	// matched partials' relative levels once the mean offset between the two
	// lists is solved out.
	TermPartialLevel Term = "partial_level_db"

	// TermPartialDecay is the level-weighted RMS of log2 of the half-life
	// ratio over the matched partials, in octaves: one means every matched
	// mode rings twice or half as long as it should.
	TermPartialDecay Term = "partial_decay_octaves"

	// TermPartialMissing is the fraction of the reference's partial weight
	// -- level above the floor, summed -- that no model partial matched.
	TermPartialMissing Term = "partial_missing"

	// TermPartialExtra is the weight of model partials above the floor that
	// matched no reference partial, as a fraction of the reference's weight.
	// A model that fakes an attack with a cluster of beating modes pays here.
	TermPartialExtra Term = "partial_extra"

	// TermSpectralFine is the noise-aware log-spectral error at the fine
	// resolution, in dB, where a partial's placement is resolved.
	TermSpectralFine Term = "spectral_fine_db"

	// TermSpectralCoarse is the same at the coarse resolution, where the
	// frame-to-frame envelope of each partial is.
	TermSpectralCoarse Term = "spectral_coarse_db"

	// TermEnvelope is the RMS error, in dB, between the broadband envelopes
	// over log-spaced windows from the strike.
	TermEnvelope Term = "envelope_db"

	// TermOnset is the RMS error, in dB, between the third-octave band
	// levels of the strike's first eleven milliseconds.
	//
	// It exists because nothing else in this list hears the attack. The
	// envelope term is broadband, so a candidate can miss the mallet's 4-8 kHz
	// band by fifty decibels and still track the envelope to within one, and
	// the spectral terms average over the whole strike, where an eleven
	// millisecond click is a rounding error against a half-second tail. Both
	// were measured doing exactly that on the morphagene C6 sample.
	TermOnset Term = "onset_db"

	// TermDecaySlope is the difference between the broadband decay slopes,
	// in dB per second.
	TermDecaySlope Term = "decay_slope_dbps"

	// TermWaveform is the aligned waveform residual after the least-squares
	// gain, as a fraction of the reference RMS: zero is the same waveform,
	// one is no correlation at all.
	TermWaveform Term = "waveform"
)

// Terms lists every term in reporting order.
func Terms() []Term {
	return []Term{
		TermPartialCents, TermPartialLevel, TermPartialDecay, TermPartialMissing, TermPartialExtra,
		TermSpectralFine, TermSpectralCoarse,
		TermEnvelope, TermOnset, TermDecaySlope,
		TermWaveform,
	}
}

// Unit is the unit a term is measured in, for reports.
func (t Term) Unit() string {
	switch t {
	case TermPartialCents:
		return "cents"
	case TermPartialLevel, TermSpectralFine, TermSpectralCoarse, TermEnvelope, TermOnset:
		return "dB"
	case TermPartialDecay:
		return "octaves"
	case TermDecaySlope:
		return "dB/s"
	default:
		return ""
	}
}

// Metrics is every raw term the composite objective measures for one
// candidate, in physical units, plus what was solved or decided along the
// way. A term that could not be measured -- a reference too short for a
// spectral frame, no partials to match -- is NaN and is left out of a score.
type Metrics struct {
	PartialCents        float64
	PartialLevelDB      float64
	PartialDecayOctaves float64
	PartialMissing      float64
	PartialExtra        float64
	SpectralFineDB      float64
	SpectralCoarseDB    float64
	EnvelopeDB          float64
	OnsetDB             float64
	DecaySlopeDBps      float64
	Waveform            float64

	// GainDB is the level gain solved in closed form and applied to the
	// candidate before the spectral and envelope terms: the ratio of the
	// reference RMS to the candidate RMS over the aligned overlap.
	GainDB float64

	// WaveformGainDB is the least-squares waveform gain, which the waveform
	// term uses and nothing else does. It is the diagnostic that says whether
	// the waveforms correlate at all: on a recording it sits tens of dB below
	// GainDB.
	WaveformGainDB float64

	// Lag is the alignment applied, in samples; positive means the candidate
	// started late. Overlap is the span the terms were taken over.
	Lag     int
	Overlap int

	// ReferencePartials, ModelPartials and Matched count the partial lists
	// and how many pairs the matching found.
	ReferencePartials int
	ModelPartials     int
	Matched           int
}

// Value returns one term.
func (m Metrics) Value(term Term) float64 {
	switch term {
	case TermPartialCents:
		return m.PartialCents
	case TermPartialLevel:
		return m.PartialLevelDB
	case TermPartialDecay:
		return m.PartialDecayOctaves
	case TermPartialMissing:
		return m.PartialMissing
	case TermPartialExtra:
		return m.PartialExtra
	case TermSpectralFine:
		return m.SpectralFineDB
	case TermSpectralCoarse:
		return m.SpectralCoarseDB
	case TermEnvelope:
		return m.EnvelopeDB
	case TermOnset:
		return m.OnsetDB
	case TermDecaySlope:
		return m.DecaySlopeDBps
	case TermWaveform:
		return m.Waveform
	default:
		return math.NaN()
	}
}

func (m *Metrics) set(term Term, value float64) {
	switch term {
	case TermPartialCents:
		m.PartialCents = value
	case TermPartialLevel:
		m.PartialLevelDB = value
	case TermPartialDecay:
		m.PartialDecayOctaves = value
	case TermPartialMissing:
		m.PartialMissing = value
	case TermPartialExtra:
		m.PartialExtra = value
	case TermSpectralFine:
		m.SpectralFineDB = value
	case TermSpectralCoarse:
		m.SpectralCoarseDB = value
	case TermEnvelope:
		m.EnvelopeDB = value
	case TermOnset:
		m.OnsetDB = value
	case TermDecaySlope:
		m.DecaySlopeDBps = value
	case TermWaveform:
		m.Waveform = value
	}
}

// UnmeasuredMetrics is a Metrics with every term NaN, the state before any term
// has been taken.
//
// It is exported because it is also the honest value for a result that has no
// single set of terms rather than none yet: a joint fit averages per-note
// scores, so writing zeros -- which read as perfect terms -- or the first
// note's terms would both be worse than saying nothing.
func UnmeasuredMetrics() Metrics {
	return unmeasuredMetrics()
}

// unmeasuredMetrics is a Metrics with every term NaN, the state before any
// term has been taken.
func unmeasuredMetrics() Metrics {
	var metrics Metrics

	for _, term := range Terms() {
		metrics.set(term, math.NaN())
	}

	metrics.GainDB = math.NaN()
	metrics.WaveformGainDB = math.NaN()

	return metrics
}

// Score folds the terms into one number under a profile.
//
// Each measured term is scaled by its norm and passed through x/(1+x), which
// is zero for a perfect term, one half at the norm and approaches one -- so a
// term can never swamp the others however wrong it is, and a term cannot
// saturate unless it sits many norms out, which is what the norms are chosen
// to prevent. The result is the weighted mean of those, in [0, 1], over the
// terms that were measured: an unmeasurable term is left out and the weights
// of the rest are renormalised, so a short reference scores on the same scale
// as a long one. +Inf when no term at all could be measured.
func (m Metrics) Score(profile Profile) float64 {
	var weighted, weightTotal float64

	for _, term := range Terms() {
		weight := profile.Weights[term]
		if weight <= 0 {
			continue
		}

		value := m.Value(term)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}

		weighted += weight * saturate(value/profile.norm(term))
		weightTotal += weight
	}

	if weightTotal == 0 {
		return math.Inf(1)
	}

	return weighted / weightTotal
}

// saturate maps a norm-scaled term onto [0, 1).
func saturate(x float64) float64 {
	if x <= 0 {
		return 0
	}

	return x / (1 + x)
}

// Contribution is what one term added to a score.
type Contribution struct {
	Term   Term    `json:"term"`
	Value  float64 `json:"value"`
	Weight float64 `json:"weight"`
	Norm   float64 `json:"norm"`

	// Scaled is the term after the norm and the saturation, in [0, 1).
	Scaled float64 `json:"scaled"`

	// Share is Weight times Scaled over the total weight of the measured
	// terms, so the shares of the measured terms sum to the score.
	Share float64 `json:"share"`

	// Measured is false for a term that could not be taken and is left out.
	Measured bool `json:"measured"`
}

// Contributions breaks a score down term by term, in reporting order, for
// every term the profile weights.
func (m Metrics) Contributions(profile Profile) []Contribution {
	var weightTotal float64

	for _, term := range Terms() {
		value := m.Value(term)
		if profile.Weights[term] > 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			weightTotal += profile.Weights[term]
		}
	}

	var contributions []Contribution

	for _, term := range Terms() {
		weight := profile.Weights[term]
		if weight <= 0 {
			continue
		}

		value := m.Value(term)
		contribution := Contribution{Term: term, Value: value, Weight: weight, Norm: profile.norm(term)}

		if !math.IsNaN(value) && !math.IsInf(value, 0) {
			contribution.Measured = true
			contribution.Scaled = saturate(value / contribution.Norm)

			if weightTotal > 0 {
				contribution.Share = weight * contribution.Scaled / weightTotal
			}
		}

		contributions = append(contributions, contribution)
	}

	return contributions
}

// MarshalJSON writes the terms in reporting order with an unmeasured term as
// null, which encoding/json cannot do for a NaN on its own.
func (m Metrics) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer

	buffer.WriteByte('{')

	for i, term := range Terms() {
		if i > 0 {
			buffer.WriteByte(',')
		}

		writeJSONFloat(&buffer, string(term), m.Value(term))
	}

	buffer.WriteByte(',')
	writeJSONFloat(&buffer, "gain_db", m.GainDB)
	buffer.WriteByte(',')
	writeJSONFloat(&buffer, "waveform_gain_db", m.WaveformGainDB)

	_, _ = fmt.Fprintf(&buffer, `,"lag":%d,"overlap":%d,"reference_partials":%d,"model_partials":%d,"matched":%d}`,
		m.Lag, m.Overlap, m.ReferencePartials, m.ModelPartials, m.Matched)

	return buffer.Bytes(), nil
}

func writeJSONFloat(buffer *bytes.Buffer, name string, value float64) {
	buffer.WriteByte('"')
	buffer.WriteString(name)
	buffer.WriteString(`":`)

	if math.IsNaN(value) || math.IsInf(value, 0) {
		buffer.WriteString("null")

		return
	}

	buffer.WriteString(strconv.FormatFloat(value, 'g', -1, 64))
}

// UnmarshalJSON reads the null back as NaN.
func (m *Metrics) UnmarshalJSON(data []byte) error {
	var wire struct {
		Terms             map[string]*float64 `json:"-"`
		GainDB            *float64            `json:"gain_db"`
		WaveformGainDB    *float64            `json:"waveform_gain_db"`
		Lag               int                 `json:"lag"`
		Overlap           int                 `json:"overlap"`
		ReferencePartials int                 `json:"reference_partials"`
		ModelPartials     int                 `json:"model_partials"`
		Matched           int                 `json:"matched"`
	}

	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	var terms map[string]*float64
	if err := json.Unmarshal(data, &terms); err != nil {
		return err
	}

	metrics := unmeasuredMetrics()

	for _, term := range Terms() {
		if value, ok := terms[string(term)]; ok && value != nil {
			metrics.set(term, *value)
		}
	}

	metrics.GainDB = derefOrNaN(wire.GainDB)
	metrics.WaveformGainDB = derefOrNaN(wire.WaveformGainDB)
	metrics.Lag = wire.Lag
	metrics.Overlap = wire.Overlap
	metrics.ReferencePartials = wire.ReferencePartials
	metrics.ModelPartials = wire.ModelPartials
	metrics.Matched = wire.Matched

	*m = metrics

	return nil
}

func derefOrNaN(value *float64) float64 {
	if value == nil {
		return math.NaN()
	}

	return *value
}

// Profile is how a score weighs the terms: a weight per term, and the norm
// at which a term counts as half wrong.
type Profile struct {
	Name    string           `json:"name"`
	Weights map[Term]float64 `json:"weights"`

	// Norms may leave a term out, in which case DefaultNorms applies.
	Norms map[Term]float64 `json:"norms,omitempty"`
}

// Norm is the value at which a term scores one half under this profile: the
// profile's own if it names one, and the default otherwise. It is exported
// because a display of the terms beside the score has to scale them by the
// same number the score used, and re-deriving DefaultNorms elsewhere is how
// the two quietly stop agreeing.
func (p Profile) Norm(term Term) float64 {
	return p.norm(term)
}

func (p Profile) norm(term Term) float64 {
	if norm, ok := p.Norms[term]; ok && norm > 0 {
		return norm
	}

	return DefaultNorms[term]
}

// DefaultNorms is the value of each term at which it scores one half.
//
// They were set against the shipped presets scored on both references
// (docs/training.md, "The composite objective on the shipped presets") so
// that no term of the balanced profile saturates there: on every row a
// shipped preset scores against the reference it was fitted to, or at its
// nearest note, each term sits between a tenth of its norm and about twice
// it, where the score still moves when the term does. Ten cents is a third
// of the pitch difference that lost the waveform entirely; six decibels of
// level and half an octave of half-life are errors a listener names; the
// spectral terms sit at their norm for the recorded bar at its best note and
// at half of it for the one fit that is known to be right; the extra norm
// is twice the missing norm because a model may carry any number of modes
// while a reference's partial weight is capped at one.
//
// The onset norm is the outlier, and deliberately so. Measured the same way,
// every shipped preset misses its reference's strike spectrum by 19 to 33 dB,
// the default against the render it fits included -- that one scores 4.7 dB
// spectrally and 0.3 dB on the envelope and still 20.2 dB here. Fifteen puts
// those rows between one and two norms, where the score still moves when the
// term does; a norm chosen for what a good attack would cost instead would
// saturate on every preset the repository has, and a saturated term is one
// the search cannot follow.
var DefaultNorms = map[Term]float64{
	TermPartialCents:   10,
	TermPartialLevel:   6,
	TermPartialDecay:   0.5,
	TermPartialMissing: 0.5,
	TermPartialExtra:   1,
	TermSpectralFine:   10,
	TermSpectralCoarse: 10,
	TermEnvelope:       3,
	TermOnset:          15,
	TermDecaySlope:     10,
	TermWaveform:       0.5,
}

// The named profiles. The weights of each sum to one, so a score is a
// weighted mean and the shares in a breakdown are fractions of it.
var (
	// ProfileBalanced is the default: partials 0.36, spectrum 0.22, envelope
	// and slope 0.22, onset 0.10, waveform 0.10.
	ProfileBalanced = Profile{
		Name: string(MetricBalanced),
		Weights: map[Term]float64{
			TermPartialCents:   0.11,
			TermPartialLevel:   0.07,
			TermPartialDecay:   0.07,
			TermPartialMissing: 0.06,
			TermPartialExtra:   0.05,
			TermSpectralFine:   0.11,
			TermSpectralCoarse: 0.11,
			TermEnvelope:       0.13,
			TermOnset:          0.10,
			TermDecaySlope:     0.09,
			TermWaveform:       0.10,
		},
	}

	// ProfilePlacement is for the global stage: it cares where the partials
	// are and whether they are all there, and hardly at all about the phase.
	ProfilePlacement = Profile{
		Name: string(MetricPlacement),
		Weights: map[Term]float64{
			TermPartialCents:   0.22,
			TermPartialLevel:   0.09,
			TermPartialDecay:   0.05,
			TermPartialMissing: 0.14,
			TermPartialExtra:   0.13,
			TermSpectralFine:   0.13,
			TermSpectralCoarse: 0.05,
			TermEnvelope:       0.05,
			TermOnset:          0.09,
			TermWaveform:       0.05,
		},
	}

	// ProfilePolish is for a local stage that starts near the answer: half
	// the weight on the waveform, which only makes sense once every partial
	// is within a few cents.
	ProfilePolish = Profile{
		Name: string(MetricPolish),
		Weights: map[Term]float64{
			TermPartialCents:   0.09,
			TermPartialLevel:   0.05,
			TermPartialDecay:   0.04,
			TermPartialMissing: 0.04,
			TermPartialExtra:   0.04,
			TermSpectralFine:   0.05,
			TermSpectralCoarse: 0.05,
			TermEnvelope:       0.05,
			TermOnset:          0.05,
			TermDecaySlope:     0.04,
			TermWaveform:       0.50,
		},
	}
)

// ProfileFor returns the profile a composite metric names.
func ProfileFor(metric Metric) (Profile, bool) {
	switch metric {
	case MetricBalanced:
		return ProfileBalanced, true
	case MetricPlacement:
		return ProfilePlacement, true
	case MetricPolish:
		return ProfilePolish, true
	default:
		return Profile{}, false
	}
}

// Composite reports whether the metric is a profile of the composite
// objective rather than one of the single-term legacy metrics.
func (m Metric) Composite() bool {
	_, ok := ProfileFor(m)

	return ok
}
