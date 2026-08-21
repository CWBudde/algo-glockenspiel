//go:build amd64

package oscbank

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/cpufeat"
)

// renderWithFeatures renders the same configuration with CPU detection forced,
// so one process can exercise both the packed kernel and the portable one. The
// rotor state is seeded from stateSeed before the render: a freshly constructed
// bank starts at re = im = 0, which leaves half of every packed multiply
// operating on zero and hides any error in the rotation itself.
//
// It returns the contract tolerance alongside the output, because the tolerance
// depends on the state the render started from and that state is gone by the
// time the render finishes.
func renderWithFeatures(t *testing.T, features cpufeat.Features, oscillators []Oscillator, input []float32, stateSeed int64) ([]float32, float64) {
	t.Helper()

	cpufeat.SetForcedFeatures(features)

	defer cpufeat.ResetDetection()

	bank := New(48000)
	if err := bank.SetOscillators(oscillators); err != nil {
		t.Fatalf("SetOscillators: %v", err)
	}

	seedBankState(bank, rand.New(rand.NewSource(stateSeed)))
	tolerance := bankTolerance(bank, input)

	out := make([]float32, len(input))
	bank.ProcessBlock(input, out)

	return out, tolerance
}

func TestPackedKernelMatchesPortableKernel(t *testing.T) {
	host := cpufeat.Detect()
	if !host.HasAVX2 || !host.HasFMA {
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

			const stateSeed = 4242

			packed, tolerance := renderWithFeatures(t, cpufeat.Features{
				HasSSE2: true, HasSSE3: true, HasAVX: true, HasAVX2: true, HasFMA: true,
			}, oscillators, input, stateSeed)
			portable, _ := renderWithFeatures(t, cpufeat.Features{}, oscillators, input, stateSeed)

			requireWithinContract(t, fmt.Sprintf("%dx%d", numOsc, numHarm), packed, portable, tolerance)
		}
	}
}

func TestPortableKernelHandlesEveryChunkLength(t *testing.T) {
	oscillators := testOscillators(9, 2)

	for _, n := range []int{1, 2, 7, 8, 9, 255, 256, 257, 512} {
		input := strikeInput(n)

		const stateSeed = 99

		packed, tolerance := renderWithFeatures(t, cpufeat.Features{
			HasSSE2: true, HasSSE3: true, HasAVX: true, HasAVX2: true, HasFMA: true,
		}, oscillators, input, stateSeed)
		portable, _ := renderWithFeatures(t, cpufeat.Features{}, oscillators, input, stateSeed)

		requireWithinContract(t, fmt.Sprintf("chunk of %d", n), packed, portable, tolerance)
	}
}

func BenchmarkBank4x4Portable(b *testing.B) {
	cpufeat.SetForcedFeatures(cpufeat.Features{})

	defer cpufeat.ResetDetection()

	benchmarkBank(b, 4, 4)
}
