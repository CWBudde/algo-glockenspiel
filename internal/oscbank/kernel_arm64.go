//go:build arm64

package oscbank

import "unsafe"

// reduceNEONFrames is how many output frames one pass of reduceLanesNEON
// collapses. Two FADDP instructions halve four frames' worth of lanes and a
// third finishes them, so the loop cannot take any other stride.
const reduceNEONFrames = 4

// oscbank_arm64.s reaches into these arrays with hardcoded byte offsets, and
// assembly is not type-checked against Go: change LaneWidth or accLanes and the
// kernel keeps building while it walks the wrong memory, which sounds like audio
// right up until it does not. These assertions are the build failure. uintptr is
// unsigned, so a mismatch in either direction overflows one of the two constant
// expressions.
const (
	_ = sizeofFloat32Arm64 - 4
	_ = 4 - sizeofFloat32Arm64

	// VLD1 (R0), [V0.S4, V1.S4, V2.S4, V3.S4] and ADD $64, R0: one VLD1 covers
	// a whole block pair, and the pair loop advances by exactly that much.
	_ = sizeofFloat32Arm64*2*LaneWidth - 64
	_ = 64 - sizeofFloat32Arm64*2*LaneWidth

	// The kernel splits each block across two vectors, so a block's low half
	// must be exactly the accumulator width.
	_ = sizeofFloat32Arm64*accLanes - 16
	_ = 16 - sizeofFloat32Arm64*accLanes

	// VLD1.P 64(R0) in reduceLanesNEON: four accumulator frames per pass.
	_ = sizeofFloat32Arm64*accLanes*reduceNEONFrames - 64
	_ = 64 - sizeofFloat32Arm64*accLanes*reduceNEONFrames

	// VST1.P [V6.S4], 16(R1) in reduceLanesNEON: the four scalars it wrote.
	_ = sizeofFloat32Arm64*reduceNEONFrames - 16
	_ = 16 - sizeofFloat32Arm64*reduceNEONFrames

	// VLD1.P 32(R9), [V30.S4, V31.S4] and VST1.P [V26.S4, V27.S4], 32(R10) in
	// oscVoiceRotorsNEON: one interleaved frame is one sample per voice, and it
	// is the same width on the way out because nothing folds. A rotor pair
	// there is the same 64 bytes a block pair is here, which is why that stride
	// needs no assertion of its own.
	_ = sizeofFloat32Arm64*LaneWidth - 32
	_ = 32 - sizeofFloat32Arm64*LaneWidth
)

// sizeofFloat32Arm64 duplicates the amd64 file's constant rather than sharing
// it, because the two are in different build-tagged files and neither
// architecture compiles the other's.
const sizeofFloat32Arm64 = unsafe.Sizeof(float32(0))

// processRotorBlocks always runs the packed kernel. Advanced SIMD is mandatory
// in ARMv8-A, so unlike amd64 there is no capability to dispatch on: there is no
// arm64 machine that can run this binary and not run the kernel. cpufeat reports
// HasASIMD unconditionally for the same reason, and gating on it would only add
// a way to be wrong under emulation, where the OS capability word is empty.
func processRotorBlocks(re, im, cosCoeff, sinCoeff, amp []float32, blocks int, input, acc []float32) {
	if blocks == 0 || len(input) == 0 {
		return
	}

	oscBankBlocksNEON(
		&re[0], &im[0], &cosCoeff[0], &sinCoeff[0], &amp[0],
		blocks,
		&input[0], len(input),
		&acc[0],
	)
}

// processVoiceRotors is the voice-major seam. Like processRotorBlocks it is
// ungated, and it has no reduction pass to go with it: in the [rotor][voice]
// layout the accumulator already is the output.
//
// acc is [samples][LaneWidth] and input must hold one frame more than that, for
// the same one-sample lookahead the rotor-major kernel takes.
func processVoiceRotors(re, im, cosCoeff, sinCoeff, amp []float32, rotors int, input, acc []float32) {
	if rotors == 0 || len(acc) == 0 {
		return
	}

	oscVoiceRotorsNEON(
		&re[0], &im[0], &cosCoeff[0], &sinCoeff[0], &amp[0],
		rotors,
		&input[0], len(acc)/LaneWidth,
		&acc[0],
	)
}

func reduceLanes(acc, output []float32) {
	if len(output) == 0 {
		return
	}

	vectorized := len(output) &^ (reduceNEONFrames - 1)
	if vectorized > 0 {
		reduceLanesNEON(&acc[0], &output[0], vectorized)
	}

	if vectorized < len(output) {
		reduceLanesGeneric(acc[vectorized*accLanes:], output[vectorized:])
	}
}

// oscBankBlocksNEON advances blocks rotor blocks over samples input frames,
// accumulating into acc ([samples][accLanes]). blocks must be even, acc must
// hold samples*accLanes float32s, and input must be readable at index samples:
// the kernel computes the excitation term one sample ahead.
//
//go:noescape
func oscBankBlocksNEON(re, im, cosCoeff, sinCoeff, amp *float32, blocks int, input *float32, samples int, acc *float32)

// reduceLanesNEON collapses samples frames of accLanes lanes into samples
// scalars. samples must be a positive multiple of reduceNEONFrames.
//
//go:noescape
func reduceLanesNEON(acc, output *float32, samples int)

// oscVoiceRotorsNEON advances rotors rotors of LaneWidth voices over samples
// interleaved input frames, accumulating into acc ([samples][LaneWidth]).
// rotors must be even, and input must be readable at frame samples: the kernel
// computes the excitation term one sample ahead.
//
//go:noescape
func oscVoiceRotorsNEON(re, im, cosCoeff, sinCoeff, amp *float32, rotors int, input *float32, samples int, acc *float32)
