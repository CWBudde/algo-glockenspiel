//go:build amd64

package model

import "github.com/cwbudde/glockenspiel/internal/cpufeat"

const chebyAVX2Block = 8

func processChebyshevBlockAVX2(input, output []float32, gains4 *[4]float32) bool {
	if len(input) == 0 || len(output) < len(input) || gains4 == nil || !cpufeat.Detect().HasAVX2 {
		return false
	}

	mainCount := len(input) &^ (chebyAVX2Block - 1)
	if mainCount == 0 {
		return false
	}

	processChebyshev4AVX2Asm(&input[0], &output[0], &gains4[0], mainCount)
	for i := mainCount; i < len(input); i++ {
		output[i] = chebyshev4Scalar(input[i], gains4)
	}
	return true
}

func chebyshev4Scalar(input float32, gains4 *[4]float32) float32 {
	x := input
	if x < -1 {
		x = -1
	}
	if x > 1 {
		x = 1
	}
	x2 := x * x
	t2 := 2*x2 - 1
	t3 := 4*x*x2 - 3*x
	t4 := 8*x2*x2 - 8*x2 + 1
	return gains4[0]*x + gains4[1]*t2 + gains4[2]*t3 + gains4[3]*t4
}

//go:noescape
func processChebyshev4AVX2Asm(input, output, gains *float32, count int)
