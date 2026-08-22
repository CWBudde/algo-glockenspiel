package model

import (
	"math"
	"testing"
)

// shapedBlockLength is long enough that the AVX2 kernel takes a body of several
// vectors and leaves a tail behind for chebyshevScalar, so a block test covers
// both paths on amd64 and stays a single scalar run everywhere else.
//
// It is a literal rather than a multiple of the kernel's block constant, which
// lives in cheby_avx2_amd64.go behind //go:build amd64: naming that constant
// from an untagged test file breaks the package build on every architecture
// that is not amd64, which is how this arrived -- green locally, red on the
// arm64 runner. 27 is three vectors of eight plus three, the arithmetic it
// replaces. The vector width is pinned by a compile-time assertion next to the
// kernel, so writing the answer down here does not put a second definition of
// it into circulation.
const shapedBlockLength = 27

// chebyshevOracleFloat64 is the float64 recurrence the shaper used to run in
// the audio path, kept as a test-only oracle: it says what the shaper computes
// mathematically, independent of the float32 rounding every live path now
// shares.
//
// The subtraction at the end is the shaper's defining property rather than a
// correction bolted onto it: a waveshaper has to map silence to silence, and
// the bare polynomial sum does not, because T_2, T_4 and every other even
// member are nonzero at the origin. Evaluating the same sum at zero and taking
// it off is the whole of it.
func chebyshevOracleFloat64(input float64, gains []float64) float64 {
	if len(gains) == 0 {
		return input
	}

	clampedInput := math.Max(-1, math.Min(1, input))

	return chebyshevSumFloat64(clampedInput, gains) - chebyshevSumFloat64(0, gains)
}

// chebyshevSumFloat64 evaluates sum over k of gains[k] * T_(k+1)(x).
func chebyshevSumFloat64(x float64, gains []float64) float64 {
	prevPrevTerm := 1.0
	prevTerm := x
	out := gains[0] * prevTerm

	for i := 1; i < len(gains); i++ {
		nextTerm := 2*x*prevTerm - prevPrevTerm
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

func TestChebyshevScalarClampsNaNToTheLowerEndpoint(t *testing.T) {
	gains := []float32{1, 0.5, 0.3, 0.2}

	// The policy is the AVX2 kernel's: VMAXPS/VMINPS return their second
	// operand when either operand is NaN, so a NaN sample comes out as the
	// value at -1 rather than poisoning every later sample of the oscillator
	// state it feeds.
	if got, want := chebyshevScalar(float32(math.NaN()), gains), chebyshevScalar(-1, gains); got != want {
		t.Fatalf("NaN: got %g, want the value at -1, %g", got, want)
	}

	if got, want := chebyshevScalar(float32(math.Inf(1)), gains), chebyshevScalar(1, gains); got != want {
		t.Fatalf("+Inf: got %g, want the value at 1, %g", got, want)
	}

	if got, want := chebyshevScalar(float32(math.Inf(-1)), gains), chebyshevScalar(-1, gains); got != want {
		t.Fatalf("-Inf: got %g, want the value at -1, %g", got, want)
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

// TestTheShaperMapsSilenceToSilence is the property the shaper lacked, and the
// reason a shipped preset sustained forever instead of decaying.
//
// The default preset puts the shaper ahead of the oscillator bank, so whatever
// it emits for a silent input is a DC excitation the bank keeps resolving long
// after the strike is over. For the shipped gains that constant was -0.3, the
// bar settled at an unchanging RMS and the auto-stop never fired. Zero in, zero
// out is not a nicety here; it is what makes a struck bar stop.
//
// Both paths are covered: the block is long enough to give the AVX2 kernel a
// full body and still leave it a scalar tail.
func TestTheShaperMapsSilenceToSilence(t *testing.T) {
	gainSets := [][]float64{
		{1.0, 0.5, 0.3, 0.2}, // the shipped preset's gains, whose sum at zero is -0.3
		{1.0},
		{1.0, 0.5},
		{0.4, -0.9, 0.25},
		{1.0, 0.5, 0.3, 0.2, -0.1, 0.05},
	}

	for _, gains := range gainSets {
		const length = shapedBlockLength

		input := make([]float32, length)
		output := make([]float32, length)

		processChebyshevBlock(input, output, float32Gains(gains))

		for i, got := range output {
			if got != 0 {
				t.Errorf("gains %v, sample %d: silent input shaped to %v, want 0", gains, i, got)

				break
			}
		}
	}
}

// TestTheShaperOnlyRemovesAConstant pins the other half: subtracting the value
// at zero must not otherwise disturb the transfer curve, so the difference
// between any two shaped samples is what it was before.
func TestTheShaperOnlyRemovesAConstant(t *testing.T) {
	gains := float32Gains([]float64{1.0, 0.5, 0.3, 0.2})

	const length = shapedBlockLength

	input := chebyshevTestInput(length)
	output := make([]float32, length)

	processChebyshevBlock(input, output, gains)

	offset := chebyshevZeroOffset(gains)
	if offset == 0 {
		t.Fatal("these gains are supposed to have a nonzero value at zero")
	}

	for i := range input {
		want := chebyshevScalar(input[i], gains) - offset
		if output[i] != want {
			t.Fatalf("sample %d: got %v, want %v", i, output[i], want)
		}
	}
}
