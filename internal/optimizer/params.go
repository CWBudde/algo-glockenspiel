package optimizer

import (
	"fmt"
	"math"

	"github.com/cwbudde/glockenspiel/internal/model"
)

const fixedParameterCount = 3 + model.NumModes*3

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

// Mirror reflects v into the range.
func (r Range) Mirror(value float64) float64 {
	if math.IsNaN(value) {
		return r.Min
	}

	if math.IsInf(value, 0) {
		return r.Clamp(value)
	}

	if r.Min == r.Max {
		return r.Min
	}

	width := r.Max - r.Min
	for value < r.Min || value > r.Max {
		if value < r.Min {
			value = r.Min + (r.Min - value)
			continue
		}

		if value > r.Max {
			value = r.Max - (value - r.Max)
		}

		if width == 0 {
			return r.Min
		}
	}

	return value
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

// Mirror returns a reflected copy of values.
func (b Bounds) Mirror(values []float64) ([]float64, error) {
	if err := b.CheckVector(values); err != nil {
		return nil, err
	}

	mirrored := make([]float64, len(values))
	for i, v := range values {
		mirrored[i] = b.Ranges[i].Mirror(v)
	}

	return mirrored, nil
}

// ParamBounds defines optimizer bounds in model-space.
type ParamBounds struct {
	InputMix      Range
	FilterFreq    Range
	BaseFrequency Range
	Amplitude     Range
	FrequencyMult Range
	DecayMs       Range
	HarmonicGain  Range
}

// DefaultParamBounds are the default optimization bounds for model parameters.
var DefaultParamBounds = ParamBounds{
	InputMix:      Range{Min: model.DefaultParamBounds.InputMix[0], Max: model.DefaultParamBounds.InputMix[1]},
	FilterFreq:    Range{Min: model.DefaultParamBounds.FilterFreq[0], Max: model.DefaultParamBounds.FilterFreq[1]},
	BaseFrequency: Range{Min: model.FrequencyMinHz, Max: model.FrequencyMaxHz},
	Amplitude:     Range{Min: model.DefaultParamBounds.Amplitude[0], Max: model.DefaultParamBounds.Amplitude[1]},
	FrequencyMult: Range{Min: model.DefaultParamBounds.FrequencyMult[0], Max: model.DefaultParamBounds.FrequencyMult[1]},
	DecayMs:       Range{Min: model.DefaultParamBounds.DecayMs[0], Max: model.DefaultParamBounds.DecayMs[1]},
	HarmonicGain:  Range{Min: model.DefaultParamBounds.HarmonicGain[0], Max: model.DefaultParamBounds.HarmonicGain[1]},
}

// ParamCodec encodes BarParams into a flat optimization vector.
type ParamCodec struct {
	harmonicCount    int
	chebyshevEnabled bool
	bounds           ParamBounds
}

// NewParamCodec builds a codec from a validated parameter template.
func NewParamCodec(params *model.BarParams) (*ParamCodec, error) {
	return NewParamCodecWithBounds(params, DefaultParamBounds)
}

// NewParamCodecWithBounds builds a codec using explicit model-space bounds.
func NewParamCodecWithBounds(params *model.BarParams, bounds ParamBounds) (*ParamCodec, error) {
	if err := model.ValidateBarParams(params); err != nil {
		return nil, err
	}

	if err := bounds.Validate(); err != nil {
		return nil, err
	}

	bounds = bounds.expandToInclude(params)

	return &ParamCodec{
		harmonicCount:    len(params.Chebyshev.HarmonicGains),
		chebyshevEnabled: params.Chebyshev.Enabled,
		bounds:           bounds,
	}, nil
}

// Validate checks that the bounds are well-formed.
func (b ParamBounds) Validate() error {
	ranges := map[string]Range{
		"input_mix":      b.InputMix,
		"filter_freq":    b.FilterFreq,
		"base_frequency": b.BaseFrequency,
		"amplitude":      b.Amplitude,
		"frequency_mult": b.FrequencyMult,
		"decay_ms":       b.DecayMs,
		"harmonic_gain":  b.HarmonicGain,
	}
	for name, valueRange := range ranges {
		if math.IsNaN(valueRange.Min) || math.IsNaN(valueRange.Max) || math.IsInf(valueRange.Min, 0) || math.IsInf(valueRange.Max, 0) {
			return fmt.Errorf("%s bounds must be finite", name)
		}

		if valueRange.Min > valueRange.Max {
			return fmt.Errorf("%s bounds invalid: min %g > max %g", name, valueRange.Min, valueRange.Max)
		}

		if valueRange.Min <= 0 && (name == "filter_freq" || name == "base_frequency" || name == "frequency_mult") {
			return fmt.Errorf("%s bounds must be > 0 for log encoding", name)
		}
	}

	return nil
}

func (b ParamBounds) expandToInclude(params *model.BarParams) ParamBounds {
	b.InputMix = expandRange(b.InputMix, params.InputMix)
	b.FilterFreq = expandRange(b.FilterFreq, params.FilterFrequency)

	b.BaseFrequency = expandRange(b.BaseFrequency, params.BaseFrequency)
	for _, mode := range params.Modes {
		b.Amplitude = expandRange(b.Amplitude, mode.Amplitude)

		b.DecayMs = expandRange(b.DecayMs, mode.DecayMs)
		if params.BaseFrequency > 0 {
			b.FrequencyMult = expandRange(b.FrequencyMult, mode.Frequency/params.BaseFrequency)
		}
	}

	for _, gain := range params.Chebyshev.HarmonicGains {
		b.HarmonicGain = expandRange(b.HarmonicGain, gain)
	}

	return b
}

// Dimension returns the encoded vector dimensionality.
func (c *ParamCodec) Dimension() int {
	return fixedParameterCount + c.harmonicCount
}

// EncodedBounds returns the bounds for encoded vectors.
func (c *ParamCodec) EncodedBounds() Bounds {
	ranges := make([]Range, 0, c.Dimension())

	ranges = append(ranges,
		c.bounds.InputMix,
		logRange(c.bounds.FilterFreq),
		logRange(c.bounds.BaseFrequency),
	)
	for range model.NumModes {
		ranges = append(ranges,
			c.bounds.Amplitude,
			logRange(c.bounds.FrequencyMult),
			c.bounds.DecayMs,
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

	if len(params.Chebyshev.HarmonicGains) != c.harmonicCount {
		return nil, fmt.Errorf("expected %d harmonic gains, got %d", c.harmonicCount, len(params.Chebyshev.HarmonicGains))
	}

	encoded := make([]float64, 0, c.Dimension())

	encoded = append(encoded,
		params.InputMix,
		math.Log10(params.FilterFrequency),
		math.Log10(params.BaseFrequency),
	)
	for _, mode := range params.Modes {
		if mode.Frequency <= 0 || params.BaseFrequency <= 0 {
			return nil, fmt.Errorf("mode frequency and base frequency must be > 0")
		}

		encoded = append(encoded,
			mode.Amplitude,
			math.Log10(mode.Frequency/params.BaseFrequency),
			mode.DecayMs,
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

	baseFrequency := math.Pow(10, bounded[2])
	params := &model.BarParams{
		InputMix:        bounded[0],
		FilterFrequency: math.Pow(10, bounded[1]),
		BaseFrequency:   baseFrequency,
		Chebyshev: model.ChebyshevParams{
			Enabled:       c.chebyshevEnabled,
			HarmonicGains: make([]float64, c.harmonicCount),
		},
	}

	index := 3
	for i := range model.NumModes {
		params.Modes[i] = model.ModeParams{
			Amplitude: bounded[index],
			Frequency: baseFrequency * math.Pow(10, bounded[index+1]),
			DecayMs:   bounded[index+2],
		}
		index += 3
	}

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
