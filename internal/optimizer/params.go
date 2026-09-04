package optimizer

import (
	"fmt"
	"math"
	"sort"

	"github.com/cwbudde/algo-glockenspiel/model"
)

// scalarParameterCount covers input mix and filter frequency; every mode adds
// three more and every Chebyshev gain one.
//
// The base frequency is not among them. It never reaches the audio -- a mode
// plays at its own frequency, and the base is only the tuning the preset's
// note claims -- so searching it was a gauge freedom: every fit had a flat
// ridge through (base, frequency/base) and the justfile told the operator to
// normalise it back to 440 by hand. The codec writes the template's value
// through instead.
const scalarParameterCount = 2

// Range describes an inclusive scalar bound.
type Range struct {
	Min float64
	Max float64
}

// Width returns the numeric width of the range.
func (r Range) Width() float64 {
	return r.Max - r.Min
}

// Clamp forces a value into the range.
func (r Range) Clamp(value float64) float64 {
	if value < r.Min {
		return r.Min
	}

	if value > r.Max {
		return r.Max
	}

	return value
}

// Contains reports whether v lies within the range.
func (r Range) Contains(value float64) bool {
	return value >= r.Min && value <= r.Max
}

// Normalize maps a value from the range into [0,1].
func (r Range) Normalize(value float64) float64 {
	if r.Max == r.Min {
		return 0
	}

	return (r.Clamp(value) - r.Min) / (r.Max - r.Min)
}

// Denormalize maps a [0,1] value back into the range.
func (r Range) Denormalize(value float64) float64 {
	if r.Max == r.Min {
		return r.Min
	}

	if value < 0 {
		value = 0
	}

	if value > 1 {
		value = 1
	}

	return r.Min + value*(r.Max-r.Min)
}

// Bounds describes the encoded vector bounds.
type Bounds struct {
	Ranges []Range
}

// Dimension returns the vector dimensionality.
func (b Bounds) Dimension() int {
	return len(b.Ranges)
}

// CheckVector validates a vector length against the bounds dimension.
func (b Bounds) CheckVector(values []float64) error {
	if len(values) != len(b.Ranges) {
		return fmt.Errorf("expected vector length %d, got %d", len(b.Ranges), len(values))
	}

	return nil
}

// Contains reports whether all values fall within bounds.
func (b Bounds) Contains(values []float64) bool {
	if err := b.CheckVector(values); err != nil {
		return false
	}

	for i, v := range values {
		if !b.Ranges[i].Contains(v) {
			return false
		}
	}

	return true
}

// Clamp returns a bounded copy of values.
func (b Bounds) Clamp(values []float64) ([]float64, error) {
	if err := b.CheckVector(values); err != nil {
		return nil, err
	}

	clamped := make([]float64, len(values))
	for i, v := range values {
		clamped[i] = b.Ranges[i].Clamp(v)
	}

	return clamped, nil
}

// Normalize maps an encoded vector into the unit cube [0,1]^n.
//
// Both optimizer backends search the unit cube rather than raw encoded units:
// encoded widths differ by orders of magnitude (amplitude spans 4, log-decay
// less than 4 decades), so a single step size is meaningless in raw units and
// degenerate along the widest axis.
func (b Bounds) Normalize(values []float64) ([]float64, error) {
	if err := b.CheckVector(values); err != nil {
		return nil, err
	}

	normalized := make([]float64, len(values))
	for i, v := range values {
		normalized[i] = b.Ranges[i].Normalize(v)
	}

	return normalized, nil
}

// Denormalize maps a unit-cube vector back into encoded units. Components
// outside [0,1] are clamped, so the result always satisfies the bounds.
func (b Bounds) Denormalize(values []float64) ([]float64, error) {
	if len(values) != len(b.Ranges) {
		return nil, fmt.Errorf("expected vector length %d, got %d", len(b.Ranges), len(values))
	}

	denormalized := make([]float64, len(values))
	for i, v := range values {
		denormalized[i] = b.Ranges[i].Denormalize(v)
	}

	return denormalized, nil
}

// UnitBounds returns bounds describing the unit cube of the given dimension.
func UnitBounds(dimension int) Bounds {
	ranges := make([]Range, dimension)
	for i := range ranges {
		ranges[i] = Range{Min: 0, Max: 1}
	}

	return Bounds{Ranges: ranges}
}

// ParamBounds defines optimizer bounds in model-space.
//
// Frequency bounds a mode's frequency in hertz, as the preset writes it. It
// replaced a multiplier against the base frequency in Phase 8.3: the
// multiplier's box, times the base frequency's, reached past the model's
// ceiling, and every candidate out there decoded to +Inf -- a plateau a
// population search cannot climb off. An absolute box inside the model's own
// range has no such plateau.
type ParamBounds struct {
	InputMix     Range
	FilterFreq   Range
	Amplitude    Range
	Frequency    Range
	DecayMs      Range
	HarmonicGain Range
}

// The default frequency box is the audible band. A reference's analysis
// narrows it further -- see FrequencyBoundsFor -- and a template outside it
// widens it, as with every other dimension.
const (
	frequencySearchMinHz = 20.0
	frequencySearchMaxHz = 20000.0
)

// DefaultParamBounds are the default optimization bounds for model parameters.
//
// Every dimension except Frequency is the model's own declared range, so
// widening one there widens the search here and nowhere else has to be told.
// DecayMs takes the model's search box and not the far wider
// model.DecayMsValidationMax: the validation ceiling covers what a preset may
// reach once it has been transposed down to the bottom of the keyboard, and
// the objective narrows the box further to what its template's note may be
// authored with.
var DefaultParamBounds = ParamBounds{
	InputMix:     Range{Min: model.InputMixMin, Max: model.InputMixMax},
	FilterFreq:   Range{Min: model.FilterFrequencyMinHz, Max: model.FilterFrequencyMaxHz},
	Amplitude:    Range{Min: model.AmplitudeMin, Max: model.AmplitudeMax},
	Frequency:    Range{Min: frequencySearchMinHz, Max: frequencySearchMaxHz},
	DecayMs:      Range{Min: model.DecayMsSearchMin, Max: model.DecayMsSearchMax},
	HarmonicGain: Range{Min: model.HarmonicGainMin, Max: model.HarmonicGainMax},
}

// ParamCodec encodes BarParams into a flat optimization vector.
//
// The mode count comes from the template rather than a constant, so a codec
// built from a nine-mode preset searches a nine-mode space.
//
// Modes are kept in ascending order of frequency: EncodeParams sorts them
// before encoding and DecodeParams sorts them after. The search is free to
// swap two slots, but what it writes is always the same list for the same
// sound, and a population seeded from an ordered starting point stays in one
// ordering rather than spending its diversity on relabelings of the same
// answer. The permutation symmetry is not removed from the box -- an encoding
// that did that chains every mode to the one below it -- it is removed from
// what leaves the codec.
//
// Per-mode harmonic gains and the Chebyshev stage are carried through from the
// template unchanged: the bank supports them, but they are not part of the
// search space yet. Dropping the stage would silently re-render an
// output-stage preset through the excitation-stage chain. The base frequency
// is carried through for the reason given at scalarParameterCount.
//
// The output gain is carried for the same reason as the stage, and it is worth
// spelling out because dropping it is quiet rather than loud. It is not a
// search dimension -- the objective solves the level in closed form and
// subtracts it from every term, so there is no gradient along it to follow --
// but a decoded candidate still has to describe the same sound the template
// does. When it did not, a preset carrying a gain rendered without one, and
// since the objective divides the level out again the scores looked perfectly
// normal: the only thing that moved was every level a caller read back, so
// glockenspiel distance reported a fitted preset's render peak 28 dB below
// where the preset actually plays.
type ParamCodec struct {
	modeCount        int
	harmonicCount    int
	chebyshevEnabled bool
	chebyshevStage   model.ChebyshevStage
	baseFrequency    float64
	outputGainDB     float64
	modeHarmonics    [][]float64
	bounds           ParamBounds
}

// NewParamCodec builds a codec from a validated parameter template.
func NewParamCodec(params *model.BarParams) (*ParamCodec, error) {
	return NewParamCodecWithBounds(params, DefaultParamBounds)
}

// NewParamCodecWithBounds builds a codec using explicit model-space bounds,
// widening them where the template parameters fall outside.
func NewParamCodecWithBounds(params *model.BarParams, bounds ParamBounds) (*ParamCodec, error) {
	return newParamCodec(params, bounds, false)
}

// NewParamCodecWithStrictBounds builds a codec that treats the bounds as a hard
// constraint. Template parameters outside the box are not allowed to widen it;
// the encoded starting point is clamped into it instead, so the search -- and
// the decoded result -- stay within the range the caller asked for.
func NewParamCodecWithStrictBounds(params *model.BarParams, bounds ParamBounds) (*ParamCodec, error) {
	return newParamCodec(params, bounds, true)
}

func newParamCodec(params *model.BarParams, bounds ParamBounds, strict bool) (*ParamCodec, error) {
	if err := model.ValidateBarParams(params); err != nil {
		return nil, err
	}

	if err := bounds.Validate(); err != nil {
		return nil, err
	}

	if !strict {
		bounds = bounds.expandToInclude(params)
	}

	// The harmonics ride with the slot, so they are taken in the order the
	// slots are encoded in, which is the template's modes sorted by frequency.
	sorted := sortedModes(params.Modes)

	modeHarmonics := make([][]float64, len(sorted))
	for i, mode := range sorted {
		if len(mode.Harmonics) > 0 {
			modeHarmonics[i] = append([]float64(nil), mode.Harmonics...)
		}
	}

	return &ParamCodec{
		modeCount:        len(params.Modes),
		harmonicCount:    len(params.Chebyshev.HarmonicGains),
		chebyshevEnabled: params.Chebyshev.Enabled,
		chebyshevStage:   params.Chebyshev.Stage,
		baseFrequency:    params.BaseFrequency,
		outputGainDB:     params.OutputGainDB,
		modeHarmonics:    modeHarmonics,
		bounds:           bounds,
	}, nil
}

// ModeCount returns the mode count this codec encodes.
func (c *ParamCodec) ModeCount() int {
	return c.modeCount
}

// BaseFrequency returns the base frequency the codec writes through, which is
// the template's.
func (c *ParamCodec) BaseFrequency() float64 {
	return c.baseFrequency
}

// sortedModes returns a copy of modes in ascending order of frequency. The
// sort is stable, so two modes at one frequency keep the order they came in.
func sortedModes(modes []model.ModeParams) []model.ModeParams {
	sorted := append([]model.ModeParams(nil), modes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Frequency < sorted[j].Frequency
	})

	return sorted
}

// Validate checks that the bounds are well-formed.
func (b ParamBounds) Validate() error {
	ranges := map[string]Range{
		"input_mix":     b.InputMix,
		"filter_freq":   b.FilterFreq,
		"amplitude":     b.Amplitude,
		"frequency":     b.Frequency,
		"decay_ms":      b.DecayMs,
		"harmonic_gain": b.HarmonicGain,
	}
	for name, valueRange := range ranges {
		if math.IsNaN(valueRange.Min) || math.IsNaN(valueRange.Max) || math.IsInf(valueRange.Min, 0) || math.IsInf(valueRange.Max, 0) {
			return fmt.Errorf("%s bounds must be finite", name)
		}

		if valueRange.Min > valueRange.Max {
			return fmt.Errorf("%s bounds invalid: min %g > max %g", name, valueRange.Min, valueRange.Max)
		}

		if valueRange.Min <= 0 && (name == "filter_freq" || name == "frequency" || name == "decay_ms") {
			return fmt.Errorf("%s bounds must be > 0 for log encoding", name)
		}
	}

	return nil
}

func (b ParamBounds) expandToInclude(params *model.BarParams) ParamBounds {
	b.InputMix = expandRange(b.InputMix, params.InputMix)
	b.FilterFreq = expandRange(b.FilterFreq, params.FilterFrequency)

	for _, mode := range params.Modes {
		b.Amplitude = expandRange(b.Amplitude, mode.Amplitude)
		b.Frequency = expandRange(b.Frequency, mode.Frequency)
		b.DecayMs = expandRange(b.DecayMs, mode.DecayMs)
	}

	for _, gain := range params.Chebyshev.HarmonicGains {
		b.HarmonicGain = expandRange(b.HarmonicGain, gain)
	}

	return b
}

// Dimension returns the encoded vector dimensionality.
func (c *ParamCodec) Dimension() int {
	return scalarParameterCount + c.modeCount*3 + c.harmonicCount
}

// BlockGroups partitions the encoded dimensions into covariance blocks for the
// CMA-ES backend's block mode.
//
// A mode's amplitude, frequency and decay are the one group of dimensions that
// genuinely trade against each other: moving a partial in frequency changes
// which reference partial it is fitting, and the amplitude and decay that
// match follow from that. Correlations between different modes are far weaker,
// so a dense block per mode buys the structure that matters at three-by-three
// cost instead of the full matrix's. The scalars and the Chebyshev gains share
// the remaining group; they are few, and they act on the whole bar rather than
// on one partial.
func (c *ParamCodec) BlockGroups() [][]int {
	shared := make([]int, 0, scalarParameterCount+c.harmonicCount)
	for i := range scalarParameterCount {
		shared = append(shared, i)
	}

	groups := make([][]int, 0, c.modeCount+1)

	for mode := range c.modeCount {
		base := scalarParameterCount + mode*3
		groups = append(groups, []int{base, base + 1, base + 2})
	}

	for i := scalarParameterCount + c.modeCount*3; i < c.Dimension(); i++ {
		shared = append(shared, i)
	}

	return append(groups, shared)
}

// EncodedBounds returns the bounds for encoded vectors.
func (c *ParamCodec) EncodedBounds() Bounds {
	ranges := make([]Range, 0, c.Dimension())

	ranges = append(
		ranges,
		c.bounds.InputMix,
		logRange(c.bounds.FilterFreq),
	)
	for range c.modeCount {
		ranges = append(
			ranges,
			c.bounds.Amplitude,
			logRange(c.bounds.Frequency),
			logRange(c.bounds.DecayMs),
		)
	}

	for range c.harmonicCount {
		ranges = append(ranges, c.bounds.HarmonicGain)
	}

	return Bounds{Ranges: ranges}
}

// EncodeParams converts validated model parameters to an optimization vector.
func (c *ParamCodec) EncodeParams(params *model.BarParams) ([]float64, error) {
	if err := model.ValidateBarParams(params); err != nil {
		return nil, err
	}

	if len(params.Modes) != c.modeCount {
		return nil, fmt.Errorf("expected %d modes, got %d", c.modeCount, len(params.Modes))
	}

	if len(params.Chebyshev.HarmonicGains) != c.harmonicCount {
		return nil, fmt.Errorf("expected %d harmonic gains, got %d", c.harmonicCount, len(params.Chebyshev.HarmonicGains))
	}

	encoded := make([]float64, 0, c.Dimension())

	encoded = append(
		encoded,
		params.InputMix,
		math.Log10(params.FilterFrequency),
	)
	for _, mode := range sortedModes(params.Modes) {
		// Decay is log-encoded like the frequencies: a decay constant is a
		// ratio-scale quantity, so 10ms->20ms must cost the same search step as
		// 200ms->400ms.
		if mode.Frequency <= 0 || mode.DecayMs <= 0 {
			return nil, fmt.Errorf("mode frequency and decay must be > 0")
		}

		encoded = append(
			encoded,
			mode.Amplitude,
			math.Log10(mode.Frequency),
			math.Log10(mode.DecayMs),
		)
	}

	encoded = append(encoded, params.Chebyshev.HarmonicGains...)

	return encoded, nil
}

// DecodeParams converts an optimization vector back to model parameters.
func (c *ParamCodec) DecodeParams(encoded []float64) (*model.BarParams, error) {
	if len(encoded) != c.Dimension() {
		return nil, fmt.Errorf("expected encoded length %d, got %d", c.Dimension(), len(encoded))
	}

	bounded, err := c.EncodedBounds().Clamp(encoded)
	if err != nil {
		return nil, err
	}

	// A log10/pow round trip lands an ulp outside the box at its edges --
	// 10^log10(20) is 19.999999999999993 -- and the model would refuse the
	// corner. The model-space value is clamped into the codec's own box,
	// which lies inside the model's range.
	params := &model.BarParams{
		InputMix:        bounded[0],
		FilterFrequency: c.bounds.FilterFreq.Clamp(math.Pow(10, bounded[1])),
		BaseFrequency:   c.baseFrequency,
		OutputGainDB:    c.outputGainDB,
		Modes:           make([]model.ModeParams, c.modeCount),
		Chebyshev: model.ChebyshevParams{
			Enabled:       c.chebyshevEnabled,
			Stage:         c.chebyshevStage,
			HarmonicGains: make([]float64, c.harmonicCount),
		},
	}

	index := scalarParameterCount

	for i := range c.modeCount {
		params.Modes[i] = model.ModeParams{
			Amplitude: bounded[index],
			Frequency: c.bounds.Frequency.Clamp(math.Pow(10, bounded[index+1])),
			DecayMs:   c.bounds.DecayMs.Clamp(math.Pow(10, bounded[index+2])),
		}

		if len(c.modeHarmonics[i]) > 0 {
			params.Modes[i].Harmonics = append([]float64(nil), c.modeHarmonics[i]...)
		}

		index += 3
	}

	params.Modes = sortedModes(params.Modes)

	copy(params.Chebyshev.HarmonicGains, bounded[index:])

	if err := model.ValidateBarParams(params); err != nil {
		return nil, err
	}

	return params, nil
}

// EncodeParams converts validated model parameters to an optimization vector.
func EncodeParams(params *model.BarParams) ([]float64, error) {
	codec, err := NewParamCodec(params)
	if err != nil {
		return nil, err
	}

	return codec.EncodeParams(params)
}

// DecodeParams reconstructs model parameters from an encoded vector and template metadata.
func DecodeParams(encoded []float64, template *model.BarParams) (*model.BarParams, error) {
	codec, err := NewParamCodec(template)
	if err != nil {
		return nil, err
	}

	return codec.DecodeParams(encoded)
}

func logRange(r Range) Range {
	return Range{
		Min: math.Log10(r.Min),
		Max: math.Log10(r.Max),
	}
}

func expandRange(valueRange Range, value float64) Range {
	if value < valueRange.Min {
		valueRange.Min = value
	}

	if value > valueRange.Max {
		valueRange.Max = value
	}

	return valueRange
}

// Bounds returns the model-space bounds the codec encodes with. Unless the
// codec was built strict, these are the requested bounds widened to contain
// the template, so they can differ from what the caller asked for.
func (c *ParamCodec) Bounds() ParamBounds {
	return c.bounds
}

// DimensionNames labels every encoded dimension in encoding order, using the
// preset's own field names so that a report can say which parameter a vector
// entry is.
func (c *ParamCodec) DimensionNames() []string {
	names := make([]string, 0, c.Dimension())
	names = append(names, "input_mix", "filter_frequency")

	for i := range c.modeCount {
		names = append(names,
			fmt.Sprintf("modes[%d].amplitude", i),
			fmt.Sprintf("modes[%d].frequency", i),
			fmt.Sprintf("modes[%d].decay_ms", i),
		)
	}

	for i := range c.harmonicCount {
		names = append(names, fmt.Sprintf("chebyshev.harmonic_gains[%d]", i))
	}

	return names
}

// PinnedDimension describes an encoded dimension sitting on an edge of the
// search box, in the units the preset is written in.
type PinnedDimension struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	// Bound is "min" or "max".
	Bound string  `json:"bound"`
	Limit float64 `json:"limit"`
}

// pinnedTolerance is how close to an edge, in unit-cube coordinates, counts
// as being on it. A value written from a clamped search sits exactly on the
// edge; the tolerance only absorbs the rounding of a log10/pow round trip
// through JSON, which is many orders of magnitude smaller.
const pinnedTolerance = 1e-6

// Pinned reports which dimensions of an encoded vector sit on an edge of the
// codec's box. A search that clamps at the boundary leaves its result there,
// so a pinned dimension is one the search wanted to push further.
//
// Mode dimensions are named by the index the mode has once decoded -- sorted
// by frequency -- so the name matches the preset the vector writes, not the
// slot the search happened to keep it in.
func (c *ParamCodec) Pinned(encoded []float64) ([]PinnedDimension, error) {
	bounds := c.EncodedBounds()

	unit, err := bounds.Normalize(encoded)
	if err != nil {
		return nil, err
	}

	names := c.writtenDimensionNames(encoded)
	pinned := make([]PinnedDimension, 0)

	for i, value := range unit {
		var side string

		switch {
		case value <= pinnedTolerance:
			side = "min"
		case value >= 1-pinnedTolerance:
			side = "max"
		default:
			continue
		}

		limit := bounds.Ranges[i].Min
		if side == "max" {
			limit = bounds.Ranges[i].Max
		}

		pinned = append(pinned, PinnedDimension{
			Name:  names[i],
			Value: c.modelValue(i, encoded[i]),
			Bound: side,
			Limit: c.modelValue(i, limit),
		})
	}

	return pinned, nil
}

// writtenDimensionNames is DimensionNames with every mode renamed to the
// index it takes in the decoded preset, which sorts the modes by frequency.
func (c *ParamCodec) writtenDimensionNames(encoded []float64) []string {
	names := c.DimensionNames()

	if len(encoded) != c.Dimension() || c.modeCount == 0 {
		return names
	}

	slots := make([]int, c.modeCount)
	for i := range slots {
		slots[i] = i
	}

	frequency := func(slot int) float64 {
		return encoded[scalarParameterCount+slot*3+1]
	}

	sort.SliceStable(slots, func(i, j int) bool {
		return frequency(slots[i]) < frequency(slots[j])
	})

	for written, slot := range slots {
		base := scalarParameterCount + slot*3
		names[base] = fmt.Sprintf("modes[%d].amplitude", written)
		names[base+1] = fmt.Sprintf("modes[%d].frequency", written)
		names[base+2] = fmt.Sprintf("modes[%d].decay_ms", written)
	}

	return names
}

// modelValue converts one encoded coordinate back to the units the preset is
// written in, undoing the log10 encoding where the codec applied it.
func (c *ParamCodec) modelValue(dimension int, encoded float64) float64 {
	if c.isLogDimension(dimension) {
		return math.Pow(10, encoded)
	}

	return encoded
}

// isLogDimension reports whether a dimension is log10-encoded: the filter
// frequency and, per mode, the frequency and the decay.
func (c *ParamCodec) isLogDimension(dimension int) bool {
	if dimension < scalarParameterCount {
		return dimension > 0
	}

	if dimension >= scalarParameterCount+c.modeCount*3 {
		return false
	}

	return (dimension-scalarParameterCount)%3 != 0
}

// WidenedBound records one edge of the search box the codec moved so that the
// template would fit inside it.
type WidenedBound struct {
	Name string  `json:"name"`
	Side string  `json:"side"`
	From float64 `json:"from"`
	To   float64 `json:"to"`
}

// Widened compares the codec's box with the one that was requested and lists
// every edge that moved. A non-strict codec widens silently, so this is how a
// caller finds out that the bounds it documented are not the bounds that ran.
func (c *ParamCodec) Widened(requested ParamBounds) []WidenedBound {
	type namedRange struct {
		name      string
		requested Range
		actual    Range
	}

	pairs := []namedRange{
		{"input_mix", requested.InputMix, c.bounds.InputMix},
		{"filter_freq", requested.FilterFreq, c.bounds.FilterFreq},
		{"amplitude", requested.Amplitude, c.bounds.Amplitude},
		{"frequency", requested.Frequency, c.bounds.Frequency},
		{"decay_ms", requested.DecayMs, c.bounds.DecayMs},
		{"harmonic_gain", requested.HarmonicGain, c.bounds.HarmonicGain},
	}

	widened := make([]WidenedBound, 0)

	for _, pair := range pairs {
		if pair.actual.Min < pair.requested.Min {
			widened = append(widened, WidenedBound{Name: pair.name, Side: "min", From: pair.requested.Min, To: pair.actual.Min})
		}

		if pair.actual.Max > pair.requested.Max {
			widened = append(widened, WidenedBound{Name: pair.name, Side: "max", From: pair.requested.Max, To: pair.actual.Max})
		}
	}

	return widened
}
