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
	filterBlock   []float64
}

// NewBar creates a new bar model instance.
func NewBar(params *BarParams, sampleRate int) (*Bar, error) {
	if sampleRate <= 0 {
		return nil, fmt.Errorf("sample rate must be positive: %d", sampleRate)
	}
	if err := ValidateBarParams(params); err != nil {
		return nil, err
	}

	b := &Bar{
		oscillator: NewQuadDecayOscillator(float64(sampleRate)),
		sampleRate: sampleRate,
	}
	if err := b.UpdateParams(params); err != nil {
		return nil, err
	}

	return b, nil
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
	n := len(excitation)
	if n == 0 {
		return nil
	}
	b.ensureBuffers(n)

	for i := 0; i < n; i++ {
		b.filterBlock[i] = float64(excitation[i])
	}
	b.lowpass.ProcessBlock(b.filterBlock[:n])
	for i := 0; i < n; i++ {
		b.filteredBuf[i] = float32(b.filterBlock[i])
	}

	if b.params.Chebyshev.Enabled && len(b.params.Chebyshev.HarmonicGains) > 0 {
		for i := 0; i < n; i++ {
			b.distortedBuf[i] = float32(applyChebyshev(float64(b.filteredBuf[i]), b.params.Chebyshev.HarmonicGains))
		}
	} else {
		copy(b.distortedBuf[:n], b.filteredBuf[:n])
	}

	out := make([]float32, n)
	b.oscillator.ProcessBlock32(b.distortedBuf[:n], out)

	if b.params.InputMix != 0 {
		dryMix := float32(b.params.InputMix)
		for i := 0; i < n; i++ {
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

	for i, mode := range params.Modes {
		b.oscillator.SetAmplitude(i, mode.Amplitude)
		b.oscillator.SetFrequency(i, mode.Frequency)
		b.oscillator.SetDecay(i, mode.DecayMs)
	}

	return nil
}

func (b *Bar) ensureBuffers(numSamples int) {
	if len(b.excitationBuf) < numSamples {
		b.excitationBuf = make([]float32, numSamples)
		b.filteredBuf = make([]float32, numSamples)
		b.distortedBuf = make([]float32, numSamples)
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
	x := clamp(input, -1, 1)

	t0 := 1.0
	t1 := x
	out := gains[0] * t1

	for i := 1; i < len(gains); i++ {
		t2 := 2*x*t1 - t0
		out += gains[i] * t2
		t0, t1 = t1, t2
	}

	return out
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
