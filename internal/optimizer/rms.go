package optimizer

import "math"

// ComputeRMSError returns the RMS difference between signals after truncation to the shorter length.
func ComputeRMSError(synth, ref []float32) float64 {
	sampleCount := minInt(len(synth), len(ref))
	if sampleCount == 0 {
		return math.Inf(1)
	}

	sum := squaredDiffSum(synth[:sampleCount], ref[:sampleCount])
	return math.Sqrt(sum / float64(sampleCount))
}

func squaredDiffSumGeneric(synth, ref []float32) float64 {
	sum := 0.0
	for i := range synth {
		d := float64(synth[i] - ref[i])
		sum += d * d
	}
	return sum
}
