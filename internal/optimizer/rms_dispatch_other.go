//go:build !amd64

package optimizer

func squaredDiffSum(synth, ref []float32) float64 {
	if len(ref) < len(synth) {
		synth = synth[:len(ref)]
	}

	return squaredDiffSumGeneric(synth, ref[:len(synth)])
}
