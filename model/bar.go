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
//
// The chain is three stages, and only the middle one is the oscillator bank:
//
//	excitation -> lowpass -> [shaper] -> bank -> [shaper] -> + dry mix
//
// Everything either side of the bank is per voice and stays that way. The
// lowpass carries a float64 delay line of its own and the shaper is a
// per-sample polynomial, so neither has anything to gain from being packed
// across voices; the bank is the only stage whose cost is a rotor recursion
// that vectorises across notes. StartBankInput and FinishBankOutput expose that
// split so a polyphonic engine can drive one voice-major bank for many voices
// while each voice keeps its own filter and shaper. This function is the
// single-voice composition of the same three stages and is what the offline
// render path uses.
func (b *Bar) ProcessExcitation(excitation []float32) []float32 {
	sampleCount := len(excitation)
	if sampleCount == 0 {
		return nil
	}

	b.ensureBuffers(sampleCount)

	in := b.bankInput(excitation)
	out := b.outputBuf[:sampleCount]

	if b.shapingAt(ChebyshevStageOutput) {
		b.bank.ProcessBlock(in, b.distortedBuf[:sampleCount])

		return b.FinishBankOutput(b.distortedBuf[:sampleCount], out)
	}

	b.bank.ProcessBlock(in, out)

	return b.FinishBankOutput(out, out)
}

// StartBankInput builds a strike's excitation and runs the pre-bank half of the
// chain over it, returning the signal that should be fed to an oscillator bank.
//
// The returned slice aliases one of the bar's working buffers and stays valid
// until the next call on this bar, which is what lets a caller gather it into
// an interleaved buffer without copying it twice.
func (b *Bar) StartBankInput(velocity, numSamples int) []float32 {
	if numSamples <= 0 {
		return nil
	}

	b.ensureBuffers(numSamples)
	clearFloat32(b.excitationBuf[:numSamples])

	if velocity > 0 {
		b.excitationBuf[0] = float32(float64(velocity) * velocityScale)
	}

	return b.bankInput(b.excitationBuf[:numSamples])
}

// FinishBankOutput runs the post-bank half of the chain over a bank's output
// and writes the result into dst, which may alias bankOut: every stage after
// the bank is elementwise, so finishing a block in place is the same
// computation as finishing it into a separate buffer. The realtime engine
// relies on that to deinterleave a lane straight into the buffer it will mix
// from, rather than through a scratch buffer and a copy.
//
// It reads the filtered excitation the matching StartBankInput or
// ProcessExcitation left behind, so the two have to be called as a pair over
// the same block of the same bar.
func (b *Bar) FinishBankOutput(bankOut, dst []float32) []float32 {
	sampleCount := len(bankOut)
	if sampleCount == 0 {
		return nil
	}

	out := dst[:sampleCount]

	switch {
	case b.shapingAt(ChebyshevStageOutput):
		processChebyshevBlock(bankOut, out, b.chebyGains)
	case &out[0] != &bankOut[0]:
		copy(out, bankOut)
	}

	if b.params.InputMix != 0 {
		dryMix := float32(b.params.InputMix)
		for i := 0; i < sampleCount; i++ {
			out[i] += dryMix * b.filteredBuf[i]
		}
	}

	return out
}

// BankOscillators returns the rotor configuration this bar renders, aliasing
// the bar's own storage. Callers must treat it as read-only; it exists so a
// voice-major bank can be pointed at the same oscillators the bar was retuned
// with, without copying them through a fresh slice on the audio thread.
func (b *Bar) BankOscillators() []oscbank.Oscillator {
	return b.oscillators
}

// bankInput runs the lowpass and, when the shaper sits in front of the bank,
// the shaper. It leaves the filtered excitation in filteredBuf, which the dry
// mix in FinishBankOutput reads afterwards.
func (b *Bar) bankInput(excitation []float32) []float32 {
	sampleCount := len(excitation)

	for i := 0; i < sampleCount; i++ {
		b.filterBlock[i] = float64(excitation[i])
	}

	b.lowpass.ProcessBlock(b.filterBlock[:sampleCount])

	for i := 0; i < sampleCount; i++ {
		b.filteredBuf[i] = float32(b.filterBlock[i])
	}

	if b.shapingAt(ChebyshevStageExcitation) {
		processChebyshevBlock(b.filteredBuf[:sampleCount], b.distortedBuf[:sampleCount], b.chebyGains)

		return b.distortedBuf[:sampleCount]
	}

	return b.filteredBuf[:sampleCount]
}

// shapingAt reports whether the Chebyshev shaper is enabled and sits at the
// given stage of the chain.
func (b *Bar) shapingAt(stage ChebyshevStage) bool {
	if !b.params.Chebyshev.Enabled || len(b.params.Chebyshev.HarmonicGains) == 0 {
		return false
	}

	return b.params.Chebyshev.ResolvedStage() == stage
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
