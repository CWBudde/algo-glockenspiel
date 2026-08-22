//go:build amd64

package model

import (
	"math"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/cpufeat"
)

// TestChebyshevBodyTailAndFallbackAgree pins the contract that the three
// Chebyshev implementations collapsed into one: a sample must come out the same
// whether the vectorised body shaped it, the scalar tail did, or the whole
// block took the fallback because the machine has no AVX2.
//
// The block length is deliberately not a multiple of the vector width, so the
// kernel leaves a tail behind and the seam is actually exercised.
func TestChebyshevBodyTailAndFallbackAgree(t *testing.T) {
	if !cpufeat.Detect().HasAVX2 {
		t.Skip("AVX2 not available")
	}

	const blockLength = 37 // four vectors of eight plus a five-sample tail

	t.Cleanup(cpufeat.ResetDetection)

	input := chebyshevTestInput(blockLength)
	gains := []float32{1.0, 0.5, 0.3, 0.2}

	// NaN and both infinities in the body and again in the tail: they are what
	// the clamp's two implementations are most likely to disagree about, and an
	// excitation buffer does occasionally contain them.
	for i, pathological := range map[int]float32{
		5:  float32(math.NaN()),
		11: float32(math.Inf(1)),
		20: float32(math.Inf(-1)),
		33: float32(math.NaN()),
		34: float32(math.Inf(1)),
		35: float32(math.Inf(-1)),
	} {
		input[i] = pathological
	}

	cpufeat.SetForcedFeatures(cpufeat.Features{HasAVX2: true, HasFMA: true})

	vectorized := make([]float32, len(input))
	processChebyshevBlock(input, vectorized, gains)

	shaped := processChebyshev4AVX2(input, make([]float32, len(input)), (*[chebyshevFastGains]float32)(gains))
	if want := blockLength &^ (chebyAVX2Block - 1); shaped != want {
		t.Fatalf("the kernel shaped %d samples, want %d: the tail is not being exercised", shaped, want)
	}

	cpufeat.SetForcedFeatures(cpufeat.Features{})

	fallback := make([]float32, len(input))
	processChebyshevBlock(input, fallback, gains)

	// Bit-exact, not merely close: the kernel and the scalar reference evaluate
	// the same recurrence in the same order at the same precision.
	for i := range input {
		if vectorized[i] != fallback[i] {
			t.Fatalf("sample %d (%s): AVX2 %.9g, fallback %.9g",
				i, chebyshevPathName(i, shaped), vectorized[i], fallback[i])
		}
	}
}

func chebyshevPathName(index, shaped int) string {
	if index < shaped {
		return "vectorised body"
	}

	return "scalar tail"
}
