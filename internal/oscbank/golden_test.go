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

// The voice-major path needs the same cross-architecture pin for the same
// reason: rule one is a claim about two kernels that no single process can run
// together, and rule four fixes an accumulation order that a vector of bits is
// the only way to check from outside.
const (
	// goldenVoiceRotors is three rotor pairs, so the vector covers the
	// accumulation path as well as the arithmetic: the first pair writes acc and
	// the other two add into it.
	goldenVoiceRotors = 6

	// Two lanes are left unconfigured, so the vector also pins that an idle
	// voice stays at exactly zero however hard its excitation stream is driven.
	goldenLiveVoices = 6

	goldenVoiceSamples = 11
)

// goldenVoiceCase builds three rotor pairs of six sounding voices and a short
// chunk of interleaved excitation. As in goldenCase, every number is a small
// dyadic rational computed in float32: no Sincos, no Exp, no rand, because those
// are allowed to differ by an ULP between architectures and a golden vector that
// inherited their rounding would be testing them instead of the kernel.
func goldenVoiceCase() (voiceRotorState, []float32) {
	state := newVoiceRotorState(goldenVoiceRotors)

	for rotor := range goldenVoiceRotors {
		for voice := range goldenLiveVoices {
			lane := rotor*LaneWidth + voice
			index := rotor*goldenLiveVoices + voice

			decay := 1 - float32(index%16+1)/64
			unit := goldenUnitVectors[index%4]

			state.cosCoeff[lane] = decay * unit[0]
			state.sinCoeff[lane] = decay * unit[1]
			state.amp[lane] = (float32(index%7) - 3) / 4
			state.re[lane] = (float32((index*7)%13) - 6) / 8
			state.im[lane] = (float32((index*5)%11) - 5) / 8
		}
	}

	// Every lane is driven, including the two that hold no voice: a lane fed an
	// excitation it must ignore is the case a crosstalk bug needs.
	input := make([]float32, goldenVoiceSamples*LaneWidth)

	for voice := range LaneWidth {
		input[voice] = 1 - float32(voice)/8

		for frame := 1; frame < goldenVoiceSamples; frame++ {
			input[frame*LaneWidth+voice] = (float32((frame*3+voice)%5) - 2) / 16
		}
	}

	return state, input
}

// goldenVoiceFused is what a packed voice kernel with FMA renders from
// goldenVoiceCase, one frame of eight voices per row. It was read off the AVX2
// kernel; the NEON kernel reproduces it under emulation, and any future fused
// backend has to as well. The last two columns are the two lanes no voice
// occupies, and they are exactly zero on every frame.
var goldenVoiceFused = [goldenVoiceSamples * LaneWidth]uint32{
	0xbf038f5c, 0x3e73ae12, 0xbfc091eb, 0xbf97f0a4, 0x3d9170a0, 0x3e64f5c4, 0x00000000, 0x00000000,
	0xbef7057c, 0x3ea2d446, 0x3d8260d8, 0xbf7fd3b6, 0xbf260567, 0x3ebdbf8b, 0x00000000, 0x00000000,
	0xbe6b61a6, 0x3d8ed5ec, 0x3fa4bd3c, 0xbe8e3985, 0xbf6a714c, 0x3f0f9121, 0x00000000, 0x00000000,
	0x3f982966, 0xbdc4f376, 0xbf4a3674, 0x3edd4016, 0x3e9e168f, 0x3f1d9e13, 0x00000000, 0x00000000,
	0x3f8a91b7, 0xbdc4ea4e, 0xbf7209c4, 0x3f6b948e, 0x3f4c4b84, 0x3ec96c2b, 0x00000000, 0x00000000,
	0xbf101b87, 0x3cf1eda0, 0x3f76e452, 0x3f83caeb, 0xbcd9b110, 0x3e697438, 0x00000000, 0x00000000,
	0xbf3a4fe8, 0x3dc3139f, 0x3ed295e9, 0x3f3c8020, 0xbe7c9ebf, 0x3e4e6e1e, 0x00000000, 0x00000000,
	0xbeed52f9, 0x3d942f97, 0xbf56b402, 0x3e8acd94, 0x3e082439, 0x3e12b7b1, 0x00000000, 0x00000000,
	0xbf6c1e5e, 0xbda8ba66, 0x3e5584f6, 0xbe5e54f7, 0xbdaabe5e, 0x3e7d26ae, 0x00000000, 0x00000000,
	0x3e0d7ca3, 0xbe8454f5, 0x3f1040e6, 0xbefd5ad4, 0xbe703762, 0x3e6887e8, 0x00000000, 0x00000000,
	0x3fd5f0a4, 0xbea6177c, 0xbf178c76, 0xbf0485b2, 0x3e5e1673, 0x3e67938f, 0x00000000, 0x00000000,
}

// goldenVoicePortable is what processVoiceRotorsGeneric renders from the same
// case, and it is the same number at every GOAMD64 level and on arm64 for the
// same reason goldenPortable is: advanceRotor is shared, and its rounding
// barriers are what keep it one program.
var goldenVoicePortable = [goldenVoiceSamples * LaneWidth]uint32{
	0xbf038f5c, 0x3e73ae12, 0xbfc091eb, 0xbf97f0a4, 0x3d9170a0, 0x3e64f5c4, 0x00000000, 0x00000000,
	0xbef7057c, 0x3ea2d446, 0x3d8260d4, 0xbf7fd3b8, 0xbf260567, 0x3ebdbf8c, 0x00000000, 0x00000000,
	0xbe6b61ac, 0x3d8ed5ed, 0x3fa4bd3c, 0xbe8e3987, 0xbf6a714c, 0x3f0f9122, 0x00000000, 0x00000000,
	0x3f982967, 0xbdc4f379, 0xbf4a3674, 0x3edd4015, 0x3e9e1690, 0x3f1d9e13, 0x00000000, 0x00000000,
	0x3f8a91bb, 0xbdc4ea4c, 0xbf7209c4, 0x3f6b948e, 0x3f4c4b84, 0x3ec96c2a, 0x00000000, 0x00000000,
	0xbf101b84, 0x3cf1eda4, 0x3f76e453, 0x3f83caea, 0xbcd9b118, 0x3e697436, 0x00000000, 0x00000000,
	0xbf3a4feb, 0x3dc3139f, 0x3ed295e6, 0x3f3c801e, 0xbe7c9ebd, 0x3e4e6e1c, 0x00000000, 0x00000000,
	0xbeed5303, 0x3d942f9a, 0xbf56b404, 0x3e8acd93, 0x3e082438, 0x3e12b7af, 0x00000000, 0x00000000,
	0xbf6c1e62, 0xbda8ba67, 0x3e5584fb, 0xbe5e54f6, 0xbdaabe62, 0x3e7d26ae, 0x00000000, 0x00000000,
	0x3e0d7c9a, 0xbe8454f6, 0x3f1040e7, 0xbefd5ad4, 0xbe703764, 0x3e6887e8, 0x00000000, 0x00000000,
	0x3fd5f0a6, 0xbea6177c, 0xbf178c76, 0xbf0485b2, 0x3e5e1672, 0x3e67938e, 0x00000000, 0x00000000,
}

func requireGoldenVoice(t *testing.T, label string, got []float32, want [goldenVoiceSamples * LaneWidth]uint32) {
	t.Helper()

	for i, expected := range want {
		if math.Float32bits(got[i]) != expected {
			t.Fatalf("%s: frame %d voice %d is %#08x, the golden vector says %#08x (%g vs %g)",
				label, i/LaneWidth, i%LaneWidth, math.Float32bits(got[i]), expected,
				got[i], math.Float32frombits(expected))
		}
	}
}

func TestFusedPackedVoiceKernelsMatchTheGoldenVector(t *testing.T) {
	state, input := goldenVoiceCase()

	checked := 0

	for _, current := range availableBackends() {
		if !current.packedFused {
			continue
		}

		checked++

		requireGoldenVoice(t, current.name, state.clone().render(current, input), goldenVoiceFused)
	}

	if checked == 0 {
		t.Skip("no fused packed backend on this machine")
	}
}

func TestPortableVoiceKernelMatchesTheGoldenVector(t *testing.T) {
	state, input := goldenVoiceCase()

	requireGoldenVoice(t, "portable", state.clone().render(backend{name: "portable"}, input), goldenVoicePortable)
}
