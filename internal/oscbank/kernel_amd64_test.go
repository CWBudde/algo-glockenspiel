//go:build amd64

package oscbank

import (
	"math"
	"math/rand"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/cpufeat"
)

// renderWithFeatures renders the same configuration with CPU detection forced,
// so one process can exercise both the packed kernel and the portable one.
func renderWithFeatures(t *testing.T, features cpufeat.Features, oscillators []Oscillator, input []float32) []float32 {
	t.Helper()

	cpufeat.SetForcedFeatures(features)

	defer cpufeat.ResetDetection()

	bank := New(48000)
	if err := bank.SetOscillators(oscillators); err != nil {
		t.Fatalf("SetOscillators: %v", err)
	}

	out := make([]float32, len(input))
	bank.ProcessBlock(input, out)

	return out
}

func TestPackedKernelMatchesPortableKernel(t *testing.T) {
	if !cpufeat.Detect().HasAVX2 || !cpufeat.Detect().HasFMA {
		t.Skip("host has no AVX2+FMA")
	}

	rng := rand.New(rand.NewSource(20260821))

	for _, numOsc := range []int{1, 4, 5, 16, 33} {
		for _, numHarm := range []int{1, 2, 4} {
			oscillators := testOscillators(numOsc, numHarm)

			// Non-trivial excitation: a strike followed by noise, so no lane
			// spends the block multiplying by zero.
			input := make([]float32, 777)
			input[0] = 1

			for i := 1; i < len(input); i++ {
				input[i] = float32(rng.NormFloat64() * 0.05)
			}

			packed := renderWithFeatures(t, cpufeat.Features{HasAVX2: true, HasFMA: true}, oscillators, input)
			portable := renderWithFeatures(t, cpufeat.Features{}, oscillators, input)

			peak := 0.0
			for _, v := range portable {
				peak = math.Max(peak, math.Abs(float64(v)))
			}

			// The packed kernel fuses its multiply-adds and the portable one
			// cannot, so the two agree to float32 rounding, not to the bit.
			// Phase 2 owns the bit-identity contract.
			tolerance := 1e-5 * peak
			for i := range packed {
				if math.Abs(float64(packed[i]-portable[i])) > tolerance {
					t.Fatalf("%dx%d sample %d: packed %g, portable %g (tolerance %g)",
						numOsc, numHarm, i, packed[i], portable[i], tolerance)
				}
			}
		}
	}
}

func TestPortableKernelHandlesEveryChunkLength(t *testing.T) {
	oscillators := testOscillators(9, 2)

	for _, n := range []int{1, 2, 7, 8, 9, 255, 256, 257, 512} {
		input := strikeInput(n)

		packed := renderWithFeatures(t, cpufeat.Features{HasAVX2: true, HasFMA: true}, oscillators, input)
		portable := renderWithFeatures(t, cpufeat.Features{}, oscillators, input)

		peak := 0.0
		for _, v := range portable {
			peak = math.Max(peak, math.Abs(float64(v)))
		}

		tolerance := 1e-5 * peak
		for i := range packed {
			if math.Abs(float64(packed[i]-portable[i])) > tolerance {
				t.Fatalf("length %d, sample %d: packed %g, portable %g (tolerance %g)", n, i, packed[i], portable[i], tolerance)
			}
		}
	}
}

func BenchmarkBank4x4Portable(b *testing.B) {
	cpufeat.SetForcedFeatures(cpufeat.Features{})

	defer cpufeat.ResetDetection()

	benchmarkBank(b, 4, 4)
}
