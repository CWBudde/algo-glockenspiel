//go:build amd64

package oscbank

import (
	"unsafe"

	"github.com/cwbudde/glockenspiel/internal/cpufeat"
)

// reduceAVX2Frames is how many output frames one pass of reduceLanesAVX2
// collapses. The permute table that undoes VHADDPS's per-half interleaving is
// exactly that wide, so the loop cannot take any other stride.
const reduceAVX2Frames = 8

// The two packed kernels reach into these arrays with hardcoded byte offsets,
// and assembly is not type-checked against Go: change LaneWidth or accLanes and
// the kernel keeps building while it walks the wrong memory, which sounds like
// audio right up until it does not. These assertions are the build failure.
// uintptr is unsigned, so a mismatch in either direction overflows one of the
// two constant expressions.
const (
	sizeofFloat32 = unsafe.Sizeof(float32(0))

	_ = sizeofFloat32 - 4
	_ = 4 - sizeofFloat32

	// VMOVUPS 32(AX): the second block of the pair held in registers.
	_ = sizeofFloat32*LaneWidth - 32
	_ = 32 - sizeofFloat32*LaneWidth

	// ADDQ $64, AX: advance every coefficient array by one block pair.
	_ = sizeofFloat32*2*LaneWidth - 64
	_ = 64 - sizeofFloat32*2*LaneWidth

	// ADDQ $16, DI: one frame of the partial accumulator. It is also the
	// half-block stride the SSE2 kernel walks with MOVUPS 16(AX) and 48(AX),
	// which is the same number for the same reason: four float32 lanes.
	_ = sizeofFloat32*accLanes - 16
	_ = 16 - sizeofFloat32*accLanes

	// MOVUPS 48(AX) in oscBankBlocksSSE2: the second block's high half.
	_ = sizeofFloat32*(LaneWidth+accLanes) - 48
	_ = 48 - sizeofFloat32*(LaneWidth+accLanes)

	// ADDQ $128, SI in reduceLanesAVX2: eight accumulator frames per pass.
	_ = sizeofFloat32*accLanes*reduceAVX2Frames - 128
	_ = 128 - sizeofFloat32*accLanes*reduceAVX2Frames

	// ADDQ $32, DI in reduceLanesAVX2: the eight scalars it wrote.
	_ = sizeofFloat32*reduceAVX2Frames - 32
	_ = 32 - sizeofFloat32*reduceAVX2Frames
)

// The voice-major kernels reuse most of those offsets, because a rotor pair in
// the [rotor][voice] layout occupies exactly the 64 bytes a block pair does in
// the rotor-major one. Two offsets mean something new there and get their own
// wall, so that a change to LaneWidth is caught by an assertion that names the
// instruction it would break rather than one that happens to share a number.
const (
	// ADDQ $32, SI and ADDQ $32, DI in oscVoiceRotorsAVX2, and VMOVUPS 32(SI)
	// for the lookahead: one interleaved frame is one sample per voice, and the
	// accumulator carries the same frame because nothing folds. The lookahead
	// is why the caller pads its scratch buffer by a whole frame.
	_ = sizeofFloat32*LaneWidth - 32
	_ = 32 - sizeofFloat32*LaneWidth

	// MOVUPS 16(SI) and MOVUPS 16(DI) in oscVoiceRotorsSSE2: XMM covers half a
	// frame, so voices 0-3 and voices 4-7 are two loads and two stores.
	_ = sizeofFloat32*(LaneWidth/2) - 16
	_ = 16 - sizeofFloat32*(LaneWidth/2)
)

func processRotorBlocks(re, im, cosCoeff, sinCoeff, amp []float32, blocks int, input, acc []float32) {
	if blocks == 0 || len(input) == 0 {
		return
	}

	features := cpufeat.Detect()

	switch {
	case features.HasAVX2 && features.HasFMA:
		oscBankBlocksAVX2(
			&re[0], &im[0], &cosCoeff[0], &sinCoeff[0], &amp[0],
			blocks,
			&input[0], len(input),
			&acc[0],
		)

	case features.HasSSE2:
		oscBankBlocksSSE2(
			&re[0], &im[0], &cosCoeff[0], &sinCoeff[0], &amp[0],
			blocks,
			&input[0], len(input),
			&acc[0],
		)

	default:
		// SSE2 is architecturally guaranteed on amd64, so this arm is
		// unreachable on hardware: the only way to get here is a test that
		// forces the flag off. That is deliberate. The portable kernel is the
		// numeric reference the other two are measured against, and a reference
		// nothing ever runs is a reference nobody trusts.
		processRotorBlocksGeneric(re, im, cosCoeff, sinCoeff, amp, blocks, input, acc)
	}
}

// processVoiceRotors is the voice-major seam: same dispatch, same three
// implementations, and no reduction pass to go with it, because in the
// [rotor][voice] layout the accumulator already is the output.
//
// acc is [samples][LaneWidth] and input must hold one frame more than that: the
// packed kernels compute amp*x one sample ahead, and a sample is a whole frame
// on this path.
func processVoiceRotors(re, im, cosCoeff, sinCoeff, amp []float32, rotors int, input, acc []float32) {
	if rotors == 0 || len(acc) == 0 {
		return
	}

	samples := len(acc) / LaneWidth

	features := cpufeat.Detect()

	switch {
	case features.HasAVX2 && features.HasFMA:
		oscVoiceRotorsAVX2(
			&re[0], &im[0], &cosCoeff[0], &sinCoeff[0], &amp[0],
			rotors,
			&input[0], samples,
			&acc[0],
		)

	case features.HasSSE2:
		oscVoiceRotorsSSE2(
			&re[0], &im[0], &cosCoeff[0], &sinCoeff[0], &amp[0],
			rotors,
			&input[0], samples,
			&acc[0],
		)

	default:
		processVoiceRotorsGeneric(re, im, cosCoeff, sinCoeff, amp, rotors, input, acc)
	}
}

// reduceLanes deliberately has no SSE2 path, unlike processRotorBlocks.
//
// The horizontal fold is the one place where SSE2 is not simply a narrower
// AVX2: VHADDPS is SSE3, so a 4-lane version would have to transpose four
// frames with UNPCKLPS/UNPCKHPS/MOVLHPS before it could add them. That is
// roughly four uops per frame, which is what the scalar loop already costs --
// the reduction is memory-shaped, not arithmetic-shaped, and it is two uops per
// sample even in the AVX2 version. Adding a third implementation of a fixed
// summation order for no measurable gain buys one more surface on which rule
// two of the numeric contract could be broken, so the split stays AVX2 or
// portable.
func reduceLanes(acc, output []float32) {
	if len(output) == 0 {
		return
	}

	if !cpufeat.Detect().HasAVX2 {
		reduceLanesGeneric(acc, output)
		return
	}

	vectorized := len(output) &^ (reduceAVX2Frames - 1)
	if vectorized > 0 {
		reduceLanesAVX2(&acc[0], &output[0], vectorized)
	}

	if vectorized < len(output) {
		reduceLanesGeneric(acc[vectorized*accLanes:], output[vectorized:])
	}
}

// oscBankBlocksAVX2 advances blocks rotor blocks over samples input frames,
// accumulating into acc ([samples][accLanes]). blocks must be even and acc must
// hold samples*accLanes float32s.
//
//go:noescape
func oscBankBlocksAVX2(re, im, cosCoeff, sinCoeff, amp *float32, blocks int, input *float32, samples int, acc *float32)

// oscBankBlocksSSE2 is the 4-lane form of the same pass, with the same
// requirements on blocks and acc. It has no FMA, so it associates the recursion
// exactly as kernel_generic.go does and is bit-identical to it on amd64; see
// the header of oscbank_sse2_amd64.s.
//
//go:noescape
func oscBankBlocksSSE2(re, im, cosCoeff, sinCoeff, amp *float32, blocks int, input *float32, samples int, acc *float32)

// reduceLanesAVX2 collapses samples frames of LaneWidth lanes into samples
// scalars. samples must be a positive multiple of 8.
//
//go:noescape
func reduceLanesAVX2(acc, output *float32, samples int)

// oscVoiceRotorsAVX2 advances rotors rotors of LaneWidth voices over samples
// interleaved input frames, accumulating into acc ([samples][LaneWidth]).
// rotors must be even, and input must be readable at frame samples: the kernel
// computes the excitation term one sample ahead.
//
//go:noescape
func oscVoiceRotorsAVX2(re, im, cosCoeff, sinCoeff, amp *float32, rotors int, input *float32, samples int, acc *float32)

// oscVoiceRotorsSSE2 is the 4-lane form of the same pass, with the same
// requirements. It has no FMA, so it associates the recursion exactly as
// kernel_generic.go does and is bit-identical to processVoiceRotorsGeneric on
// amd64, for the reason oscBankBlocksSSE2 is on the rotor-major path.
//
//go:noescape
func oscVoiceRotorsSSE2(re, im, cosCoeff, sinCoeff, amp *float32, rotors int, input *float32, samples int, acc *float32)
