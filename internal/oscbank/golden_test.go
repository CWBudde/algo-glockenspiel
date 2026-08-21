package oscbank

import (
	"math"
	"testing"
)

// Rule one of the numeric contract says that every packed backend with FMA
// produces identical float32 words for identical inputs. Within one process
// that is checkable directly, and fuzz_test.go does check it -- but the
// interesting instance of the rule is across architectures, and no process can
// run an AVX2 kernel and a NEON kernel at the same time. This file closes that
// gap the only way it can be closed: with a vector of expected bits.
//
// The case is built entirely out of float32 arithmetic on small dyadic
// rationals. No math.Sincos, no math.Exp, no math/rand: those are the parts of
// the standard library that are allowed to differ by an ULP between
// architectures, and a golden vector that inherited their rounding would be
// testing them instead of the kernel.

// goldenUnitVectors are the (cos, sin) directions the golden rotors advance by,
// scaled by a per-lane decay factor below one so the recursion contracts.
var goldenUnitVectors = [4][2]float32{
	{0.6, 0.8},
	{0.8, -0.6},
	{-0.28, 0.96},
	{0.96, 0.28},
}

// goldenLiveRotors is deliberately less than a full pair, so the vector also
// pins the behaviour of the padding lanes that every real bank carries.
const goldenLiveRotors = 12

// goldenCase builds two blocks of rotors and a short chunk of excitation.
func goldenCase() (rotorState, []float32) {
	state := newRotorState(2)

	for lane := range goldenLiveRotors {
		decay := 1 - float32(lane+1)/64
		unit := goldenUnitVectors[lane%4]

		state.cosCoeff[lane] = decay * unit[0]
		state.sinCoeff[lane] = decay * unit[1]
		state.amp[lane] = (float32(lane%7) - 3) / 4
		state.re[lane] = (float32((lane*7)%13) - 6) / 8
		state.im[lane] = (float32((lane*5)%11) - 5) / 8
	}

	input := make([]float32, 12)
	input[0] = 1

	for i := 1; i < len(input); i++ {
		input[i] = (float32(i%5) - 2) / 16
	}

	return state, input
}

// goldenOutput is what a fused packed kernel renders from goldenCase. It was
// read off the AVX2 kernel; the NEON kernel reproduces it under emulation, and
// any future fused backend has to as well. Regenerate it only when the model
// itself changes, never to make a backend pass.
var goldenOutput = [12]uint32{
	0xc08168f6,
	0xc07e28e9,
	0x3e19ebec,
	0x3fd904f1,
	0x4001b41c,
	0x401a0a1a,
	0x3f6dc0ef,
	0xbf8a067e,
	0xbeca9388,
	0x3f59965a,
	0x3f095cfb,
	0x3f503610,
}

func TestFusedPackedKernelsMatchTheGoldenVector(t *testing.T) {
	state, input := goldenCase()

	checked := 0

	for _, current := range availableBackends() {
		if !current.packedFused {
			continue
		}

		checked++

		got := state.clone().render(current.features, input)

		for i, want := range goldenOutput {
			if math.Float32bits(got[i]) != want {
				t.Fatalf("%s: sample %d is %#08x, the golden vector says %#08x (%g vs %g)",
					current.name, i, math.Float32bits(got[i]), want, got[i], math.Float32frombits(want))
			}
		}
	}

	if checked == 0 {
		t.Skip("no fused packed backend on this machine")
	}
}
