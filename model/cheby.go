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

	// The polynomial sum is not zero at zero, so a silent input leaves the
	// shaper holding a constant. Removing it here rather than inside the
	// recurrence keeps body and tail on the same footing: both have already
	// been rounded to float32 and written, and both lose the same constant.
	if offset := chebyshevZeroOffset(gains); offset != 0 {
		for i := range input {
			output[i] -= offset
		}
	}
}

// chebyshevZeroOffset is what the polynomial sum evaluates to at zero.
//
// T_(k+1) is odd for even k and even for odd k, so the odd-indexed gains --
// those weighting T_2, T_4 and so on -- contribute at x = 0 and the rest do
// not. For the shipped preset's [1.0, 0.5, 0.3, 0.2] it is exactly -0.3.
//
// Left in, that constant is not a cosmetic offset. The default preset places
// the shaper ahead of the oscillator bank, so it becomes a DC excitation that
// never stops: every rotor settles at a steady state and the note sustains for
// its whole length instead of decaying. Measured on the shipped preset before
// this was subtracted, the bar sat at an unchanging RMS of 0.1289 from 0.4 s to
// 3.8 s, and the auto-stop never fired because nothing ever went quiet. The
// legacy reference render the port is checked against decays into silence
// within 0.56 s, so the sustain was never the intended sound.
//
// It is evaluated per block rather than cached with the gains because it costs
// one pass over len(gains) -- four, for every v1 preset -- against a block of
// hundreds of samples, and a cached copy is one more thing that can go stale
// when a bar is retuned in place.
func chebyshevZeroOffset(gains []float32) float32 {
	if len(gains) == 0 {
		return 0
	}

	// The recurrence with x = 0 collapses to T_(k+1) = -T_(k-1), so the terms
	// run 0, -1, 0, 1, 0, -1 and every one of them is exact. What is not free
	// to be reordered is the sum: it accumulates front to back, the same way
	// chebyshevScalar does, so the two agree bit for bit on a gain set whose
	// partial sums do not round the same way in a different order.
	prevPrevTerm := float32(1)
	prevTerm := float32(0)
	out := float32(0)

	for i := 1; i < len(gains); i++ {
		nextTerm := -prevPrevTerm
		out += float32(gains[i] * nextTerm)
		prevPrevTerm, prevTerm = prevTerm, nextTerm
	}

	return out
}

// chebyshevScalar evaluates the polynomial sum for one sample: the sum over k
// of gains[k] * T_(k+1)(x), with x clamped to the interval the Chebyshev
// polynomials are defined on.
//
// This is the sum alone. The shaper is this minus chebyshevZeroOffset, and
// processChebyshevBlock is where the two meet -- the vectorised body cannot
// subtract the constant inside the recurrence, so neither does the tail.
//
// The arithmetic mirrors the AVX2 kernel instruction for instruction. It is
// float32 throughout, the recurrence is evaluated as 2*(x*T_k) - T_(k-1), and
// the terms accumulate front to back. Anything else and a block's tail would
// not join up seamlessly with its vectorised body.
//
// Every product is bound to its own float32 before it is added or subtracted.
// That is not decoration: the kernel multiplies and adds in separate,
// separately rounded instructions, while the compiler is free to contract a
// multiply followed by an add into one fused instruction that rounds only once
// -- and does exactly that from GOAMD64=v3 upwards, and on arm64 at any level.
// The Go specification makes an explicit float32 conversion a rounding point
// that fusion may not cross, so these conversions are what keeps the seam
// closed. See TestChebyshevBodyTailAndFallbackAgree.
func chebyshevScalar(input float32, gains []float32) float32 {
	if len(gains) == 0 {
		return input
	}

	x := clampChebyshevInput(input)

	prevPrevTerm := float32(1)
	prevTerm := x
	out := float32(gains[0] * prevTerm)

	for i := 1; i < len(gains); i++ {
		doubledProduct := float32(2 * float32(x*prevTerm))
		nextTerm := doubledProduct - prevPrevTerm
		out += float32(gains[i] * nextTerm)
		prevPrevTerm, prevTerm = prevTerm, nextTerm
	}

	return out
}

// clampChebyshevInput folds a sample into [-1, 1], the interval the Chebyshev
// polynomials are defined on.
//
// It reproduces the AVX2 kernel's VMAXPS/VMINPS semantics, including their NaN
// policy: those instructions return their second operand when either operand is
// NaN, so a NaN sample leaves the kernel as -1. Plain Go comparisons would
// carry the NaN through instead, and the two paths would disagree on exactly
// the input an audio buffer should never contain but sometimes does.
//
// Clamping is also the better answer of the two. A NaN that survives the shaper
// poisons the oscillator state it is fed into, and every sample after it; -1 is
// a click, which is audible, local, and recoverable.
func clampChebyshevInput(input float32) float32 {
	if input != input || input < -1 {
		return -1
	}

	if input > 1 {
		return 1
	}

	return input
}
