//go:build amd64

package optimizer

import "github.com/cwbudde/glockenspiel/internal/cpufeat"

// avx2RMSErrorBlock is the number of float32 lanes one YMM register holds.
const avx2RMSErrorBlock = 8

// avx2RMSErrorMinLen is the length from which the vector kernel is worth its
// call overhead. Measured kernel-to-kernel on an i7-1255U: at 8 samples the two
// paths are level (2 ns each), at 16 the kernel is already ~1.5x ahead (2 ns vs
// 3 ns) and the gap keeps widening (4096 samples: 518 ns vs 950 ns). The
// previous threshold of 32 was an unmeasured guess that left 16..31 on the slow
// path.
//
// Note that BenchmarkSquaredDiffSum measures the whole dispatch, which is
// dominated for short slices by the ~20 ns cpufeat.Detect() spends on two mutex
// acquisitions - a fixed cost this threshold cannot influence.
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
