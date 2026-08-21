package model

import (
	"errors"
	"fmt"
	"math"
)

const (
	// MaxModes bounds a preset's mode count so a malformed file cannot ask for
	// an unbounded allocation.
	MaxModes = 512

	// MaxHarmonics bounds the partials one mode may carry.
	MaxHarmonics = 64

	InputMixMin = 0.0
	InputMixMax = 2.0

	FilterFrequencyMinHz = 20.0
	FilterFrequencyMaxHz = 20000.0

	AmplitudeMin = -2.0
	AmplitudeMax = 2.0

	FrequencyMinHz = 0.01
	FrequencyMaxHz = 50000.0

	DecayMsMin = 0.1
	DecayMsMax = 500.0

	HarmonicGainMin = 0.0
	HarmonicGainMax = 2.0
)

// ParamBounds contains optimization parameter bounds.
type ParamBounds struct {
	InputMix      [2]float64 // [0.0, 2.0]
	FilterFreq    [2]float64 // [20.0, 20000.0] Hz, log scale
	Amplitude     [2]float64 // [-2.0, 2.0]
	FrequencyMult [2]float64 // [0.5, 10.0] * base_frequency
	DecayMs       [2]float64 // [0.1, 500.0] milliseconds
	HarmonicGain  [2]float64 // [0.0, 2.0] per harmonic
}

// DefaultParamBounds are the bounds used for optimization.
var DefaultParamBounds = ParamBounds{
	InputMix:      [2]float64{InputMixMin, InputMixMax},
	FilterFreq:    [2]float64{FilterFrequencyMinHz, FilterFrequencyMaxHz},
	Amplitude:     [2]float64{AmplitudeMin, AmplitudeMax},
	FrequencyMult: [2]float64{0.5, 10.0},
	DecayMs:       [2]float64{DecayMsMin, DecayMsMax},
	HarmonicGain:  [2]float64{HarmonicGainMin, HarmonicGainMax},
}

// ModeParams describes one resonant mode.
//
// Harmonics are optional integer-multiple partials computed on top of this
// mode's oscillator: entry k adds a rotor at (k+1) * Frequency sharing DecayMs,
// with its gain applied on top of Amplitude. An empty slice means the mode is a
// single oscillator at its fundamental, which is what every v1 preset describes.
type ModeParams struct {
	Amplitude float64   `json:"amplitude"`
	Frequency float64   `json:"frequency"`
	DecayMs   float64   `json:"decay_ms"`
	Harmonics []float64 `json:"harmonics,omitempty"`
}

// Clone returns a deep copy of the mode.
func (m ModeParams) Clone() ModeParams {
	if len(m.Harmonics) > 0 {
		m.Harmonics = append([]float64(nil), m.Harmonics...)
	}

	return m
}

// CopyInto deep-copies the mode into dst, reusing dst's harmonics slice when
// its capacity allows. See [BarParams.CopyInto] for why this exists.
func (m *ModeParams) CopyInto(dst *ModeParams) {
	if m == dst {
		return
	}

	dst.Amplitude = m.Amplitude
	dst.Frequency = m.Frequency
	dst.DecayMs = m.DecayMs
	dst.Harmonics = copyFloat64s(dst.Harmonics, m.Harmonics)
}

// ChebyshevStage selects where the Chebyshev waveshaper sits in the chain.
type ChebyshevStage string

const (
	// ChebyshevStageExcitation shapes the filtered excitation before it reaches
	// the oscillators. This is what v1 presets describe and stays the default,
	// so their rendering is unchanged.
	ChebyshevStageExcitation ChebyshevStage = "excitation"

	// ChebyshevStageOutput shapes the oscillator bank's output instead, which
	// is the post-oscillator placement the shaper was always meant to have.
	ChebyshevStageOutput ChebyshevStage = "output"
)

// ChebyshevParams controls harmonic excitation.
type ChebyshevParams struct {
	Enabled       bool           `json:"enabled"`
	Stage         ChebyshevStage `json:"stage,omitempty"`
	HarmonicGains []float64      `json:"harmonic_gains"`
}

// ResolvedStage returns the shaper stage, defaulting to the v1 placement.
func (c ChebyshevParams) ResolvedStage() ChebyshevStage {
	if c.Stage == ChebyshevStageOutput {
		return ChebyshevStageOutput
	}

	return ChebyshevStageExcitation
}

// Clone returns a deep copy of the Chebyshev parameters.
func (c ChebyshevParams) Clone() ChebyshevParams {
	if len(c.HarmonicGains) > 0 {
		c.HarmonicGains = append([]float64(nil), c.HarmonicGains...)
	}

	return c
}

// CopyInto deep-copies the Chebyshev parameters into dst, reusing dst's gain
// slice when its capacity allows.
func (c *ChebyshevParams) CopyInto(dst *ChebyshevParams) {
	if c == dst {
		return
	}

	dst.Enabled = c.Enabled
	dst.Stage = c.Stage
	dst.HarmonicGains = copyFloat64s(dst.HarmonicGains, c.HarmonicGains)
}

// BarParams are the top-level model parameters for one bar.
//
// Modes is a slice: the mode count is runtime configuration. Copy BarParams
// with Clone rather than assignment, or the copy shares this slice.
type BarParams struct {
	InputMix        float64         `json:"input_mix"`
	FilterFrequency float64         `json:"filter_frequency"`
	BaseFrequency   float64         `json:"base_frequency"`
	Modes           []ModeParams    `json:"modes"`
	Chebyshev       ChebyshevParams `json:"chebyshev"`
}

// Clone returns a deep copy, safe to mutate independently of the original.
//
// Clone is the convenient form: it starts from an empty destination, so every
// non-empty slice it copies is freshly allocated. Code on the audio path that
// already owns a destination should use [BarParams.CopyInto] instead, which
// reuses the buffers that destination already holds.
func (p BarParams) Clone() BarParams {
	var dst BarParams
	p.CopyInto(&dst)

	return dst
}

// CopyInto deep-copies p into dst, reusing dst's slices wherever their capacity
// already suffices and only allocating when it does not.
//
// This exists so that a Bar which is retuned rather than rebuilt — the pooled
// voice case, where a note-on must not allocate — can absorb a new parameter
// set without touching the allocator. A plain Clone cannot serve that role: it
// allocates the Modes slice, every non-empty Harmonics slice and the Chebyshev
// gains on every single call, however little the shape actually changed.
//
// The copy is deep, so dst never aliases p's backing arrays and the two can be
// mutated independently, exactly as with Clone. That holds even when dst starts
// out already sharing an array with p, as a shallow struct copy such as
// dst := *p leaves it: reusing that array would turn the copy into a no-op and
// quietly keep the two views aliased, so a shared array is replaced rather than
// written into. See [sharesBacking] for what that check does and does not see.
// Copying a value into itself is a no-op.
//
// Nil-ness is preserved rather than normalized to an empty slice, because
// BarParams round-trips through JSON and a nil slice and an empty one do not
// encode alike. That costs nothing: a nil source needs no buffer to copy into
// either.
func (p *BarParams) CopyInto(dst *BarParams) {
	// Copying a value into itself is a no-op, and it has to be spelled out
	// rather than left to fall through: the overlap handling below would see
	// dst's arrays aliasing p's, replace them with fresh ones, and — dst being
	// p — leave p pointing at those empty arrays before anything was read out
	// of the originals.
	if p == dst {
		return
	}

	dst.InputMix = p.InputMix
	dst.FilterFrequency = p.FilterFrequency
	dst.BaseFrequency = p.BaseFrequency

	if p.Modes == nil {
		dst.Modes = nil
	} else {
		if dst.Modes != nil && cap(dst.Modes) >= len(p.Modes) && !sharesBacking(dst.Modes, p.Modes) {
			dst.Modes = dst.Modes[:len(p.Modes)]
		} else {
			dst.Modes = make([]ModeParams, len(p.Modes))
		}

		for i := range p.Modes {
			p.Modes[i].CopyInto(&dst.Modes[i])
		}
	}

	p.Chebyshev.CopyInto(&dst.Chebyshev)
}

// copyFloat64s copies src into dst, reusing dst's backing array when it is
// large enough. A nil src yields a nil result, so callers that care about the
// nil/empty distinction keep it.
func copyFloat64s(dst, src []float64) []float64 {
	if src == nil {
		return nil
	}

	// dst != nil matters for the empty-but-not-nil source: reslicing a nil dst
	// to length zero would hand back a nil slice and silently turn [] into null.
	// sharesBacking matters for a dst that already aliases src, where reusing
	// the array would leave the two views pointing at the same elements.
	if dst != nil && cap(dst) >= len(src) && !sharesBacking(dst, src) {
		dst = dst[:len(src)]
	} else {
		dst = make([]float64, len(src))
	}

	copy(dst, src)

	return dst
}

// sharesBacking reports whether dst and src are views onto the same backing
// array. It is the guard that keeps a copy-into from degenerating into an
// alias when the destination was seeded from the source, which a shallow struct
// copy does for free: after dst := *p, dst.Modes and p.Modes are the same array.
//
// Comparing the first element of each slice expanded to its full capacity
// catches every alias a shallow copy or a leading reslice can produce. A slice
// deliberately offset into the middle of the other's array is not detected;
// ordering two pointers to decide that needs unsafe, and no caller in this
// codebase constructs one.
func sharesBacking[T any](dst, src []T) bool {
	if len(dst) == 0 || len(src) == 0 {
		return false
	}

	return &dst[:cap(dst)][0] == &src[:cap(src)][0]
}

// Validate checks whether BarParams are well-formed and in supported ranges.
func (p *BarParams) Validate() error {
	return ValidateBarParams(p)
}

// ValidateBarParams validates bar model parameters.
func ValidateBarParams(params *BarParams) error {
	if params == nil {
		return errors.New("bar params cannot be nil")
	}

	if err := validateFiniteRange("input_mix", params.InputMix, InputMixMin, InputMixMax); err != nil {
		return err
	}

	if err := validateFiniteRange("filter_frequency", params.FilterFrequency, FilterFrequencyMinHz, FilterFrequencyMaxHz); err != nil {
		return err
	}

	if err := validateFiniteRange("base_frequency", params.BaseFrequency, FrequencyMinHz, FrequencyMaxHz); err != nil {
		return err
	}

	if len(params.Modes) > MaxModes {
		return fmt.Errorf("modes: %d exceeds the maximum of %d", len(params.Modes), MaxModes)
	}

	for modeIndex, mode := range params.Modes {
		if len(mode.Harmonics) > MaxHarmonics {
			return fmt.Errorf("modes[%d].harmonics: %d exceeds the maximum of %d", modeIndex, len(mode.Harmonics), MaxHarmonics)
		}

		for harmonicIndex, gain := range mode.Harmonics {
			if !isFiniteInRange(gain, HarmonicGainMin, HarmonicGainMax) {
				return fmt.Errorf("modes[%d].harmonics[%d] out of range [%g, %g]: %g",
					modeIndex, harmonicIndex, HarmonicGainMin, HarmonicGainMax, gain)
			}
		}

		if !isFiniteInRange(mode.Amplitude, AmplitudeMin, AmplitudeMax) {
			return rangeErrorf("modes[%d].amplitude", modeIndex, mode.Amplitude, AmplitudeMin, AmplitudeMax)
		}

		if !isFiniteInRange(mode.Frequency, FrequencyMinHz, FrequencyMaxHz) {
			return rangeErrorf("modes[%d].frequency", modeIndex, mode.Frequency, FrequencyMinHz, FrequencyMaxHz)
		}

		if !isFiniteInRange(mode.DecayMs, DecayMsMin, DecayMsMax) {
			return rangeErrorf("modes[%d].decay_ms", modeIndex, mode.DecayMs, DecayMsMin, DecayMsMax)
		}
	}

	if stage := params.Chebyshev.Stage; stage != "" && stage != ChebyshevStageExcitation && stage != ChebyshevStageOutput {
		return fmt.Errorf("chebyshev.stage must be %q or %q: %q", ChebyshevStageExcitation, ChebyshevStageOutput, stage)
	}

	for gainIndex, gain := range params.Chebyshev.HarmonicGains {
		if !isFiniteInRange(gain, HarmonicGainMin, HarmonicGainMax) {
			return rangeErrorf("chebyshev.harmonic_gains[%d]", gainIndex, gain, HarmonicGainMin, HarmonicGainMax)
		}
	}

	return nil
}

func isFiniteInRange(value, min, max float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= min && value <= max
}

func validateFiniteRange(field string, value, min, max float64) error {
	if !isFiniteInRange(value, min, max) {
		return rangeError(field, value, min, max)
	}

	return nil
}

func rangeError(field string, value, min, max float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s must be finite", field)
	}

	return fmt.Errorf("%s out of range [%g, %g]: %g", field, min, max, value)
}

func rangeErrorf(fieldFmt string, index int, value, min, max float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf(fieldFmt+" must be finite", index)
	}

	return fmt.Errorf(fieldFmt+" out of range [%g, %g]: %g", index, min, max, value)
}
