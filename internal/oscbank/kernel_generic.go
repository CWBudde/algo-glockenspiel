package oscbank

// processRotorBlocksGeneric is the portable reference kernel. It advances every
// rotor block over the whole sample chunk and accumulates into acc, which is
// laid out as [len(input)][accLanes].
//
// The eight lanes of a block fold down to four on the way into acc -- that much
// the kernel gets for free -- but no further: the rest of the horizontal sum
// happens once per chunk in reduceLanes rather than once per sample.
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
			out := acc[i*accLanes : i*accLanes+accLanes : i*accLanes+accLanes]

			for lane := range accLanes {
				// The four rotors that fold into this accumulator lane: the low
				// and high halves of both blocks in the pair.
				loA, hiA := lane, accLanes+lane
				loB, hiB := LaneWidth+lane, LaneWidth+accLanes+lane

				var lowA, lowB, highA, highB float32

				reBlock[loA], imBlock[loA], lowA = advanceRotor(reBlock[loA], imBlock[loA], cosBlock[loA], sinBlock[loA], ampBlock[loA], x)
				reBlock[loB], imBlock[loB], lowB = advanceRotor(reBlock[loB], imBlock[loB], cosBlock[loB], sinBlock[loB], ampBlock[loB], x)
				reBlock[hiA], imBlock[hiA], highA = advanceRotor(reBlock[hiA], imBlock[hiA], cosBlock[hiA], sinBlock[hiA], ampBlock[hiA], x)
				reBlock[hiB], imBlock[hiB], highB = advanceRotor(reBlock[hiB], imBlock[hiB], cosBlock[hiB], sinBlock[hiB], ampBlock[hiB], x)

				folded := (lowA + lowB) + (highA + highB)

				if block == 0 {
					out[lane] = folded
				} else {
					out[lane] += folded
				}
			}
		}
	}
}

// advanceRotor advances one rotor by one sample. It returns the rotor's new
// state and its output term, and the caller stores the state back.
//
// The excitation is folded into the accumulator seed rather than added to t
// afterwards, matching the packed kernels: they do it to shorten the recursion's
// dependency chain, and the reference follows so both associate the same way.
//
// Every product is bound to its own float32 before it is added or subtracted,
// the same way model.chebyshevScalar is. That is not decoration. The Go
// specification lets an implementation contract a multiply followed by an add
// into one fused operation that rounds once instead of twice, and gc takes it:
// on arm64 at every level, and on amd64 from GOAMD64=v3 upwards, where FMA
// stops being an optional extension. Written the obvious way this function is
// therefore not one program but three, and which one it is depends on a build
// flag. An explicit float32 conversion is a rounding point fusion may not cross,
// so these conversions are what make it one program again.
//
// The last line needs the barrier as much as the others do: ampx is amp*x, so
// newIm - ampx is a multiply followed by a subtract and contracts to an FMSUB
// just as readily. That one is the easiest of the five to miss.
//
// This is what lets the reference be an oracle rather than an approximation.
// The SSE2 kernel is bit-identical to it, and NEON and AVX2 are held to a bound
// against it; both claims are worth nothing if the reference itself changes
// arithmetic when someone passes GOAMD64=v3. See TestPortableKernelDoesNotFuse.
//
// It takes scalars rather than the five rotor arrays for an unglamorous reason:
// the conversions cost gc's inliner five points each, and a version that also
// did its own indexing came to 98 against a budget of 80. It stopped being
// inlined, every lane started paying a real call with five slice headers on the
// stack, and the portable kernel got three times slower -- a far worse
// regression than the fusion it fixes. Check `-gcflags=-m` after touching this.
func advanceRotor(reVal, imVal, cosVal, sinVal, ampVal, x float32) (newRe, newIm, out float32) {
	ampx := float32(ampVal * x)
	reSin := float32(reVal * sinVal)
	imCos := float32(imVal * cosVal)
	reCos := float32(reVal * cosVal)
	imSin := float32(imVal * sinVal)

	newIm = (ampx + reSin) + imCos
	newRe = reCos - imSin

	return newRe, newIm, newIm - ampx
}

// reduceLanesGeneric collapses the accumulator into one sample per frame. The
// pairwise tree order is fixed so every backend sums in the same order.
//
// Nothing here needs the anti-contraction conversions advanceRotor carries: three
// additions with no product between them give the compiler nothing to fuse.
// Same for the accumulation in processRotorBlocksGeneric above. Both were
// audited for it, and the audit is worth writing down, because the next person
// to add a term here has to keep it true.
func reduceLanesGeneric(acc, output []float32) {
	for i := range output {
		lane := acc[i*accLanes : i*accLanes+accLanes : i*accLanes+accLanes]

		output[i] = (lane[0] + lane[1]) + (lane[2] + lane[3])
	}
}
