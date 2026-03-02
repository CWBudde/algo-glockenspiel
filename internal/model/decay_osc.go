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
	block4Coeff [NumModes]modeBlock4Coeff

	sampleRate float64
}

type modeBlock4Coeff struct {
	baseCos [4]float64
	baseSin [4]float64
	outX0   [4]float64
	outX1   [4]float64
	outX2   [4]float64
	c2 float64
	s2 float64
	c3 float64
	s3 float64
	c4 float64
	s4 float64
}

// NewQuadDecayOscillator returns a zero-state oscillator with defaults.
func NewQuadDecayOscillator(sampleRate float64) *QuadDecayOscillator {
	if sampleRate <= 0 {
		sampleRate = defaultOscSampleRate
	}

	oscillator := &QuadDecayOscillator{
		sampleRate: sampleRate,
		frequency:  [NumModes]float64{1000, 2000, 3000, 4000},
		decayMs:    [NumModes]float64{100, 50, 20, 10},
		amplitude:  [NumModes]float64{1, 0, 0, 0},
	}
	oscillator.calculateCoefficients()

	return oscillator
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

// SetMode updates one mode and recomputes its coefficients once.
func (o *QuadDecayOscillator) SetMode(mode int, amp, freq, decayMs float64) {
	if !validMode(mode) {
		panic(fmt.Sprintf("invalid mode index %d", mode))
	}

	o.amplitude[mode] = amp
	o.frequency[mode] = freq
	o.decayMs[mode] = decayMs
	o.calculateCoefficient(mode)
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
	inputValue := float64(input)
	sum := 0.0

	for i := range o.realState {
		temp := o.imagState[i]*o.cosCoeff[i] + o.realState[i]*o.sinCoeff[i]

		o.realState[i] = o.realState[i]*o.cosCoeff[i] - o.imagState[i]*o.sinCoeff[i]
		o.imagState[i] = o.amplitude[i]*inputValue + temp

		sum += temp
	}

	return float32(sum)
}

// ProcessBlock32 processes input into output.
func (o *QuadDecayOscillator) ProcessBlock32(input, output []float32) {
	if len(output) < len(input) {
		panic("output buffer too small")
	}

	if processBlock32AVX2(o, input, output) {
		return
	}

	o.processBlock32Generic(input, output)
}

func (o *QuadDecayOscillator) processBlock32Generic(input, output []float32) {
	r0, r1, r2, r3 := o.realState[0], o.realState[1], o.realState[2], o.realState[3]
	im0, im1, im2, im3 := o.imagState[0], o.imagState[1], o.imagState[2], o.imagState[3]

	a0, a1, a2, a3 := o.amplitude[0], o.amplitude[1], o.amplitude[2], o.amplitude[3]
	c0, c1, c2, c3 := o.cosCoeff[0], o.cosCoeff[1], o.cosCoeff[2], o.cosCoeff[3]
	s0, s1, s2, s3 := o.sinCoeff[0], o.sinCoeff[1], o.sinCoeff[2], o.sinCoeff[3]

	i := 0
	for ; i+3 < len(input); i += 4 {
		x0 := float64(input[i])
		x1 := float64(input[i+1])
		x2 := float64(input[i+2])
		x3 := float64(input[i+3])

		b0 := processModeBlock4(r0, im0, a0, c0, s0, o.block4Coeff[0], x0, x1, x2, x3)
		r0, im0 = b0.real, b0.imag
		b1 := processModeBlock4(r1, im1, a1, c1, s1, o.block4Coeff[1], x0, x1, x2, x3)
		r1, im1 = b1.real, b1.imag
		b2 := processModeBlock4(r2, im2, a2, c2, s2, o.block4Coeff[2], x0, x1, x2, x3)
		r2, im2 = b2.real, b2.imag
		b3 := processModeBlock4(r3, im3, a3, c3, s3, o.block4Coeff[3], x0, x1, x2, x3)
		r3, im3 = b3.real, b3.imag

		output[i] = float32(b0.out0 + b1.out0 + b2.out0 + b3.out0)
		output[i+1] = float32(b0.out1 + b1.out1 + b2.out1 + b3.out1)
		output[i+2] = float32(b0.out2 + b1.out2 + b2.out2 + b3.out2)
		output[i+3] = float32(b0.out3 + b1.out3 + b2.out3 + b3.out3)
	}

	for ; i < len(input); i++ {
		inputValue := float64(input[i])

		t0 := im0*c0 + r0*s0
		r0 = r0*c0 - im0*s0
		im0 = a0*inputValue + t0

		t1 := im1*c1 + r1*s1
		r1 = r1*c1 - im1*s1
		im1 = a1*inputValue + t1

		t2 := im2*c2 + r2*s2
		r2 = r2*c2 - im2*s2
		im2 = a2*inputValue + t2

		t3 := im3*c3 + r3*s3
		r3 = r3*c3 - im3*s3
		im3 = a3*inputValue + t3

		output[i] = float32(t0 + t1 + t2 + t3)
	}

	o.realState[0], o.realState[1], o.realState[2], o.realState[3] = r0, r1, r2, r3
	o.imagState[0], o.imagState[1], o.imagState[2], o.imagState[3] = im0, im1, im2, im3
}

type modeBlock4Result struct {
	out0 float64
	out1 float64
	out2 float64
	out3 float64
	real float64
	imag float64
}

func processModeBlock4(realState, imagState, amplitude, cosCoeff, sinCoeff float64, block4 modeBlock4Coeff, x0, x1, x2, x3 float64) modeBlock4Result {
	base0 := imagState*cosCoeff + realState*sinCoeff
	base1 := imagState*block4.c2 + realState*block4.s2
	base2 := imagState*block4.c3 + realState*block4.s3
	base3 := imagState*block4.c4 + realState*block4.s4

	return modeBlock4Result{
		out0: base0,
		out1: base1 + amplitude*cosCoeff*x0,
		out2: base2 + amplitude*(block4.c2*x0+cosCoeff*x1),
		out3: base3 + amplitude*(block4.c3*x0+block4.c2*x1+cosCoeff*x2),
		real: realState*block4.c4 - imagState*block4.s4 - amplitude*(block4.s3*x0+block4.s2*x1+sinCoeff*x2),
		imag: base3 + amplitude*(block4.c3*x0+block4.c2*x1+cosCoeff*x2+x3),
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
		o.block4Coeff[mode] = modeBlock4Coeff{}

		return
	}

	decayFactor := math.Exp(-math.Ln2 / (0.001 * decayMs * o.sampleRate))
	phase := 2 * math.Pi * o.frequency[mode] / o.sampleRate
	sinVal, cosVal := math.Sincos(phase)

	o.decayFactor[mode] = decayFactor
	o.cosCoeff[mode] = decayFactor * cosVal
	o.sinCoeff[mode] = decayFactor * sinVal

	c1 := o.cosCoeff[mode]
	s1 := o.sinCoeff[mode]
	c2 := c1*c1 - s1*s1
	s2 := 2 * c1 * s1
	c3 := c2*c1 - s2*s1
	s3 := c2*s1 + s2*c1
	o.block4Coeff[mode] = modeBlock4Coeff{
		baseCos: [4]float64{c1, c2, c3, c3*c1 - s3*s1},
		baseSin: [4]float64{s1, s2, s3, s3*c1 + c3*s1},
		outX0:   [4]float64{0, c1, c2, c3},
		outX1:   [4]float64{0, 0, c1, c2},
		outX2:   [4]float64{0, 0, 0, c1},
		c2: c2,
		s2: s2,
		c3: c3,
		s3: s3,
		c4: c3*c1 - s3*s1,
		s4: c3*s1 + s3*c1,
	}
}

func validMode(mode int) bool {
	return mode >= 0 && mode < NumModes
}
