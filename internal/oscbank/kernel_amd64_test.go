//go:build amd64

package oscbank

import (
	"fmt"
	"math"
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

// sse2Only is the forced feature set that reaches oscBankBlocksSSE2. AVX2 and
// FMA have to be off: the dispatcher prefers the wider kernel whenever it can,
// which on real amd64 hardware it usually can.
var sse2Only = cpufeat.Features{HasSSE2: true, HasSSE3: true}

// TestSSE2KernelMatchesPortableKernel runs the differential grid through the
// SSE2 dispatch path. The bound is what the contract requires of a packed
// backend that cannot fuse; the bit-identity below is what this one delivers.
func TestSSE2KernelMatchesPortableKernel(t *testing.T) {
	rng := rand.New(rand.NewSource(20260823))

	for _, numOsc := range []int{1, 4, 5, 16, 33} {
		for _, numHarm := range []int{1, 2, 4} {
			oscillators := testOscillators(numOsc, numHarm)

			input := make([]float32, 777)
			input[0] = 1

			for i := 1; i < len(input); i++ {
				input[i] = float32(rng.NormFloat64() * 0.05)
			}

			const stateSeed = 4242

			packed, tolerance := renderWithFeatures(t, sse2Only, oscillators, input, stateSeed)
			portable, _ := renderWithFeatures(t, cpufeat.Features{}, oscillators, input, stateSeed)

			requireWithinContract(t, fmt.Sprintf("sse2 %dx%d", numOsc, numHarm), packed, portable, tolerance)
		}
	}
}

// TestSSE2IsBitIdenticalToPortable is the claim the kernel's association was
// chosen for. SSE2 has no FMA, so it rounds the recursion in exactly the four
// places kernel_generic.go does, and the Go compiler cannot fuse those on amd64
// because FMA is not part of the amd64 baseline. The portable kernel is
// therefore an exact oracle for this backend, not merely a bounded one.
//
// This is an amd64-only property and must stay one: on arm64 the compiler does
// fuse a + b*c, so the portable kernel there is a different program.
func TestSSE2IsBitIdenticalToPortable(t *testing.T) {
	for _, numOsc := range []int{1, 4, 17, 64} {
		for _, numHarm := range []int{1, 3} {
			oscillators := testOscillators(numOsc, numHarm)

			// Every chunk length that matters: shorter than a chunk, exactly a
			// chunk, and a ragged tail past one.
			for _, n := range []int{1, 2, 7, 255, 256, 257, 700} {
				input := strikeInput(n)
				for i := 1; i < len(input); i++ {
					input[i] = float32(math.Sin(float64(i)) * 0.05)
				}

				const stateSeed = 8181

				packed, _ := renderWithFeatures(t, sse2Only, oscillators, input, stateSeed)
				portable, _ := renderWithFeatures(t, cpufeat.Features{}, oscillators, input, stateSeed)

				requireBitIdentical(t, fmt.Sprintf("sse2 %dx%d over %d samples", numOsc, numHarm, n), packed, portable)
			}
		}
	}
}

// TestSSE2LeavesPaddingLanesAlone guards the half-block offsets. A kernel that
// walked the wrong 16 bytes would still produce plausible audio, but it would
// disturb the padding lanes a partly filled bank holds at zero forever.
func TestSSE2LeavesPaddingLanesAlone(t *testing.T) {
	cpufeat.SetForcedFeatures(sse2Only)

	defer cpufeat.ResetDetection()

	bank := New(48000)
	if err := bank.SetOscillators(testOscillators(3, 1)); err != nil {
		t.Fatalf("SetOscillators: %v", err)
	}

	input := strikeInput(64)
	out := make([]float32, len(input))
	bank.ProcessBlock(input, out)

	for lane := bank.numRotors; lane < len(bank.re); lane++ {
		if bank.re[lane] != 0 || bank.im[lane] != 0 {
			t.Fatalf("padding lane %d drifted to (%g, %g)", lane, bank.re[lane], bank.im[lane])
		}
	}
}

func BenchmarkBank4x4SSE2(b *testing.B) {
	cpufeat.SetForcedFeatures(sse2Only)

	defer cpufeat.ResetDetection()

	benchmarkBank(b, 4, 4)
}

func BenchmarkBank16x4SSE2(b *testing.B) {
	cpufeat.SetForcedFeatures(sse2Only)

	defer cpufeat.ResetDetection()

	benchmarkBank(b, 16, 4)
}

func BenchmarkBank4x4Portable(b *testing.B) {
	cpufeat.SetForcedFeatures(cpufeat.Features{})

	defer cpufeat.ResetDetection()

	benchmarkBank(b, 4, 4)
}
