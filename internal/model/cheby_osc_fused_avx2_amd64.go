//go:build amd64

package model

import "github.com/cwbudde/glockenspiel/internal/cpufeat"

func processChebyshev4OscillatorBlockAVX2(o *QuadDecayOscillator, input, output []float32, gains4 *[4]float32) bool {
	if len(input) == 0 || len(output) < len(input) || gains4 == nil || !cpufeat.Detect().HasAVX2 {
		return false
	}

	processChebyshev4OscillatorBlockAVX2Asm(
		&o.realState[0],
		&o.imagState[0],
		&o.amplitude[0],
		&o.cosCoeff[0],
		&o.sinCoeff[0],
		&input[0],
		&gains4[0],
		&output[0],
		len(input),
	)
	return true
}

//go:noescape
func processChebyshev4OscillatorBlockAVX2Asm(realState, imagState, amplitude, cosCoeff, sinCoeff *float64, input, gains, output *float32, count int)
