//go:build amd64

package optimizer

import "github.com/cwbudde/algo-glockenspiel/internal/cpufeat"

// avx2RMSErrorBlock is the number of float32 lanes one YMM register holds.
const avx2RMSErrorBlock = 8

// avx2RMSErrorMinLen is the length from which the vector kernel is worth its
// call overhead. Re-measured kernel-to-kernel on an i7-1255U (medians of
// -benchtime 2s -count 7; the box was neither quiesced nor pinned to a
// performance governor, so read the ratios rather than the absolute
// nanoseconds): at 8 samples the two paths are level (3.8 ns vs 4.1 ns), at 16
// the kernel is already ~2.2x ahead (4.2 ns vs 9.3 ns), and the gap keeps
// widening -- 32 samples 5.9 ns vs 19.5 ns, 64 samples 10.3 ns vs 37.5 ns,
// 4096 samples 539 ns vs 949 ns. The threshold of 16 therefore still sits
// exactly where the curves cross; the older threshold of 32 was an unmeasured
// guess that left 16..31 on the slow path.
//
// BenchmarkSquaredDiffSum measures the whole dispatch, so its short-slice
// numbers sit well above the kernel-to-kernel ones: at 8 samples the dispatch
// arm costs 11.3 ns against the generic arm's 2.3 ns. That gap is plain call
// overhead, not lock traffic. squaredDiffSumGeneric inlines into its own
// benchmark, while squaredDiffSum does not inline and cpufeat.Detect cannot
// either -- its cold path carries a defer -- so the dispatch arm pays two
// out-of-line calls the generic arm does not.
//
// Detect's own work is a single atomic pointer load: 7.9 ns/op against
// 7.2 ns/op for a //go:noinline function that just returns a zero Features,
// measured back to back on the same box -- under a nanosecond above the bare
// call. An earlier revision of this comment blamed "~20 ns spent on two mutex
// acquisitions"; that described Detect before it was rewritten around
// atomic.Pointer, and since cpufeat warms the cache from its own init not even
// the first caller takes the lock. Either way it is a fixed cost that this
// threshold cannot influence.
const avx2RMSErrorMinLen = 2 * avx2RMSErrorBlock

func squaredDiffSum(synth, ref []float32) float64 {
	if len(ref) < len(synth) {
		synth = synth[:len(ref)]
	}

	if cpufeat.Detect().HasAVX2 && len(synth) >= avx2RMSErrorMinLen {
		mainCount := len(synth) &^ (avx2RMSErrorBlock - 1)
		sum := sumSquaredDiffAVX2(synth[:mainCount], ref[:mainCount])

		return sum + squaredDiffSumGeneric(synth[mainCount:], ref[mainCount:len(synth)])
	}

	return squaredDiffSumGeneric(synth, ref[:len(synth)])
}

// sumSquaredDiffAVX2 reads len(synth) elements from both slices. The bounds
// check here is what makes that safe; the assembly cannot perform one.
func sumSquaredDiffAVX2(synth, ref []float32) float64 {
	if len(synth) == 0 {
		return 0
	}

	_ = ref[len(synth)-1]

	return sumSquaredDiffAVX2Asm(&synth[0], &ref[0], len(synth))
}

//go:noescape
func sumSquaredDiffAVX2Asm(synth, ref *float32, count int) float64
