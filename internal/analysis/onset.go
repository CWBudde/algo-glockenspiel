package analysis

import "math"

const (
	// OnsetBlockSize is the block the onset detector measures energy over. A
	// 64-sample block at 44.1 kHz is 1.45 ms: short enough to localise an
	// attack, long enough that a single stray sample cannot trigger it.
	OnsetBlockSize = 64

	// OnsetRatio is the fraction of the loudest block's RMS the first block of
	// the attack has to reach. A struck bar rises far faster than this, so the
	// exact value only has to separate the attack from the noise floor.
	OnsetRatio = 0.1
)

// Onset returns the index of the first sample that belongs to the attack.
//
// Two cheap passes over block RMS values avoid allocating an envelope, which
// matters because the optimizer runs this once per candidate evaluation. The
// first pass finds the loudest block; the second finds the first block within
// OnsetRatio of it and, inside that block, the first sample that reaches the
// same fraction of the peak sample level. Silence returns zero.
func Onset(signal []float32) int {
	if len(signal) == 0 {
		return 0
	}

	peak := 0.0

	for start := 0; start < len(signal); start += OnsetBlockSize {
		if energy := blockMeanSquare(signal, start, OnsetBlockSize); energy > peak {
			peak = energy
		}
	}

	if peak <= 0 {
		return 0
	}

	threshold := peak * OnsetRatio * OnsetRatio
	sampleThreshold := math.Sqrt(peak) * OnsetRatio

	for start := 0; start < len(signal); start += OnsetBlockSize {
		if blockMeanSquare(signal, start, OnsetBlockSize) < threshold {
			continue
		}

		end := min(start+OnsetBlockSize, len(signal))
		for i := start; i < end; i++ {
			if math.Abs(float64(signal[i])) >= sampleThreshold {
				return i
			}
		}

		return start
	}

	return 0
}

// blockMeanSquare is the mean square of the block of at most size samples
// starting at start, or zero past the end.
func blockMeanSquare(signal []float32, start, size int) float64 {
	end := min(start+size, len(signal))
	if end <= start {
		return 0
	}

	sum := 0.0

	for _, sample := range signal[start:end] {
		value := float64(sample)
		sum += value * value
	}

	return sum / float64(end-start)
}
