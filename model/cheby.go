package model

// chebyshevFastGains is the gain count the AVX2 kernel is specialised for. It
// is also the count every v1 preset carries, so the vectorised path covers the
// common case while the general recurrence covers everything else.
const chebyshevFastGains = 4

// processChebyshevBlock shapes a whole block of samples.
//
// Everything the vectorised kernel does not cover -- the tail of a block whose
// length is not a multiple of the vector width, every block on a machine
// without AVX2, and any gain count other than chebyshevFastGains -- goes
// through chebyshevScalar. There is one definition of what the shaper computes,
// so the same input yields the same output regardless of which path a sample
// took.
func processChebyshevBlock(input, output, gains []float32) {
	shaped := 0

	if len(gains) == chebyshevFastGains {
		shaped = processChebyshev4AVX2(input, output, (*[chebyshevFastGains]float32)(gains))
	}

	for i := shaped; i < len(input); i++ {
		output[i] = chebyshevScalar(input[i], gains)
	}
}

// chebyshevScalar evaluates the waveshaper for one sample: the sum over k of
// gains[k] * T_(k+1)(x), with x clamped to the interval the Chebyshev
// polynomials are defined on.
//
// The arithmetic mirrors the AVX2 kernel instruction for instruction. It is
// float32 throughout, the recurrence is evaluated as 2*(x*T_k) - T_(k-1), and
// the terms accumulate front to back. Anything else and a block's tail would
// not join up seamlessly with its vectorised body.
func chebyshevScalar(input float32, gains []float32) float32 {
	if len(gains) == 0 {
		return input
	}

	x := input
	if x < -1 {
		x = -1
	}

	if x > 1 {
		x = 1
	}

	prevPrevTerm := float32(1)
	prevTerm := x
	out := gains[0] * prevTerm

	for i := 1; i < len(gains); i++ {
		nextTerm := 2*(x*prevTerm) - prevPrevTerm
		out += gains[i] * nextTerm
		prevPrevTerm, prevTerm = prevTerm, nextTerm
	}

	return out
}
