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
	re       []float32
	im       []float32
	cosCoeff []float32
	sinCoeff []float32
	amp      []float32

	decayFactor []float64

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

	b.oscillators = make([]Oscillator, len(oscillators))
	for i, osc := range oscillators {
		b.oscillators[i] = osc.Clone()
	}

	b.numOsc = len(oscillators)
	b.numHarm = numHarm
	b.numRotors = b.numOsc * b.numHarm

	// Round up to an even number of blocks so a 16-lane kernel can consume two
	// blocks at a time without a separate tail path.
	blocks := (b.numRotors + LaneWidth - 1) / LaneWidth
	if blocks%2 == 1 {
		blocks++
	}

	b.numBlocks = blocks
	b.allocate(blocks * LaneWidth)
	b.calculateCoefficients()

	return nil
}

func (b *Bank) allocate(size int) {
	if cap(b.re) >= size {
		b.re = b.re[:size]
		b.im = b.im[:size]
		b.cosCoeff = b.cosCoeff[:size]
		b.sinCoeff = b.sinCoeff[:size]
		b.amp = b.amp[:size]
		b.decayFactor = b.decayFactor[:size]

		clear(b.re)
		clear(b.im)

		return
	}

	b.re = make([]float32, size)
	b.im = make([]float32, size)
	b.cosCoeff = make([]float32, size)
	b.sinCoeff = make([]float32, size)
	b.amp = make([]float32, size)
	b.decayFactor = make([]float64, size)
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
	clear(b.cosCoeff)
	clear(b.sinCoeff)
	clear(b.amp)
	clear(b.decayFactor)

	rotor := 0

	for _, osc := range b.oscillators {
		decayFactor, decaying := decayFactorFor(osc.DecayMs, b.sampleRate)

		for harmonic := 0; harmonic < b.numHarm; harmonic++ {
			gain, active := harmonicGain(osc, harmonic)
			if !decaying || !active {
				rotor++
				continue
			}

			phase := 2 * math.Pi * float64(harmonic+1) * osc.Frequency / b.sampleRate
			sinVal, cosVal := math.Sincos(phase)

			b.decayFactor[rotor] = decayFactor
			b.cosCoeff[rotor] = float32(decayFactor * cosVal)
			b.sinCoeff[rotor] = float32(decayFactor * sinVal)
			b.amp[rotor] = float32(osc.Amplitude * gain)

			rotor++
		}
	}
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
