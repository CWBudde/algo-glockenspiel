//go:build !amd64

package optimizer

func squaredDiffSum(synth, ref []float32) float64 {
	return squaredDiffSumGeneric(synth, ref)
}
