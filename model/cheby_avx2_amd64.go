//go:build amd64

package model

import "github.com/cwbudde/glockenspiel/internal/cpufeat"

// chebyAVX2Block is the kernel's vector width in float32 samples.
const chebyAVX2Block = 8

// processChebyshev4AVX2 shapes the chebyAVX2Block-aligned body of input and
// reports how many samples it wrote. Zero means the caller has to shape all of
// them scalar: no AVX2, or a block too short to fill one vector.
//
// The caller owns the tail deliberately. The kernel evaluates the recurrence in
// float32 in the same order chebyshevScalar does, so body and tail meet without
// a seam.
func processChebyshev4AVX2(input, output []float32, gains *[chebyshevFastGains]float32) int {
	if len(input) == 0 || len(output) < len(input) || gains == nil || !cpufeat.Detect().HasAVX2 {
		return 0
	}

	mainCount := len(input) &^ (chebyAVX2Block - 1)
	if mainCount == 0 {
		return 0
	}

	processChebyshev4AVX2Asm(&input[0], &output[0], &gains[0], mainCount)

	return mainCount
}

//go:noescape
func processChebyshev4AVX2Asm(input, output, gains *float32, count int)
