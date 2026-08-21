package model

import (
	"fmt"
	"math"

	"github.com/cwbudde/algo-dsp/dsp/filter/biquad"
	"github.com/cwbudde/algo-dsp/dsp/filter/design/pass"
	"github.com/cwbudde/glockenspiel/internal/oscbank"
)

const velocityScale = 1.0 / 128.0

// Bar integrates excitation shaping and modal resonance.
type Bar struct {
	bank *oscbank.Bank

	// lowpass is held by value, not by pointer: reconfiguring a bar must not
	// allocate, and biquad.Section exposes its Coefficients as an embedded
	// field, so a retune is a plain assignment onto the existing section.
	lowpass biquad.Section

	params     BarParams
	sampleRate int

	excitationBuf []float32
	filteredBuf   []float32
	distortedBuf  []float32
	outputBuf     []float32
	filterBlock   []float64

	// chebyGains carries the shaper gains in float32, the precision the
	// waveshaper actually runs at, so the audio path never converts per sample.
	chebyGains []float32

	oscillators []oscbank.Oscillator
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
		bank:       oscbank.New(float64(sampleRate)),
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
	b.bank.SetSampleRate(float64(sampleRate))
	b.setLowpass(b.params.FilterFrequency, float64(sampleRate))

	return nil
}

// Reset clears filter and oscillator state.
func (b *Bar) Reset() {
	b.bank.Reset()
	b.lowpass.Reset()
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
	shaping := b.params.Chebyshev.Enabled && len(b.params.Chebyshev.HarmonicGains) > 0

	switch {
	case shaping && b.params.Chebyshev.ResolvedStage() == ChebyshevStageExcitation:
		processChebyshevBlock(b.filteredBuf[:sampleCount], b.distortedBuf[:sampleCount], b.chebyGains)
		b.bank.ProcessBlock(b.distortedBuf[:sampleCount], out)
	case shaping:
		b.bank.ProcessBlock(b.filteredBuf[:sampleCount], b.distortedBuf[:sampleCount])
		processChebyshevBlock(b.distortedBuf[:sampleCount], out, b.chebyGains)
	default:
		b.bank.ProcessBlock(b.filteredBuf[:sampleCount], out)
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

	params.CopyInto(&b.params)
	b.setLowpass(b.params.FilterFrequency, float64(b.sampleRate))

	// Everything below reads b.params rather than params, so the bar only ever
	// keeps references into memory it owns.
	gains := b.params.Chebyshev.HarmonicGains
	if cap(b.chebyGains) >= len(gains) {
		b.chebyGains = b.chebyGains[:len(gains)]
	} else {
		b.chebyGains = make([]float32, len(gains))
	}

	for i, gain := range gains {
		b.chebyGains[i] = float32(gain)
	}

	if cap(b.oscillators) >= len(b.params.Modes) {
		b.oscillators = b.oscillators[:len(b.params.Modes)]
	} else {
		b.oscillators = make([]oscbank.Oscillator, len(b.params.Modes))
	}

	for i, mode := range b.params.Modes {
		b.oscillators[i] = oscbank.Oscillator{
			Amplitude: mode.Amplitude,
			Frequency: mode.Frequency,
			DecayMs:   mode.DecayMs,
			Harmonics: mode.Harmonics,
		}
	}

	return b.bank.SetOscillators(b.oscillators)
}

// NumModes returns how many modes this bar currently renders.
func (b *Bar) NumModes() int {
	return b.bank.NumOscillators()
}

// NumHarmonics returns the largest partial count any of this bar's modes carries.
func (b *Bar) NumHarmonics() int {
	return b.bank.NumHarmonics()
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

// setLowpass redesigns the excitation lowpass in place.
//
// Only the coefficients are replaced; the delay line is deliberately left
// alone. A parameter change is not a discontinuity in the signal, so zeroing
// the state mid-note would put a click into the output where the old and new
// responses ought to cross-fade through the filter's own memory. That holds for
// a bar being retuned for a fresh note too: there the caller wants a clean
// slate and asks for it explicitly via Reset, which is cheaper and clearer than
// having every parameter write silently imply one.
func (b *Bar) setLowpass(freq, sampleRate float64) {
	b.lowpass.Coefficients = lowpassCoefficients(freq, sampleRate)
}

func lowpassCoefficients(freq, sampleRate float64) biquad.Coefficients {
	nyquistLimit := 0.499 * sampleRate

	cutoff := freq
	if cutoff >= nyquistLimit {
		cutoff = nyquistLimit
	}

	if cutoff <= 0 {
		cutoff = 1000
	}

	return pass.LowpassRBJ(cutoff, 1/math.Sqrt2, sampleRate)
}

func clearFloat32(buf []float32) {
	for i := range buf {
		buf[i] = 0
	}
}
