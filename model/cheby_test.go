package model

import (
	"math"
	"testing"
)

// chebyshevOracleFloat64 is the float64 recurrence the shaper used to run in
// the audio path, kept as a test-only oracle: it says what the polynomial sum
// is mathematically, independent of the float32 rounding every live path now
// shares.
func chebyshevOracleFloat64(input float64, gains []float64) float64 {
	if len(gains) == 0 {
		return input
	}

	clampedInput := math.Max(-1, math.Min(1, input))

	prevPrevTerm := 1.0
	prevTerm := clampedInput
	out := gains[0] * prevTerm

	for i := 1; i < len(gains); i++ {
		nextTerm := 2*clampedInput*prevTerm - prevPrevTerm
		out += gains[i] * nextTerm
		prevPrevTerm, prevTerm = prevTerm, nextTerm
	}

	return out
}

func chebyshevTestInput(length int) []float32 {
	input := make([]float32, length)
	for i := range input {
		// Amplitudes beyond unity on purpose: the clamp is part of the shaper.
		input[i] = float32(math.Sin(float64(i)*0.17) * 1.3)
	}

	return input
}

func float32Gains(gains []float64) []float32 {
	out := make([]float32, len(gains))
	for i, gain := range gains {
		out[i] = float32(gain)
	}

	return out
}

func TestChebyshevBlockMatchesTheFloat64Oracle(t *testing.T) {
	// Four gains take the specialised path, three and five the general one, so
	// all three shapes are checked against the same mathematics.
	for _, gains := range [][]float64{
		{1.0, 0.5, 0.3},
		{1.0, 0.5, 0.3, 0.2},
		{1.0, 0.5, 0.3, 0.2, 0.1},
	} {
		input := chebyshevTestInput(257)
		got := make([]float32, len(input))

		processChebyshevBlock(input, got, float32Gains(gains))

		for i := range input {
			want := chebyshevOracleFloat64(float64(input[i]), gains)
			if !approxEqual(float64(got[i]), want, 1e-5) {
				t.Fatalf("%d gains, sample %d: got %.8f want %.8f", len(gains), i, got[i], want)
			}
		}
	}
}

func TestChebyshevScalarClampsItsInput(t *testing.T) {
	gains := []float32{1, 0.5, 0.3, 0.2}

	// Beyond the interval the polynomials are defined on, the shaper must hold
	// its endpoint value rather than diverge with the fourth power of the input.
	for _, input := range []float32{1, 2, 1000} {
		if got, want := chebyshevScalar(input, gains), chebyshevScalar(1, gains); got != want {
			t.Fatalf("input %g: got %g, want the clamped value %g", input, got, want)
		}
	}

	for _, input := range []float32{-1, -2, -1000} {
		if got, want := chebyshevScalar(input, gains), chebyshevScalar(-1, gains); got != want {
			t.Fatalf("input %g: got %g, want the clamped value %g", input, got, want)
		}
	}
}

func TestChebyshevScalarWithoutGainsIsTransparent(t *testing.T) {
	if got := chebyshevScalar(0.75, nil); got != 0.75 {
		t.Fatalf("got %g, want the input unchanged", got)
	}
}

func BenchmarkProcessChebyshevBlock(b *testing.B) {
	input := chebyshevTestInput(512)
	output := make([]float32, len(input))
	gains := []float32{1.0, 0.5, 0.3, 0.2}

	b.ResetTimer()

	for range b.N {
		processChebyshevBlock(input, output, gains)
	}
}
