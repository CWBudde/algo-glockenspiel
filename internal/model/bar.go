package model

import (
	"fmt"
	"math"

	"github.com/cwbudde/algo-dsp/dsp/filter/biquad"
	"github.com/cwbudde/algo-dsp/dsp/filter/design/pass"
)

const velocityScale = 1.0 / 128.0

// Bar integrates excitation shaping and modal resonance.
type Bar struct {
	oscillator *QuadDecayOscillator
	lowpass    *biquad.Section

	params     BarParams
	sampleRate int

	excitationBuf []float32
	filteredBuf   []float32
	distortedBuf  []float32
	outputBuf     []float32
	filterBlock   []float64
	chebyGains4   [4]float32
	hasCheby4     bool
}

// NewBar creates a new bar model instance.
func NewBar(params *BarParams, sampleRate int) (*Bar, error) {
	if sampleRate <= 0 {
		return nil, fmt.Errorf("sample rate must be positive: %d", sampleRate)
	}

	if err := ValidateBarParams(params); err != nil {
		return nil, err
	}

	bar := &Bar{
		oscillator: NewQuadDecayOscillator(float64(sampleRate)),
		sampleRate: sampleRate,
	}
	if err := bar.UpdateParams(params); err != nil {
		return nil, err
	}

	return bar, nil
}

// SetSampleRate updates sample rate and recomputes derived coefficients.
func (b *Bar) SetSampleRate(sampleRate int) error {
	if sampleRate <= 0 {
		return fmt.Errorf("sample rate must be positive: %d", sampleRate)
	}

	b.sampleRate = sampleRate
	b.oscillator.SetSampleRate(float64(sampleRate))
	b.lowpass = newLowpassSection(b.params.FilterFrequency, float64(sampleRate))

	return nil
}

// Reset clears filter and oscillator state.
func (b *Bar) Reset() {
	b.oscillator.Reset()

	if b.lowpass != nil {
		b.lowpass.Reset()
	}
}

// Synthesize renders numSamples from a single impulse-like strike.
func (b *Bar) Synthesize(velocity int, numSamples int) []float32 {
	if numSamples <= 0 {
		return nil
	}

	b.ensureBuffers(numSamples)
	clearFloat32(b.excitationBuf[:numSamples])

	if velocity > 0 {
		b.excitationBuf[0] = float32(float64(velocity) * velocityScale)
	}

	return b.ProcessExcitation(b.excitationBuf[:numSamples])
}

// ProcessExcitation runs an externally provided excitation through the chain.
func (b *Bar) ProcessExcitation(excitation []float32) []float32 {
	sampleCount := len(excitation)
	if sampleCount == 0 {
		return nil
	}

	b.ensureBuffers(sampleCount)

	for i := 0; i < sampleCount; i++ {
		b.filterBlock[i] = float64(excitation[i])
	}

	b.lowpass.ProcessBlock(b.filterBlock[:sampleCount])

	for i := 0; i < sampleCount; i++ {
		b.filteredBuf[i] = float32(b.filterBlock[i])
	}

	out := b.outputBuf[:sampleCount]

	if b.params.Chebyshev.Enabled && len(b.params.Chebyshev.HarmonicGains) > 0 {
		processChebyshevBlock(b.filteredBuf[:sampleCount], b.distortedBuf[:sampleCount], b.params.Chebyshev.HarmonicGains, &b.chebyGains4, b.hasCheby4)
		b.oscillator.ProcessBlock32(b.distortedBuf[:sampleCount], out)
	} else {
		b.oscillator.ProcessBlock32(b.filteredBuf[:sampleCount], out)
	}

	if b.params.InputMix != 0 {
		dryMix := float32(b.params.InputMix)
		for i := 0; i < sampleCount; i++ {
			out[i] += dryMix * b.filteredBuf[i]
		}
	}

	return out
}

// UpdateParams updates all bar processing parameters.
func (b *Bar) UpdateParams(params *BarParams) error {
	if err := ValidateBarParams(params); err != nil {
		return err
	}

	b.params = *params
	b.lowpass = newLowpassSection(params.FilterFrequency, float64(b.sampleRate))
	b.hasCheby4 = len(params.Chebyshev.HarmonicGains) == 4
	if b.hasCheby4 {
		for i := range b.chebyGains4 {
			b.chebyGains4[i] = float32(params.Chebyshev.HarmonicGains[i])
		}
	}

	for i, mode := range params.Modes {
		b.oscillator.SetMode(i, mode.Amplitude, mode.Frequency, mode.DecayMs)
	}

	return nil
}

func (b *Bar) ensureBuffers(numSamples int) {
	if len(b.excitationBuf) < numSamples {
		b.excitationBuf = make([]float32, numSamples)
		b.filteredBuf = make([]float32, numSamples)
		b.distortedBuf = make([]float32, numSamples)
		b.outputBuf = make([]float32, numSamples)
		b.filterBlock = make([]float64, numSamples)
	}
}

func newLowpassSection(freq, sampleRate float64) *biquad.Section {
	nyquistLimit := 0.499 * sampleRate

	cutoff := freq
	if cutoff >= nyquistLimit {
		cutoff = nyquistLimit
	}

	if cutoff <= 0 {
		cutoff = 1000
	}

	coeff := pass.LowpassRBJ(cutoff, 1/math.Sqrt2, sampleRate)

	return biquad.NewSection(coeff)
}

func applyChebyshev(input float64, gains []float64) float64 {
	if len(gains) == 0 {
		return input
	}

	clampedInput := clamp(input, -1, 1)

	prevPrevTerm := 1.0
	prevTerm := clampedInput
	out := gains[0] * prevTerm

	for i := 1; i < len(gains); i++ {
		nextTerm := 2*clampedInput*prevTerm - prevPrevTerm
		out += gains[i] * nextTerm
		prevPrevTerm, prevTerm = prevTerm, nextTerm
	}

	return out
}

func processChebyshevBlock(input, output []float32, gains []float64, gains4 *[4]float32, has4 bool) {
	if has4 && processChebyshevBlockAVX2(input, output, gains4) {
		return
	}

	for i := range input {
		output[i] = float32(applyChebyshev(float64(input[i]), gains))
	}
}

func clearFloat32(buf []float32) {
	for i := range buf {
		buf[i] = 0
	}
}

func clamp(value, low, high float64) float64 {
	if value < low {
		return low
	}

	if value > high {
		return high
	}

	return value
}
