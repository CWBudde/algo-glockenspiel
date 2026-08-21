//go:build amd64

package oscbank

import "github.com/cwbudde/glockenspiel/internal/cpufeat"

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

	vectorized := len(output) &^ 7
	if vectorized > 0 {
		reduceLanesAVX2(&acc[0], &output[0], vectorized)
	}

	if vectorized < len(output) {
		reduceLanesGeneric(acc[vectorized*LaneWidth:], output[vectorized:])
	}
}

// oscBankBlocksAVX2 advances blocks rotor blocks over samples input frames,
// accumulating into acc ([samples][LaneWidth]). blocks must be even and acc
// must hold samples*LaneWidth float32s.
//
//go:noescape
func oscBankBlocksAVX2(re, im, cosCoeff, sinCoeff, amp *float32, blocks int, input *float32, samples int, acc *float32)

// reduceLanesAVX2 collapses samples frames of LaneWidth lanes into samples
// scalars. samples must be a positive multiple of 8.
//
//go:noescape
func reduceLanesAVX2(acc, output *float32, samples int)
