package oscbank

import (
	"math"
	"testing"
)

// Two rules of the numeric contract are about agreement between programs, and
// both of them are checkable inside one process only up to a point. Rule one
// says every packed backend with FMA produces identical float32 words; the
// interesting instance of that is across architectures, and no process can run
// an AVX2 kernel and a NEON kernel. The portable kernel's claim to be one
// program has the same shape: TestPortableKernelDoesNotFuse asserts it from the
// inside, on whatever target happens to be running.
//
// This file closes both gaps the only way they can be closed, with vectors of
// expected bits. Together they also pin something neither of them says alone:
// the divergence between a fused packed backend and the reference is the same
// number on every target. If both sides are bit-identical everywhere, so is the
// distance between them, and the fused/unfused split really is a property of the
// backend rather than of the toolchain.
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

const (
	// goldenBlocks is three block pairs, so the vector covers the accumulation
	// path as well as the arithmetic: the first pair writes acc and the other
	// two add into it, which a single pair would never exercise.
	goldenBlocks = 6

	// goldenLiveRotors is deliberately short of goldenBlocks*LaneWidth, so the
	// vector also pins the padding lanes every real bank carries.
	goldenLiveRotors = 40

	// goldenSamples is not a multiple of either reduction stride -- eight on
	// AVX2, four on NEON -- so both packed reductions have to hand a tail to
	// reduceLanesGeneric and both tails are pinned here too.
	goldenSamples = 37
)

// goldenCase builds three block pairs of rotors and a short chunk of excitation.
func goldenCase() (rotorState, []float32) {
	state := newRotorState(goldenBlocks)

	for lane := range goldenLiveRotors {
		decay := 1 - float32(lane%16+1)/64
		unit := goldenUnitVectors[lane%4]

		state.cosCoeff[lane] = decay * unit[0]
		state.sinCoeff[lane] = decay * unit[1]
		state.amp[lane] = (float32(lane%7) - 3) / 4
		state.re[lane] = (float32((lane*7)%13) - 6) / 8
		state.im[lane] = (float32((lane*5)%11) - 5) / 8
	}

	input := make([]float32, goldenSamples)
	input[0] = 1

	for i := 1; i < len(input); i++ {
		input[i] = (float32(i%5) - 2) / 16
	}

	return state, input
}

// goldenFused is what a packed kernel with FMA renders from goldenCase. It was
// read off the AVX2 kernel; the NEON kernel reproduces it under emulation, and
// any future fused backend has to as well.
var goldenFused = [goldenSamples]uint32{
	0xc02be51f, 0xc027b1bc, 0xbebe9634, 0x3fa84802, 0x4003b4ab,
	0x3f9630d6, 0x3e6e20d0, 0xbe9991d8, 0xbf2da052, 0xbe10f4e4,
	0x3f391f17, 0x3f071124, 0xbf02a5c0, 0xbf8b6e87, 0xbf9110a9,
	0xbf06aa75, 0x3f8e2f9c, 0x3fef437c, 0x3f26c1ab, 0xbf703638,
	0xbfc48266, 0xbf824f4e, 0x3dd14a98, 0x3f9f8c4d, 0x3faeff13,
	0x3ec0b6ea, 0xbeb716aa, 0xbf30797d, 0xbf43ccc1, 0xbe4e2885,
	0x3f265296, 0x3f7dd1d0, 0x3ea8bbee, 0xbf1f337a, 0xbf8c1553,
	0xbf462071, 0x3ee1c2f7,
}

// goldenPortable is what kernel_generic.go renders from the same case. It is
// the same on amd64 at every GOAMD64 level and on arm64, which is the whole
// point of the anti-contraction barriers in advanceRotor: remove them and this
// vector stops being one number.
var goldenPortable = [goldenSamples]uint32{
	0xc02be520, 0xc027b1bc, 0xbebe963e, 0x3fa84803, 0x4003b4ab,
	0x3f9630d5, 0x3e6e20be, 0xbe9991e0, 0xbf2da05a, 0xbe10f4da,
	0x3f391f1b, 0x3f071124, 0xbf02a5c4, 0xbf8b6e87, 0xbf9110ab,
	0xbf06aa76, 0x3f8e2f9e, 0x3fef437c, 0x3f26c1a7, 0xbf70363a,
	0xbfc48268, 0xbf824f4f, 0x3dd14aa5, 0x3f9f8c4f, 0x3faeff14,
	0x3ec0b6eb, 0xbeb716a7, 0xbf30797f, 0xbf43ccc3, 0xbe4e287e,
	0x3f265296, 0x3f7dd1ce, 0x3ea8bbed, 0xbf1f3379, 0xbf8c1552,
	0xbf462071, 0x3ee1c300,
}

func requireGolden(t *testing.T, label string, got []float32, want [goldenSamples]uint32) {
	t.Helper()

	for i, expected := range want {
		if math.Float32bits(got[i]) != expected {
			t.Fatalf("%s: sample %d is %#08x, the golden vector says %#08x (%g vs %g)",
				label, i, math.Float32bits(got[i]), expected, got[i], math.Float32frombits(expected))
		}
	}
}

func TestFusedPackedKernelsMatchTheGoldenVector(t *testing.T) {
	state, input := goldenCase()

	checked := 0

	for _, current := range availableBackends() {
		if !current.packedFused {
			continue
		}

		checked++

		requireGolden(t, current.name, state.clone().render(current.features, input), goldenFused)
	}

	if checked == 0 {
		t.Skip("no fused packed backend on this machine")
	}
}

// TestPortableKernelMatchesTheGoldenVector is the outside view of
// TestPortableKernelDoesNotFuse. That test reads the arithmetic the compiler
// chose on the target in front of it; this one pins the number every target has
// to arrive at, so a build flag or an architecture that quietly reintroduced a
// contraction fails here even if it fused somewhere the other test does not
// look.
func TestPortableKernelMatchesTheGoldenVector(t *testing.T) {
	state, input := goldenCase()

	acc := make([]float32, len(input)*accLanes)
	out := make([]float32, len(input))

	clone := state.clone()
	processRotorBlocksGeneric(clone.re, clone.im, clone.cosCoeff, clone.sinCoeff, clone.amp, clone.blocks, input, acc)
	reduceLanesGeneric(acc, out)

	requireGolden(t, "portable", out, goldenPortable)
}
