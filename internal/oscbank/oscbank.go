package oscbank

import (
	"fmt"
	"math"
)

const (
	// LaneWidth is the number of oscillator rotors held in one AoSoA block.
	// It is the AVX2 float32 vector width; the 4-lane NEON/SSE kernels consume
	// two half-blocks and a 16-lane AVX-512 kernel consumes two whole blocks,
	// which is why the rotor arrays are always rounded up to an even number of
	// blocks.
	LaneWidth = 8

	// accLanes is the width of the per-sample output accumulator. The kernels
	// fold a block's two 128-bit halves together before storing, because they
	// have issue slots to spare while the reduction pass does not.
	accLanes = LaneWidth / 2

	// blockSamples bounds the per-lane accumulator so it stays in L1 while the
	// bank walks the rotor blocks. 256 samples x 8 lanes x 4 bytes = 8 KiB.
	blockSamples = 256

	defaultSampleRate = 44100.0

	// minDecayMs matches internal/model: at or below this the rotor is muted
	// rather than producing a division by zero in the decay factor.
	minDecayMs = 1e-9
)

// Oscillator describes one decaying quadrature oscillator and its harmonics.
//
// Harmonics are integer-multiple rotors that share the oscillator's decay:
// entry k drives a rotor at (k+1) * Frequency with gain Harmonics[k] applied on
// top of Amplitude. An empty Harmonics slice means a single unity-gain partial
// at the fundamental, so a bank of plain oscillators needs no harmonic data.
type Oscillator struct {
	Amplitude float64   `json:"amplitude"`
	Frequency float64   `json:"frequency"`
	DecayMs   float64   `json:"decay_ms"`
	Harmonics []float64 `json:"harmonics,omitempty"`
}

// Clone returns a deep copy, so callers can mutate harmonics independently.
func (o Oscillator) Clone() Oscillator {
	if len(o.Harmonics) > 0 {
		o.Harmonics = append([]float64(nil), o.Harmonics...)
	}

	return o
}

// Bank is a bank of N oscillators with up to M harmonics each, laid out as
// N*M rotors in AoSoA blocks of LaneWidth.
//
// The recursion is the exact phase rotation used by the fixed four-mode model:
//
//	t  = im*cos + re*sin
//	re = re*cos - im*sin
//	im = amp*x + t
//
// with cos/sin already scaled by the per-sample decay factor. It is drift-free
// while the decay factor stays below 1; a sustained rotor (decay factor 1)
// would need magnitude renormalization, which this bank does not do.
type Bank struct {
	sampleRate float64

	oscillators []Oscillator

	numOsc    int
	numHarm   int
	numRotors int
	numBlocks int

	// AoSoA rotor arrays, len == numBlocks*LaneWidth. Padding lanes carry zero
	// coefficients and zero amplitude, so they contribute nothing.
	rotorArrays

	// acc is the partial output accumulator, [blockSamples][accLanes].
	acc []float32

	// scratchIn holds one chunk of excitation with a zero sample appended. The
	// packed kernel reads one sample ahead to keep the excitation term off the
	// recursion's critical path, so it needs that guard element.
	scratchIn []float32
}

// New returns an empty bank at the given sample rate.
func New(sampleRate float64) *Bank {
	if sampleRate <= 0 {
		sampleRate = defaultSampleRate
	}

	return &Bank{
		sampleRate: sampleRate,
		acc:        make([]float32, blockSamples*accLanes),
		scratchIn:  make([]float32, blockSamples+1),
	}
}

// NumOscillators returns the configured oscillator count N.
func (b *Bank) NumOscillators() int { return b.numOsc }

// NumHarmonics returns the configured harmonic count M, the largest number of
// partials any single oscillator carries.
func (b *Bank) NumHarmonics() int { return b.numHarm }

// NumRotors returns the number of active rotors, N*M for a rectangular bank.
func (b *Bank) NumRotors() int { return b.numRotors }

// SampleRate returns the current sample rate.
func (b *Bank) SampleRate() float64 { return b.sampleRate }

// Oscillators returns a copy of the current configuration.
func (b *Bank) Oscillators() []Oscillator {
	out := make([]Oscillator, len(b.oscillators))
	for i, osc := range b.oscillators {
		out[i] = osc.Clone()
	}

	return out
}

// SetOscillators reconfigures the bank. Oscillator and harmonic counts are
// plain runtime values: any N >= 0 and any per-oscillator harmonic count is
// accepted, and the rotor arrays are resized to match.
//
// Existing rotor state is discarded, because rotor k no longer denotes the same
// partial once the layout changes.
func (b *Bank) SetOscillators(oscillators []Oscillator) error {
	numHarm := 0

	for i, osc := range oscillators {
		if err := validateOscillator(i, osc); err != nil {
			return err
		}

		harmonics := len(osc.Harmonics)
		if harmonics == 0 {
			harmonics = 1
		}

		if harmonics > numHarm {
			numHarm = harmonics
		}
	}

	b.storeOscillators(oscillators)

	b.numOsc = len(oscillators)
	b.numHarm = numHarm
	b.numRotors = b.numOsc * b.numHarm

	// Round up to an even number of blocks so a 16-lane kernel can consume two
	// blocks at a time without a separate tail path.
	blocks := roundUpToEven((b.numRotors + LaneWidth - 1) / LaneWidth)

	b.numBlocks = blocks
	b.allocate(blocks * LaneWidth)
	b.calculateCoefficients()

	return nil
}

// storeOscillators takes a private deep copy of the configuration, reusing the
// slices already held wherever their capacity allows.
//
// Reconfiguring a bank whose shape has not changed must not allocate: it sits
// on the path a pooled voice takes when it is retuned for a new note, and the
// audio thread has no budget for the allocator. Copying the configuration with
// Clone, which is what this used to do, cost one allocation for the oscillator
// slice plus one per oscillator carrying harmonics, on every single call.
func (b *Bank) storeOscillators(oscillators []Oscillator) {
	b.oscillators = storeOscillators(b.oscillators, oscillators)
}

// storeOscillators copies src into dst, reusing dst's backing array and its
// per-oscillator harmonic slices wherever their capacity allows.
func storeOscillators(dst, src []Oscillator) []Oscillator {
	if dst != nil && cap(dst) >= len(src) {
		dst = dst[:len(src)]
	} else {
		dst = make([]Oscillator, len(src))
	}

	for i := range src {
		from := &src[i]
		to := &dst[i]

		to.Amplitude = from.Amplitude
		to.Frequency = from.Frequency
		to.DecayMs = from.DecayMs
		to.Harmonics = copyFloat64s(to.Harmonics, from.Harmonics)
	}

	return dst
}

// copyFloat64s copies src into dst, reusing dst's backing array when it is
// large enough. A nil src yields a nil result, so the distinction between an
// absent and an empty harmonics slice survives the copy.
func copyFloat64s(dst, src []float64) []float64 {
	if src == nil {
		return nil
	}

	// dst != nil matters for the empty-but-not-nil source: reslicing a nil dst
	// to length zero would hand back a nil slice and silently turn [] into null.
	if dst != nil && cap(dst) >= len(src) {
		dst = dst[:len(src)]
	} else {
		dst = make([]float64, len(src))
	}

	copy(dst, src)

	return dst
}

// rotorArrays is the state and coefficient storage both banks are built from.
//
// Bank indexes it by rotor and VoiceBank by rotor and voice, but the storage,
// the reuse-on-resize rule and the "padding lanes stay at zero forever"
// invariant are the same either way, so they share one implementation rather
// than two that have to be kept in step.
type rotorArrays struct {
	re       []float32
	im       []float32
	cosCoeff []float32
	sinCoeff []float32
	amp      []float32

	decayFactor []float64
}

// allocate sizes every array to size, reusing the backing arrays when their
// capacity allows and clearing the rotor state either way. Reconfiguring a bank
// whose shape has not changed must not allocate: it sits on the path a pooled
// voice takes when it is retuned, and the audio thread has no allocator budget.
func (a *rotorArrays) allocate(size int) {
	if cap(a.re) >= size {
		a.re = a.re[:size]
		a.im = a.im[:size]
		a.cosCoeff = a.cosCoeff[:size]
		a.sinCoeff = a.sinCoeff[:size]
		a.amp = a.amp[:size]
		a.decayFactor = a.decayFactor[:size]

		clear(a.re)
		clear(a.im)

		return
	}

	a.re = make([]float32, size)
	a.im = make([]float32, size)
	a.cosCoeff = make([]float32, size)
	a.sinCoeff = make([]float32, size)
	a.amp = make([]float32, size)
	a.decayFactor = make([]float64, size)
}

// clearCoefficients zeroes everything a coefficient pass is about to rewrite,
// so a rotor the new configuration does not reach is inert rather than stale.
func (a *rotorArrays) clearCoefficients() {
	clear(a.cosCoeff)
	clear(a.sinCoeff)
	clear(a.amp)
	clear(a.decayFactor)
}

// roundUpToEven rounds n up to the next even number. Both banks hand the
// kernels their working set in pairs -- block pairs for Bank, rotor pairs for
// VoiceBank -- so an odd count would need a tail path in every kernel.
func roundUpToEven(n int) int {
	if n%2 == 1 {
		return n + 1
	}

	return n
}

// SetSampleRate updates the sample rate and recomputes the rotor coefficients.
func (b *Bank) SetSampleRate(sampleRate float64) {
	if sampleRate <= 0 {
		return
	}

	b.sampleRate = sampleRate
	b.calculateCoefficients()
}

// Reset clears all rotor state.
func (b *Bank) Reset() {
	clear(b.re)
	clear(b.im)
}

// MaxDecayFactor returns the largest per-sample decay factor across all rotors.
func (b *Bank) MaxDecayFactor() float64 {
	maxVal := 0.0

	for i := 0; i < b.numRotors; i++ {
		if b.decayFactor[i] > maxVal {
			maxVal = b.decayFactor[i]
		}
	}

	return maxVal
}

// ProcessBlock renders the excitation through every rotor and writes the summed
// output. output must be at least as long as input.
func (b *Bank) ProcessBlock(input, output []float32) {
	if len(output) < len(input) {
		panic("oscbank: output buffer too small")
	}

	if b.numBlocks == 0 {
		clear(output[:len(input)])
		return
	}

	// The rotors are what decay into denormal state, so the scope sits here
	// rather than at some caller: every path into the recursion goes through
	// this function, and the save-set-restore is noise against a block.
	scope := FlushDenormals()
	defer scope.Restore()

	for start := 0; start < len(input); start += blockSamples {
		end := min(start+blockSamples, len(input))
		b.processChunk(input[start:end], output[start:end])
	}
}

func (b *Bank) processChunk(input, output []float32) {
	acc := b.acc[:len(input)*accLanes]

	copy(b.scratchIn, input)
	b.scratchIn[len(input)] = 0

	processRotorBlocks(b.re, b.im, b.cosCoeff, b.sinCoeff, b.amp, b.numBlocks, b.scratchIn[:len(input)], acc)
	reduceLanes(acc, output)
}

func (b *Bank) calculateCoefficients() {
	b.clearCoefficients()

	rotor := 0

	for _, osc := range b.oscillators {
		decayFactor, decaying := decayFactorFor(osc.DecayMs, b.sampleRate)

		for harmonic := 0; harmonic < b.numHarm; harmonic++ {
			coeff, active := rotorCoefficients(osc, harmonic, decayFactor, decaying, b.sampleRate)
			if active {
				b.decayFactor[rotor] = coeff.decay
				b.cosCoeff[rotor] = coeff.cos
				b.sinCoeff[rotor] = coeff.sin
				b.amp[rotor] = coeff.amp
			}

			rotor++
		}
	}
}

// rotorCoefficient is one rotor's packed coefficient set.
type rotorCoefficient struct {
	decay    float64
	cos, sin float32
	amp      float32
}

// rotorCoefficients derives the coefficients for harmonic k of one oscillator,
// given the oscillator's per-sample decay factor. active is false when the
// rotor is inert -- a muted decay or a harmonic the oscillator does not carry --
// and the caller must then leave the rotor at zero rather than write anything.
//
// The decay factor is passed in rather than derived here so both banks compute
// math.Exp once per oscillator, not once per harmonic.
func rotorCoefficients(osc Oscillator, harmonic int, decayFactor float64, decaying bool, sampleRate float64) (rotorCoefficient, bool) {
	gain, active := harmonicGain(osc, harmonic)
	if !decaying || !active {
		return rotorCoefficient{}, false
	}

	frequency := float64(harmonic+1) * osc.Frequency

	// A rotor at or above Nyquist is culled rather than rendered.
	//
	// It is tempting to treat one as harmless -- the model's frequency ceiling
	// says as much, calling a mode above Nyquist "a wasted oscillator rather
	// than an invalid one" -- but a resonator does not go quiet above Nyquist.
	// It produces the alias, at full amplitude, wherever that happens to land:
	// recorded-bar.json's 9791.5 Hz mode transposed to the keyboard's top key
	// is 93.15 kHz, which is a loud 4.95 kHz partial at 44.1 kHz and a loud
	// 2.85 kHz one at 48 kHz. That is the same preset sounding different on
	// two soundcards, which is worse than either result on its own.
	//
	// Culling here rather than in validation keeps the two questions apart, as
	// FrequencyMaxHz's own reasoning asks: whether a preset is *valid* stays
	// independent of the rate it is rendered at, and whether a rotor is
	// *audible* is decided at the rate it is actually rendered at.
	if frequency >= 0.5*sampleRate {
		return rotorCoefficient{}, false
	}

	phase := 2 * math.Pi * frequency / sampleRate
	sinVal, cosVal := math.Sincos(phase)

	return rotorCoefficient{
		decay: decayFactor,
		cos:   float32(decayFactor * cosVal),
		sin:   float32(decayFactor * sinVal),
		amp:   float32(osc.Amplitude * gain),
	}, true
}

func harmonicGain(osc Oscillator, harmonic int) (float64, bool) {
	if len(osc.Harmonics) == 0 {
		return 1, harmonic == 0
	}

	if harmonic >= len(osc.Harmonics) {
		return 0, false
	}

	return osc.Harmonics[harmonic], true
}

func decayFactorFor(decayMs, sampleRate float64) (float64, bool) {
	if decayMs <= minDecayMs {
		return 0, false
	}

	return math.Exp(-math.Ln2 / (0.001 * decayMs * sampleRate)), true
}

func validateOscillator(index int, osc Oscillator) error {
	if !isFinite(osc.Amplitude) {
		return fmt.Errorf("oscillators[%d].amplitude must be finite", index)
	}

	if !isFinite(osc.Frequency) {
		return fmt.Errorf("oscillators[%d].frequency must be finite", index)
	}

	if !isFinite(osc.DecayMs) {
		return fmt.Errorf("oscillators[%d].decay_ms must be finite", index)
	}

	if osc.DecayMs < 0 {
		return fmt.Errorf("oscillators[%d].decay_ms must not be negative: %g", index, osc.DecayMs)
	}

	for k, gain := range osc.Harmonics {
		if !isFinite(gain) {
			return fmt.Errorf("oscillators[%d].harmonics[%d] must be finite", index, k)
		}
	}

	return nil
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
