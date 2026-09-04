package model

import (
	"fmt"
	"math"
	"math/cmplx"

	"github.com/cwbudde/algo-dsp/dsp/filter/biquad"
	"github.com/cwbudde/algo-dsp/dsp/filter/design/pass"
	"github.com/cwbudde/algo-glockenspiel/internal/oscbank"
)

const velocityScale = 1.0 / 128.0

// Oscillator is the rotor configuration one mode contributes to the bank: an
// amplitude, a fundamental and a decay, plus the integer-multiple partials that
// ride on it. It is what [Bar.BankOscillators] hands out.
//
// It is an alias rather than a defined type on purpose. BankOscillators returns
// the bar's own storage without copying, so the bank on the other side has to
// see the same type; a defined type would put a conversion -- and therefore an
// allocation -- on the note-on path the accessor exists to keep clear.
type Oscillator = oscbank.Oscillator

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

	// dryMix is InputMix folded together with the output gain, in the
	// precision the mix loop runs at. See UpdateParams for why the gain is
	// carried in coefficients rather than applied to the finished buffer.
	dryMix float32

	oscillators []Oscillator
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
//
// dst has to be at least as long as bankOut. A short one is a caller bug rather
// than a runtime condition -- the engine sizes the slot buffer it passes from
// the same block length it sized the bank pass from -- so it panics with a
// named message instead of failing as a slice-bounds error a few frames deep.
func (b *Bar) FinishBankOutput(bankOut, dst []float32) []float32 {
	sampleCount := len(bankOut)
	if sampleCount == 0 {
		return nil
	}

	if len(dst) < sampleCount {
		panic("model: destination buffer too small")
	}

	out := dst[:sampleCount]

	switch {
	case b.shapingAt(ChebyshevStageOutput):
		processChebyshevBlock(bankOut, out, b.chebyGains)
	case &out[0] != &bankOut[0]:
		copy(out, bankOut)
	}

	if b.dryMix != 0 {
		dryMix := b.dryMix
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
func (b *Bar) BankOscillators() []Oscillator {
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

	bankGain, shaperGain := b.outputGainSplit()
	b.dryMix = float32(b.params.InputMix * decibelsToLinear(b.params.OutputGainDB))

	// Everything below reads b.params rather than params, so the bar only ever
	// keeps references into memory it owns.
	gains := b.params.Chebyshev.HarmonicGains
	if cap(b.chebyGains) >= len(gains) {
		b.chebyGains = b.chebyGains[:len(gains)]
	} else {
		b.chebyGains = make([]float32, len(gains))
	}

	for i, gain := range gains {
		b.chebyGains[i] = float32(gain * shaperGain)
	}

	if cap(b.oscillators) >= len(b.params.Modes) {
		b.oscillators = b.oscillators[:len(b.params.Modes)]
	} else {
		b.oscillators = make([]Oscillator, len(b.params.Modes))
	}

	for i, mode := range b.params.Modes {
		b.oscillators[i] = Oscillator{
			Amplitude: mode.Amplitude * bankGain,
			Frequency: mode.Frequency,
			DecayMs:   mode.DecayMs,
			Harmonics: mode.Harmonics,
		}
	}

	return b.bank.SetOscillators(b.oscillators)
}

// outputGainSplit decides which coefficients OutputGainDB is folded into, and
// returns it as a factor on the mode amplitudes and a factor on the shaper
// gains -- exactly one of which is the gain, the other being 1.
//
// The gain is a scalar on the finished signal, so the obvious implementation is
// a multiply over the output buffer. It is not what happens, because the bar
// does not need one: every stage that carries the signal already has a
// coefficient computed once per retune, and scaling one of those costs nothing
// per sample. The oscillator bank runs a rotor recursion that is linear in the
// amplitude it was built with, over hand-written AVX2 and NEON kernels; adding
// a pass over its output to apply a constant would be the only pass in the
// chain that exists purely to multiply.
//
// Which coefficient depends on where the shaper sits, because the shaper is the
// one nonlinearity in the chain:
//
//   - Shaper on the excitation, or disabled: everything from the bank onwards
//     is linear, so the gain folds into the mode amplitudes.
//   - Shaper on the output: scaling what goes *into* a polynomial is not
//     scaling what comes out, so the amplitudes are the wrong place. The gain
//     folds into the shaper's own gains instead, which is exact because the
//     shaper is a weighted sum of Chebyshev terms -- sum (G*g_k) T_k(x) is
//     G * sum g_k T_k(x) -- and because the input clamp that makes it
//     nonlinear acts on x, which the fold does not touch. The DC offset
//     chebyshevZeroOffset removes is linear in the same gains, so it scales
//     with the rest rather than being left behind as a step.
//
// The dry input_mix path is scaled separately in either case: it is added after
// the bank and reads the pre-shaper excitation, so it carries neither set of
// coefficients.
//
// Because the fold is exact, rendering at gain G is G times rendering at unity,
// which is what lets a fit scale a buffer it has already rendered instead of
// rendering it again.
func (b *Bar) outputGainSplit() (bankGain, shaperGain float64) {
	gain := decibelsToLinear(b.params.OutputGainDB)

	if b.shapingAt(ChebyshevStageOutput) {
		return 1, gain
	}

	return gain, 1
}

// decibelsToLinear converts a gain in dB to a linear factor, with zero mapping
// to exactly 1 rather than to the rounding of math.Pow(10, 0).
func decibelsToLinear(db float64) float64 {
	if db == 0 {
		return 1
	}

	return math.Pow(10, db/20)
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

// ExcitationResponse is the magnitude of the excitation lowpass at a frequency,
// for a bar with the given cutoff rendered at sampleRate. It is what scales the
// strike a mode at that frequency receives, so a fit that reasons about mode
// levels from the parameters alone -- rather than from a render -- has to
// multiply the amplitude by it. The shaper and the dry mix are not included.
func ExcitationResponse(filterFrequency, atHz, sampleRate float64) float64 {
	if sampleRate <= 0 || atHz < 0 {
		return 0
	}

	coefficients := lowpassCoefficients(filterFrequency, sampleRate)
	omega := 2 * math.Pi * atHz / sampleRate
	z1 := complex(math.Cos(omega), -math.Sin(omega))
	z2 := z1 * z1

	numerator := complex(coefficients.B0, 0) + complex(coefficients.B1, 0)*z1 + complex(coefficients.B2, 0)*z2
	denominator := complex(1, 0) + complex(coefficients.A1, 0)*z1 + complex(coefficients.A2, 0)*z2

	return cmplx.Abs(numerator / denominator)
}
