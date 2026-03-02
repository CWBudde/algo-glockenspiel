//go:build amd64

package optimizer

import "github.com/cwbudde/glockenspiel/internal/cpufeat"

const avx2RMSErrorBlock = 8

func squaredDiffSum(synth, ref []float32) float64 {
	if cpufeat.Detect().HasAVX2 && len(synth) >= 32 {
		mainCount := len(synth) &^ (avx2RMSErrorBlock - 1)
		sum := sumSquaredDiffAVX2(synth[:mainCount], ref[:mainCount])
		return sum + squaredDiffSumGeneric(synth[mainCount:], ref[mainCount:])
	}
	return squaredDiffSumGeneric(synth, ref)
}

func sumSquaredDiffAVX2(synth, ref []float32) float64 {
	return sumSquaredDiffAVX2Asm(&synth[0], &ref[0], len(synth))
}

//go:noescape
func sumSquaredDiffAVX2Asm(synth, ref *float32, count int) float64
