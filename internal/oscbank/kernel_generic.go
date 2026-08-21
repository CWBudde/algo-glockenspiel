package oscbank

// processRotorBlocksGeneric is the portable reference kernel. It advances every
// rotor block over the whole sample chunk and accumulates each lane's
// contribution into acc, which is laid out as [len(input)][LaneWidth].
//
// Nothing is reduced across lanes here: the horizontal sum happens once per
// chunk in reduceLanes rather than once per sample.
//
// Blocks are walked in pairs, matching the packed kernels, which keep two
// blocks in flight to hide the recursion's multiply-then-add latency. The first
// pair writes acc and later pairs add into it, so no caller has to zero it.
func processRotorBlocksGeneric(re, im, cosCoeff, sinCoeff, amp []float32, blocks int, input, acc []float32) {
	for block := 0; block+1 < blocks; block += 2 {
		lo := block * LaneWidth
		hi := lo + 2*LaneWidth

		reBlock := re[lo:hi:hi]
		imBlock := im[lo:hi:hi]
		cosBlock := cosCoeff[lo:hi:hi]
		sinBlock := sinCoeff[lo:hi:hi]
		ampBlock := amp[lo:hi:hi]

		for i, x := range input {
			out := acc[i*LaneWidth : i*LaneWidth+LaneWidth : i*LaneWidth+LaneWidth]

			for lane := range LaneWidth {
				first := stepRotor(reBlock, imBlock, cosBlock, sinBlock, ampBlock, lane, x)
				second := stepRotor(reBlock, imBlock, cosBlock, sinBlock, ampBlock, LaneWidth+lane, x)

				if block == 0 {
					out[lane] = first + second
				} else {
					out[lane] += first + second
				}
			}
		}
	}
}

// stepRotor advances one rotor by one sample and returns its output term.
//
// The excitation is folded into the accumulator seed rather than added to t
// afterwards, matching the packed kernels: they do it to shorten the recursion's
// dependency chain, and the reference follows so both associate the same way.
func stepRotor(re, im, cosCoeff, sinCoeff, amp []float32, lane int, x float32) float32 {
	cosVal := cosCoeff[lane]
	sinVal := sinCoeff[lane]
	reVal := re[lane]
	imVal := im[lane]

	ampx := amp[lane] * x
	next := ampx + reVal*sinVal + imVal*cosVal

	re[lane] = reVal*cosVal - imVal*sinVal
	im[lane] = next

	return next - ampx
}

// reduceLanesGeneric collapses the per-lane accumulator into one sample per
// frame. The pairwise tree order is fixed so every backend sums in the same
// order.
func reduceLanesGeneric(acc, output []float32) {
	for i := range output {
		lane := acc[i*LaneWidth : i*LaneWidth+LaneWidth : i*LaneWidth+LaneWidth]

		a := lane[0] + lane[1]
		b := lane[2] + lane[3]
		c := lane[4] + lane[5]
		d := lane[6] + lane[7]

		output[i] = (a + b) + (c + d)
	}
}
