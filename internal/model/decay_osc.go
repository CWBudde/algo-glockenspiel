package model

import (
	"fmt"
	"math"
)

const (
	defaultOscSampleRate = 44100.0
	minDecayMs           = 1e-9
)

// QuadDecayOscillator models 4 decaying quadrature modes in parallel.
type QuadDecayOscillator struct {
	realState [NumModes]float64
	imagState [NumModes]float64

	amplitude [NumModes]float64
	frequency [NumModes]float64
	decayMs   [NumModes]float64

	decayFactor [NumModes]float64
	cosCoeff    [NumModes]float64
	sinCoeff    [NumModes]float64

	sampleRate float64
}

// NewQuadDecayOscillator returns a zero-state oscillator with defaults.
func NewQuadDecayOscillator(sampleRate float64) *QuadDecayOscillator {
	if sampleRate <= 0 {
		sampleRate = defaultOscSampleRate
	}

	o := &QuadDecayOscillator{
		sampleRate: sampleRate,
		frequency:  [NumModes]float64{1000, 2000, 3000, 4000},
		decayMs:    [NumModes]float64{100, 50, 20, 10},
		amplitude:  [NumModes]float64{1, 0, 0, 0},
	}
	o.calculateCoefficients()

	return o
}

// Reset clears all oscillator state.
func (o *QuadDecayOscillator) Reset() {
	o.realState = [NumModes]float64{}
	o.imagState = [NumModes]float64{}
}

// SetSampleRate changes sample rate and recomputes coefficients.
func (o *QuadDecayOscillator) SetSampleRate(sr float64) {
	if sr <= 0 {
		return
	}
	o.sampleRate = sr
	o.calculateCoefficients()
}

// SetFrequency sets one mode frequency in Hz.
func (o *QuadDecayOscillator) SetFrequency(mode int, freq float64) {
	if !validMode(mode) {
		panic(fmt.Sprintf("invalid mode index %d", mode))
	}
	o.frequency[mode] = freq
	o.calculateCoefficient(mode)
}

// SetAmplitude sets one mode amplitude.
func (o *QuadDecayOscillator) SetAmplitude(mode int, amp float64) {
	if !validMode(mode) {
		panic(fmt.Sprintf("invalid mode index %d", mode))
	}
	o.amplitude[mode] = amp
}

// SetDecay sets one mode decay in milliseconds.
func (o *QuadDecayOscillator) SetDecay(mode int, decayMs float64) {
	if !validMode(mode) {
		panic(fmt.Sprintf("invalid mode index %d", mode))
	}
	o.decayMs[mode] = decayMs
	o.calculateCoefficient(mode)
}

// MaxDecayFactor returns the largest per-sample decay factor among all modes.
func (o *QuadDecayOscillator) MaxDecayFactor() float64 {
	maxVal := o.decayFactor[0]
	for i := 1; i < NumModes; i++ {
		if o.decayFactor[i] > maxVal {
			maxVal = o.decayFactor[i]
		}
	}
	return maxVal
}

// ProcessSample32 processes one excitation sample and returns resonant output.
func (o *QuadDecayOscillator) ProcessSample32(input float32) float32 {
	in := float64(input)
	sum := 0.0

	for i := range o.realState {
		temp := o.imagState[i]*o.cosCoeff[i] + o.realState[i]*o.sinCoeff[i]

		o.realState[i] = o.realState[i]*o.cosCoeff[i] - o.imagState[i]*o.sinCoeff[i]
		o.imagState[i] = o.amplitude[i]*in + temp

		sum += temp
	}

	return float32(sum)
}

// ProcessBlock32 processes input into output.
func (o *QuadDecayOscillator) ProcessBlock32(input, output []float32) {
	if len(output) < len(input) {
		panic("output buffer too small")
	}
	for i, in := range input {
		output[i] = o.ProcessSample32(in)
	}
}

func (o *QuadDecayOscillator) calculateCoefficients() {
	for i := 0; i < NumModes; i++ {
		o.calculateCoefficient(i)
	}
}

func (o *QuadDecayOscillator) calculateCoefficient(mode int) {
	decayMs := o.decayMs[mode]
	if decayMs <= minDecayMs {
		o.decayFactor[mode] = 0
		o.cosCoeff[mode] = 0
		o.sinCoeff[mode] = 0
		return
	}

	decayFactor := math.Exp(-math.Ln2 / (0.001 * decayMs * o.sampleRate))
	phase := 2 * math.Pi * o.frequency[mode] / o.sampleRate
	sinVal, cosVal := math.Sincos(phase)

	o.decayFactor[mode] = decayFactor
	o.cosCoeff[mode] = decayFactor * cosVal
	o.sinCoeff[mode] = decayFactor * sinVal
}

func validMode(mode int) bool {
	return mode >= 0 && mode < NumModes
}
