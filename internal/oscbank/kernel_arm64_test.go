//go:build arm64

package oscbank

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

// The differential tests that live in kernel_amd64_test.go force cpufeat to
// pick between the packed kernel and the portable one. That lever does not
// exist here: Advanced SIMD is mandatory in ARMv8-A, so processRotorBlocks
// always dispatches to the NEON kernel and forcing an empty feature set changes
// nothing. Everything below therefore calls processRotorBlocksGeneric directly
// for the reference side.
//
// That also means the shared harness in fuzz_test.go compares NEON against
// itself on arm64: its "portable" backend goes through the same dispatcher. The
// fuzzer still earns its keep -- it is what proves the kernel does not produce
// a NaN, walk off the end of a buffer or mishandle a ragged chunk -- but the
// differential half of it is these tests, not that one.

// renderNEON and renderPortable run one chunk through the two kernels with the
// padded input the packed kernel needs, and return the reduced output.
func renderNEON(state rotorState, input []float32) []float32 {
	padded := make([]float32, len(input)+1)
	copy(padded, input)

	acc := make([]float32, len(input)*accLanes)
	out := make([]float32, len(input))

	oscBankBlocksNEON(
		&state.re[0], &state.im[0], &state.cosCoeff[0], &state.sinCoeff[0], &state.amp[0],
		state.blocks,
		&padded[0], len(input),
		&acc[0],
	)
	reduceLanes(acc, out)

	return out
}

func renderPortable(state rotorState, input []float32) []float32 {
	acc := make([]float32, len(input)*accLanes)
	out := make([]float32, len(input))

	processRotorBlocksGeneric(state.re, state.im, state.cosCoeff, state.sinCoeff, state.amp, state.blocks, input, acc)
	reduceLanesGeneric(acc, out)

	return out
}

// TestNEONKernelMatchesPortableKernel is the arm64 half of the contract's third
// rule. It sweeps block counts and chunk lengths, including the ragged ones the
// reduction's four-frame stride has to fall out of.
func TestNEONKernelMatchesPortableKernel(t *testing.T) {
	for _, blocks := range []int{2, 4, 8, 32} {
		for _, chunk := range []int{1, 2, 3, 4, 5, 7, 8, 9, 255, 256} {
			for regime := range regimeCount {
				state, input := generateCase(blocks, chunk, regime, amplitudeSpread, 0, int64(blocks*1000+chunk))

				tolerance := contractTolerance(state, input)
				if math.IsNaN(tolerance) || math.IsInf(tolerance, 0) {
					continue
				}

				neon := renderNEON(state.clone(), input)
				portable := renderPortable(state.clone(), input)

				label := fmt.Sprintf("%d blocks, %d samples, regime %d", blocks, chunk, regime)
				requireWithinContract(t, label, neon, portable, tolerance)
			}
		}
	}
}

// renderBankPortable is Bank.processChunk with the reference kernel
// substituted. The packed kernel's padded scratch buffer is not needed here:
// the reference reads no further than the chunk it is given.
func renderBankPortable(bank *Bank, input, output []float32) {
	for start := 0; start < len(input); start += blockSamples {
		end := min(start+blockSamples, len(input))
		acc := bank.acc[:(end-start)*accLanes]

		processRotorBlocksGeneric(bank.re, bank.im, bank.cosCoeff, bank.sinCoeff, bank.amp,
			bank.numBlocks, input[start:end], acc)
		reduceLanesGeneric(acc, output[start:end])
	}
}

// TestBankNEONMatchesPortableKernel is the same sweep at Bank level, where the
// coefficients come from real oscillator parameters and the chunk length
// crosses blockSamples. It is the arm64 counterpart of
// TestPackedKernelMatchesPortableKernel.
func TestBankNEONMatchesPortableKernel(t *testing.T) {
	rng := rand.New(rand.NewSource(20260821))

	for _, numOsc := range []int{1, 4, 5, 16, 33} {
		for _, numHarm := range []int{1, 2, 4} {
			oscillators := testOscillators(numOsc, numHarm)

			input := make([]float32, 777)
			input[0] = 1

			for i := 1; i < len(input); i++ {
				input[i] = float32(rng.NormFloat64() * 0.05)
			}

			const stateSeed = 4242

			packed := New(48000)
			portable := New(48000)

			for _, bank := range []*Bank{packed, portable} {
				if err := bank.SetOscillators(oscillators); err != nil {
					t.Fatalf("SetOscillators: %v", err)
				}

				seedBankState(bank, rand.New(rand.NewSource(stateSeed)))
			}

			tolerance := bankTolerance(packed, input)

			got := make([]float32, len(input))
			want := make([]float32, len(input))

			packed.ProcessBlock(input, got)
			renderBankPortable(portable, input, want)

			requireWithinContract(t, fmt.Sprintf("%dx%d", numOsc, numHarm), got, want, tolerance)
		}
	}
}

// roundedProduct exists to be un-fusable. Go's specification permits an
// implementation to fuse a*b + c, and the arm64 backend takes that permission
// everywhere it can; a call boundary is the one place it cannot reach across.
//
//go:noinline
func roundedProduct(a, b float32) float32 { return a * b }

// stepRotorUnfusedExcitation is stepRotor with one change: amp*x is rounded to
// float32 before it is subtracted back out of the accumulator seed.
//
// That single substitution is the entire difference between the NEON kernel and
// kernel_generic.go on arm64, and it is worth spelling out why. The compiler
// emits FMADDS for `ampx + re*sin` and `+ im*cos`, and FMULS/FMSUBS for
// `re*cos - im*sin`, which is exactly what the kernel does -- but it then
// re-fuses the last line, `next - ampx`, into FMSUBS next, amp, x. The portable
// kernel therefore recovers t with one rounding where the packed kernels use
// two, because a packed kernel has already materialised amp*x in a register and
// cannot un-round it.
//
// Everything else agrees to the bit. TestNEONKernelReproducesTheAssociation
// pins that, so a future edit to the kernel that reassociates anything at all
// fails immediately instead of hiding inside the contract's tolerance.
func stepRotorUnfusedExcitation(re, im, cosCoeff, sinCoeff, amp []float32, lane int, x float32) float32 {
	cosVal := cosCoeff[lane]
	sinVal := sinCoeff[lane]
	reVal := re[lane]
	imVal := im[lane]

	ampx := roundedProduct(amp[lane], x)
	next := ampx + reVal*sinVal + imVal*cosVal

	re[lane] = reVal*cosVal - imVal*sinVal
	im[lane] = next

	return next - ampx
}

// renderUnfusedExcitation mirrors processRotorBlocksGeneric exactly, down to
// the block pairing and the lane fold, and differs only in the rotor step.
func renderUnfusedExcitation(state rotorState, input []float32) []float32 {
	acc := make([]float32, len(input)*accLanes)
	out := make([]float32, len(input))

	for block := 0; block+1 < state.blocks; block += 2 {
		lo := block * LaneWidth
		hi := lo + 2*LaneWidth

		re, im := state.re[lo:hi:hi], state.im[lo:hi:hi]
		cosBlock, sinBlock := state.cosCoeff[lo:hi:hi], state.sinCoeff[lo:hi:hi]
		ampBlock := state.amp[lo:hi:hi]

		for i, x := range input {
			frame := acc[i*accLanes : i*accLanes+accLanes : i*accLanes+accLanes]

			for lane := range accLanes {
				lowA := stepRotorUnfusedExcitation(re, im, cosBlock, sinBlock, ampBlock, lane, x)
				lowB := stepRotorUnfusedExcitation(re, im, cosBlock, sinBlock, ampBlock, LaneWidth+lane, x)
				highA := stepRotorUnfusedExcitation(re, im, cosBlock, sinBlock, ampBlock, accLanes+lane, x)
				highB := stepRotorUnfusedExcitation(re, im, cosBlock, sinBlock, ampBlock, LaneWidth+accLanes+lane, x)

				folded := (lowA + lowB) + (highA + highB)

				if block == 0 {
					frame[lane] = folded
				} else {
					frame[lane] += folded
				}
			}
		}
	}

	reduceLanesGeneric(acc, out)

	return out
}

// TestNEONKernelReproducesTheAssociation is the stronger assertion the contract
// allows on arm64 but not on amd64. On amd64 the portable kernel cannot fuse at
// all, so the two backends are only comparable within a bound. Here they differ
// in precisely one rounding, and once that one is put back the kernel is
// bit-identical to a scalar Go reference -- every FMLA, every subtraction and
// every add in the lane fold lands on the same float32 word.
//
// This is a regression test for the association, not for the answer. Nothing in
// it may ever be relaxed into a tolerance: a tolerance here would silently
// accept a kernel that folds the lanes in a different order, which rule two of
// the contract forbids outright.
func TestNEONKernelReproducesTheAssociation(t *testing.T) {
	for _, blocks := range []int{2, 4, 8} {
		for _, chunk := range []int{1, 7, 64, 255, 256} {
			for regime := range regimeCount {
				for amplitude := range amplitudeModeCount {
					state, input := generateCase(blocks, chunk, regime, amplitude, 0, int64(blocks*100+chunk))

					neon := renderNEON(state.clone(), input)
					reference := renderUnfusedExcitation(state.clone(), input)

					label := fmt.Sprintf("%d blocks, %d samples, regime %d, amplitude %d", blocks, chunk, regime, amplitude)
					requireBitIdentical(t, label, neon, reference)
				}
			}
		}
	}
}

// TestNEONKernelLeavesPaddingLanesAlone guards the half-block split. NEON reads
// a block as two vectors, so a kernel that mixed the halves up would still look
// right on a full bank and only go wrong on a partly filled one -- which is
// every bank the synthesizer actually builds.
func TestNEONKernelLeavesPaddingLanesAlone(t *testing.T) {
	bank := New(48000)
	if err := bank.SetOscillators(testOscillators(3, 2)); err != nil {
		t.Fatalf("SetOscillators: %v", err)
	}

	seedBankState(bank, rand.New(rand.NewSource(7)))

	input := strikeInput(300)
	output := make([]float32, len(input))
	bank.ProcessBlock(input, output)

	for rotor := bank.numRotors; rotor < len(bank.re); rotor++ {
		if bank.re[rotor] != 0 || bank.im[rotor] != 0 {
			t.Fatalf("padding lane %d drifted to (%g, %g)", rotor, bank.re[rotor], bank.im[rotor])
		}
	}
}

// FuzzNEONMatchesGeneric is the differential fuzz target the shared one cannot
// be on arm64. FuzzOscBankMatchesGeneric reaches both of its backends through
// processRotorBlocks, and on arm64 that is the NEON kernel either way, so it
// compares the kernel against itself; what it still proves is that no input
// makes the kernel produce a NaN, and that every ragged chunk length is
// handled. This one holds the kernel to the portable reference for real.
//
// The seed corpus is the same pathology list, for the same reason: it runs as
// an ordinary test in CI whether or not anyone starts the fuzzer.
func FuzzNEONMatchesGeneric(f *testing.F) {
	seeds := []struct {
		pairs, samples, regime, amplitude uint8
		silentLanes                       uint32
		seed                              int64
	}{
		{0, 0, regimeLogUniform, amplitudeUnit, 0, 1},
		{3, 255, regimeSustained, amplitudeUnit, 0, 2},
		{1, 200, regimeCollapsing, amplitudeTiny, 0, 3},
		{0, 6, regimeLogUniform, amplitudeUnit, 0, 4},
		{1, 8, regimeSustained, amplitudeSpread, 0, 5},
		{3, 254, regimeSplit, amplitudeSpread, 0, 6},
		{2, 100, regimeSustained, amplitudeUnit, 0x0000FFFF, 7},
		{3, 64, regimeSustained, amplitudeUnit, 0xFFFFFFFE, 8},
		{1, 128, regimeSustained, amplitudeSilent, 0, 9},
		{2, 255, regimeCollapsing, amplitudeTiny, 0xAAAAAAAA, 10},
		{3, 255, regimeSplit, amplitudeSpread, 0x00FF00FF, 11},
	}

	for _, seed := range seeds {
		f.Add(seed.pairs, seed.samples, seed.regime, seed.amplitude, seed.silentLanes, seed.seed)
	}

	f.Fuzz(func(t *testing.T, pairs, samples, regime, amplitude uint8, silentLanes uint32, seed int64) {
		blocks := 2 * (1 + int(pairs)%4)
		chunk := 1 + int(samples)

		state, input := generateCase(blocks, chunk, int(regime)%regimeCount, int(amplitude)%amplitudeModeCount, silentLanes, seed)

		tolerance := contractTolerance(state, input)
		if math.IsNaN(tolerance) || math.IsInf(tolerance, 0) {
			t.Skip("degenerate case: the error envelope is not finite")
		}

		neon := renderNEON(state.clone(), input)

		for i, sample := range neon {
			if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
				t.Fatalf("neon: sample %d is %g on a bounded recursion", i, sample)
			}
		}

		requireWithinContract(t, "neon", neon, renderPortable(state.clone(), input), tolerance)

		// Rule two has no tolerance, and on arm64 it is checkable directly:
		// once amp*x is rounded the way a packed kernel has to round it, the
		// two are the same program down to the bit.
		requireBitIdentical(t, "neon vs unfused-excitation reference", neon, renderUnfusedExcitation(state.clone(), input))
	})
}

// BenchmarkBank4x4Portable is the arm64 counterpart of the amd64 benchmark of
// the same name, so the two architectures report the packed-versus-portable
// ratio the same way. It cannot force the portable path through cpufeat the way
// amd64 does, so it drives the reference kernel directly over the same chunking
// Bank.ProcessBlock uses.
func BenchmarkBank4x4Portable(b *testing.B) {
	bank := New(48000)
	if err := bank.SetOscillators(testOscillators(4, 4)); err != nil {
		b.Fatalf("SetOscillators: %v", err)
	}

	input := strikeInput(512)
	output := make([]float32, len(input))

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		renderBankPortable(bank, input, output)
	}

	if rotors := bank.NumRotors(); rotors > 0 && b.Elapsed() > 0 {
		nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
		b.ReportMetric(nsPerOp/float64(rotors), "ns/rotor-block")
	}
}

// BenchmarkReduceLanes4x4 isolates the reduction, which is the one part of the
// arm64 backend whose payoff is not obvious from the kernel's shape: FADDP does
// four frames in three instructions where the scalar loop does one frame in
// three adds.
func BenchmarkReduceLanes4x4(b *testing.B) {
	acc := make([]float32, blockSamples*accLanes)
	out := make([]float32, blockSamples)

	for i := range acc {
		acc[i] = float32(i%17) * 0.125
	}

	b.ResetTimer()

	for range b.N {
		reduceLanes(acc, out)
	}
}
