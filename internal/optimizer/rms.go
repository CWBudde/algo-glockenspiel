package optimizer

import "math"

// AlignmentMode selects how a candidate signal is time-aligned to the reference
// before the error is computed.
type AlignmentMode int

const (
	// AlignNone compares sample i of the candidate with sample i of the
	// reference. Only correct when both signals start at the same instant.
	AlignNone AlignmentMode = iota

	// AlignOnsetCorrelation estimates the candidate's onset, derives a coarse
	// lag against the reference onset, and refines it by maximising the
	// normalised cross-correlation over a bounded lag range.
	AlignOnsetCorrelation
)

// GainMode selects how a candidate's level is matched to the reference.
type GainMode int

const (
	// GainNone compares the signals at their natural levels, so the absolute
	// amplitude of the model is part of what is being fitted.
	GainNone GainMode = iota

	// GainLeastSquares divides out the scalar gain that minimises the residual
	// before measuring it. Use it when the reference level is unknown (a
	// recording at an arbitrary mic gain); it makes absolute amplitude
	// parameters unidentifiable, which is why it is not the default.
	GainLeastSquares
)

const (
	// A 64-sample block at 44.1 kHz is 1.45 ms: short enough to localise an
	// attack, long enough that a single stray sample cannot trigger it.
	alignBlockSize = 64

	// Onset is the first block whose RMS reaches this fraction of the loudest
	// block. A struck bar rises far faster than this, so the exact value only
	// has to separate the attack from the noise floor.
	alignOnsetRatio = 0.1

	// The correlation window covers the attack, where the partials are all
	// still present and the waveform is most distinctive. 2048 samples is 46 ms
	// at 44.1 kHz - dozens of periods of the lowest partial, so the correlation
	// peak is unambiguous - while keeping the refinement affordable.
	alignWindowSamples = 2048

	// Refinement range around the onset-derived coarse lag. Onset estimates are
	// threshold-dependent, so a little slack on either side is needed; the cost
	// is linear in this range, so it is kept tight (2*64+1 correlations of
	// alignWindowSamples each, ~70 us against a ~1.3 ms render).
	alignFineRange = 64

	// Default bound on the absolute lag. 50 ms of slack absorbs the leading
	// silence of a trimmed recording without letting the search slide onto a
	// later partial of a different note.
	alignDefaultMaxLagSeconds = 0.050
)

// ComputeRMSError returns the RMS difference between signals after truncation
// to the shorter length.
//
// It performs no time or level alignment: sample i of synth is compared with
// sample i of ref. Use an AlignmentPlan when the reference may start at a
// different instant or sit at a different level.
func ComputeRMSError(synth, ref []float32) float64 {
	sampleCount := minInt(len(synth), len(ref))
	if sampleCount == 0 {
		return math.Inf(1)
	}

	sum := squaredDiffSum(synth[:sampleCount], ref[:sampleCount])

	return math.Sqrt(sum / float64(sampleCount))
}

// AlignmentPlan holds everything derived from the reference signal that time
// and level alignment need. It is immutable after construction and therefore
// safe to share between concurrently evaluating goroutines.
type AlignmentPlan struct {
	window     []float64
	windowNorm float64
	refOnset   int
	maxLag     int
	mode       AlignmentMode
	gain       GainMode
}

// NewAlignmentPlan precomputes the reference-side alignment data.
//
// Everything expensive that depends only on the reference (onset detection and
// the correlation window plus its norm) happens here, so that an objective
// called thousands of times only pays for the candidate-side work.
//
// maxLagSamples <= 0 selects a default derived from the sample rate.
func NewAlignmentPlan(ref []float32, sampleRate int, mode AlignmentMode, gain GainMode, maxLagSamples int) *AlignmentPlan {
	plan := &AlignmentPlan{mode: mode, gain: gain}
	if mode == AlignNone || len(ref) == 0 {
		return plan
	}

	if maxLagSamples <= 0 {
		maxLagSamples = int(alignDefaultMaxLagSeconds * float64(sampleRate))
	}

	// The lag must stay well inside the reference: shifting by more than a
	// quarter of it would leave too little overlap for the error to mean
	// anything.
	if limit := len(ref) / 4; maxLagSamples > limit {
		maxLagSamples = limit
	}

	if maxLagSamples < 0 {
		maxLagSamples = 0
	}

	plan.maxLag = maxLagSamples
	plan.refOnset = detectOnset(ref)

	windowLen := minInt(alignWindowSamples, len(ref)-plan.refOnset)
	if windowLen <= 0 {
		plan.mode = AlignNone
		return plan
	}

	plan.window = make([]float64, windowLen)

	var norm float64

	for i, sample := range ref[plan.refOnset : plan.refOnset+windowLen] {
		value := float64(sample)
		plan.window[i] = value
		norm += value * value
	}

	plan.windowNorm = math.Sqrt(norm)
	if plan.windowNorm == 0 {
		// A silent reference carries no timing information at all.
		plan.mode = AlignNone
	}

	return plan
}

// MaxLag returns the bound on the absolute lag the plan will consider.
func (p *AlignmentPlan) MaxLag() int {
	if p == nil {
		return 0
	}

	return p.maxLag
}

// BestLag returns the lag, in samples, by which the candidate trails the
// reference. A positive value means the candidate starts late.
//
// The search is two-stage: onset detection gives a coarse lag in one cheap pass
// over the head of the candidate, and a bounded normalised cross-correlation
// refines it to the sample. A full correlation over the whole lag range would
// cost more than rendering the candidate in the first place.
func (p *AlignmentPlan) BestLag(synth []float32) int {
	if p == nil || p.mode != AlignOnsetCorrelation || len(p.window) == 0 {
		return 0
	}

	// Only the first refOnset+maxLag samples can hold an onset that the lag
	// bound would accept, so there is no point scanning a two-second render.
	onsetLimit := minInt(len(synth), p.refOnset+p.maxLag+alignBlockSize)
	coarse := clampInt(detectOnset(synth[:onsetLimit])-p.refOnset, -p.maxLag, p.maxLag)

	windowLen := len(p.window)
	low := clampInt(coarse-alignFineRange, -p.maxLag, p.maxLag)
	high := clampInt(coarse+alignFineRange, -p.maxLag, p.maxLag)

	// Keep the correlation window inside the candidate.
	low = maxInt(low, -p.refOnset)
	high = minInt(high, len(synth)-windowLen-p.refOnset)

	if low > high {
		return 0
	}

	// The candidate energy under the window is maintained incrementally: only
	// one sample enters and one leaves per lag step.
	energy := 0.0

	for _, sample := range synth[p.refOnset+low : p.refOnset+low+windowLen] {
		value := float64(sample)
		energy += value * value
	}

	best := low
	bestScore := math.Inf(-1)

	for lag := low; lag <= high; lag++ {
		start := p.refOnset + lag

		dot := 0.0
		for i, coefficient := range p.window {
			dot += float64(synth[start+i]) * coefficient
		}

		if energy > 0 {
			if score := dot / (math.Sqrt(energy) * p.windowNorm); score > bestScore {
				bestScore = score
				best = lag
			}
		}

		if lag < high {
			leaving := float64(synth[start])
			entering := float64(synth[start+windowLen])
			energy += entering*entering - leaving*leaving

			if energy < 0 {
				energy = 0
			}
		}
	}

	if math.IsInf(bestScore, -1) {
		return 0
	}

	return best
}

// Align shifts the candidate against the reference by the best lag and returns
// the overlapping sub-slices. No copying is involved.
func (p *AlignmentPlan) Align(synth, ref []float32) ([]float32, []float32) {
	return alignSlices(synth, ref, p.BestLag(synth))
}

// RMSError returns the RMS error after time alignment and, if the plan asks for
// it, after dividing out the least-squares optimal gain.
func (p *AlignmentPlan) RMSError(synth, ref []float32) float64 {
	aligned, target := p.Align(synth, ref)

	return alignedRMSError(aligned, target, p.gainMode())
}

func (p *AlignmentPlan) gainMode() GainMode {
	if p == nil {
		return GainNone
	}

	return p.gain
}

func alignSlices(synth, ref []float32, lag int) ([]float32, []float32) {
	switch {
	case lag > 0:
		if lag >= len(synth) {
			return nil, nil
		}

		synth = synth[lag:]
	case lag < 0:
		if -lag >= len(ref) {
			return nil, nil
		}

		ref = ref[-lag:]
	}

	overlap := minInt(len(synth), len(ref))

	return synth[:overlap], ref[:overlap]
}

// alignedRMSError measures two already-aligned, equal-length signals.
//
// With GainLeastSquares the optimal gain g = <c,r>/<c,c> is not applied to a
// scaled copy; the residual is expanded algebraically as
// |r|^2 - <c,r>^2/<c,c>, which needs one pass and no allocation.
func alignedRMSError(cand, ref []float32, gain GainMode) float64 {
	count := minInt(len(cand), len(ref))
	if count == 0 {
		return math.Inf(1)
	}

	if gain == GainNone {
		return math.Sqrt(squaredDiffSum(cand[:count], ref[:count]) / float64(count))
	}

	candEnergy, cross, refEnergy := crossSums(cand[:count], ref[:count])
	if candEnergy <= 0 {
		return math.Sqrt(refEnergy / float64(count))
	}

	residual := refEnergy - cross*cross/candEnergy
	if residual < 0 {
		residual = 0
	}

	return math.Sqrt(residual / float64(count))
}

// crossSums returns <c,c>, <c,r> and <r,r> in a single pass.
func crossSums(cand, ref []float32) (float64, float64, float64) {
	var candEnergy, cross, refEnergy float64

	for i := range cand {
		candValue := float64(cand[i])
		refValue := float64(ref[i])
		candEnergy += candValue * candValue
		cross += candValue * refValue
		refEnergy += refValue * refValue
	}

	return candEnergy, cross, refEnergy
}

// OptimalGain returns the scalar that minimises |g*cand - ref| after alignment.
func OptimalGain(cand, ref []float32) float64 {
	count := minInt(len(cand), len(ref))
	if count == 0 {
		return 1
	}

	candEnergy, cross, _ := crossSums(cand[:count], ref[:count])
	if candEnergy <= 0 {
		return 1
	}

	return cross / candEnergy
}

// detectOnset returns the index of the first sample that belongs to the attack.
//
// Two cheap passes over block RMS values avoid allocating an envelope, which
// matters because this runs once per candidate evaluation.
func detectOnset(signal []float32) int {
	if len(signal) == 0 {
		return 0
	}

	peak := 0.0

	for start := 0; start < len(signal); start += alignBlockSize {
		if energy := blockMeanSquare(signal, start); energy > peak {
			peak = energy
		}
	}

	if peak <= 0 {
		return 0
	}

	threshold := peak * alignOnsetRatio * alignOnsetRatio
	sampleThreshold := math.Sqrt(peak) * alignOnsetRatio

	for start := 0; start < len(signal); start += alignBlockSize {
		if blockMeanSquare(signal, start) < threshold {
			continue
		}

		end := minInt(start+alignBlockSize, len(signal))
		for i := start; i < end; i++ {
			if math.Abs(float64(signal[i])) >= sampleThreshold {
				return i
			}
		}

		return start
	}

	return 0
}

func blockMeanSquare(signal []float32, start int) float64 {
	end := minInt(start+alignBlockSize, len(signal))
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

func squaredDiffSumGeneric(synth, ref []float32) float64 {
	sum := 0.0

	for i := range synth {
		d := float64(synth[i] - ref[i])
		sum += d * d
	}

	return sum
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}

	if value > high {
		return high
	}

	return value
}
