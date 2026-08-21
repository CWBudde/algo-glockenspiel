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

// oscbank_avx2_amd64.s reaches into these arrays with hardcoded byte offsets,
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

	// ADDQ $16, DI: one frame of the partial accumulator.
	_ = sizeofFloat32*accLanes - 16
	_ = 16 - sizeofFloat32*accLanes

	// ADDQ $128, SI in reduceLanesAVX2: eight accumulator frames per pass.
	_ = sizeofFloat32*accLanes*reduceAVX2Frames - 128
	_ = 128 - sizeofFloat32*accLanes*reduceAVX2Frames

	// ADDQ $32, DI in reduceLanesAVX2: the eight scalars it wrote.
	_ = sizeofFloat32*reduceAVX2Frames - 32
	_ = 32 - sizeofFloat32*reduceAVX2Frames
)

func processRotorBlocks(re, im, cosCoeff, sinCoeff, amp []float32, blocks int, input, acc []float32) {
	if blocks == 0 || len(input) == 0 {
		return
	}

	if features := cpufeat.Detect(); !features.HasAVX2 || !features.HasFMA {
		processRotorBlocksGeneric(re, im, cosCoeff, sinCoeff, amp, blocks, input, acc)
		return
	}

	oscBankBlocksAVX2(
		&re[0], &im[0], &cosCoeff[0], &sinCoeff[0], &amp[0],
		blocks,
		&input[0], len(input),
		&acc[0],
	)
}

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

// reduceLanesAVX2 collapses samples frames of LaneWidth lanes into samples
// scalars. samples must be a positive multiple of 8.
//
//go:noescape
func reduceLanesAVX2(acc, output *float32, samples int)
