package optimizer

import (
	"math"
	"sort"

	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
)

const (
	// partialMatchCents is how far a model partial may sit from a reference
	// partial and still be the one that plays it. A semitone: beyond that no
	// listener would call it the same partial, and the search is better told
	// the partial is missing than that it is very out of tune.
	partialMatchCents = 100.0

	// envelopeFirstWindowSeconds and envelopeWindowRatio lay out the
	// log-spaced windows of the envelope term: the first ends 5 ms after the
	// strike and each is a quarter longer than the last, so the attack is
	// resolved at the millisecond and a two-second tail costs thirty windows.
	envelopeFirstWindowSeconds = 0.005
	envelopeWindowRatio        = 1.25

	// envelopeFloorDBFS is the lowest the envelope floor may sit. A synthetic
	// reference decays to numerical silence, and comparing a candidate's -90 dB
	// against that would be a hundred-decibel error over nothing.
	envelopeFloorDBFS = -100.0

	// analysisMinFrameSize is the smallest window the reference's partials are
	// measured with when the reference is too short for the default.
	analysisMinFrameSize = 256
)

// referencePartial is one partial of the reference as the partial term sees
// it: pitch, level at the strike relative to the strongest, half-life, and
// the weight its level above the floor gives it.
type referencePartial struct {
	frequencyHz float64
	levelDB     float64
	halfLifeMs  float64
	weight      float64
}

// modelPartial is one component a candidate's parameters say it produces.
type modelPartial struct {
	frequencyHz float64
	levelDB     float64
	halfLifeMs  float64
}

// compositeReference is everything the composite terms precompute from the
// reference once, so an evaluation only pays for the candidate's side.
type compositeReference struct {
	sampleRate int

	// onset is where the strike starts in the reference. A reference that
	// went through analysis.LoadReference starts at its strike.
	onset int

	fine   *spectrogram
	coarse *spectrogram

	// edges are the envelope windows' sample offsets from the onset, with the
	// last edge inside the reference. levels are the reference's window
	// levels in dB, and floorDB the level both envelopes are clamped to.
	edges   []int
	levels  []float64
	floorDB float64

	slopeDBps float64

	partials     []referencePartial
	partialFloor float64
	weightTotal  float64
}

// newCompositeReference measures the reference for every term. A nil
// measurement means the partials are measured here, with a window that fits
// the reference.
func newCompositeReference(reference []float32, sampleRate int, measurement *analysis.Measurement) *compositeReference {
	onset := analysis.Onset(reference)
	strike := reference[onset:]

	composite := &compositeReference{
		sampleRate: sampleRate,
		onset:      onset,
		fine:       newSpectrogram(reference, sampleRate, spectralFineFrameSize),
		coarse:     newSpectrogram(reference, sampleRate, spectralFrameSize),
		slopeDBps:  analysis.DecaySlopeDBps(strike, sampleRate),
	}

	composite.edges = envelopeEdges(len(strike), sampleRate)
	composite.levels = envelopeLevels(strike, composite.edges)
	composite.floorDB = envelopeFloorDBFS

	if len(composite.levels) > 0 {
		lowest := composite.levels[0]
		for _, level := range composite.levels {
			lowest = math.Min(lowest, level)
		}

		composite.floorDB = math.Max(envelopeFloorDBFS, lowest)
	}

	if measurement == nil {
		measurement = measurePartials(strike, sampleRate)
	}

	composite.partialFloor = analysis.DefaultMinLevelDB
	if measurement != nil {
		composite.partialFloor = measurement.Options.MinLevelDB
		composite.partials = referencePartials(measurement.Partials, composite.partialFloor)
	}

	for _, partial := range composite.partials {
		composite.weightTotal += partial.weight
	}

	return composite
}

// measurePartials measures the reference's partials with the largest
// power-of-two window up to the default that the reference can hold. Nil
// when the reference is shorter than the smallest window.
func measurePartials(strike []float32, sampleRate int) *analysis.Measurement {
	frameSize := analysis.DefaultFrameSize
	for frameSize > len(strike) {
		frameSize /= 2
	}

	if frameSize < analysisMinFrameSize {
		return nil
	}

	measurement, err := analysis.Measure(strike, sampleRate, analysis.PartialOptions{FrameSize: frameSize})
	if err != nil {
		return nil
	}

	return measurement
}

// referencePartials converts measured partials into the term's form: levels
// are the attack levels -- the decay line at the strike, which is what a mode
// amplitude has to reach -- relative to the strongest, with the averaged
// level standing in where no decay could be fitted.
func referencePartials(partials []analysis.Partial, floorDB float64) []referencePartial {
	if len(partials) == 0 {
		return nil
	}

	converted := make([]referencePartial, len(partials))
	strongest := math.Inf(-1)

	for i, partial := range partials {
		level := partial.AttackDB
		if math.IsNaN(level) || math.IsInf(level, 0) {
			level = partial.AmplitudeDB
		}

		converted[i] = referencePartial{
			frequencyHz: partial.FrequencyHz,
			levelDB:     level,
			halfLifeMs:  partial.HalfLifeMs,
		}
		strongest = math.Max(strongest, level)
	}

	for i := range converted {
		converted[i].levelDB -= strongest
		converted[i].weight = math.Max(0, converted[i].levelDB-floorDB)
	}

	return converted
}

// envelopeEdges lays out log-spaced window edges from the strike: zero, then
// the first window's end, then each a ratio longer, until the next edge
// would pass the end of the signal.
func envelopeEdges(length, sampleRate int) []int {
	if length <= 0 {
		return nil
	}

	edges := []int{0}
	edge := envelopeFirstWindowSeconds * float64(sampleRate)

	for {
		next := int(math.Round(edge))
		if next > length {
			break
		}

		if next > edges[len(edges)-1] {
			edges = append(edges, next)
		}

		edge *= envelopeWindowRatio
	}

	if edges[len(edges)-1] < length && length-edges[len(edges)-1] >= 1 {
		edges = append(edges, length)
	}

	return edges
}

// envelopeLevels is the RMS level in dB of each window between the edges.
// Windows beyond the signal are left out, so the result may be shorter than
// the edge count minus one.
func envelopeLevels(signal []float32, edges []int) []float64 {
	var levels []float64

	for i := 0; i+1 < len(edges); i++ {
		start, end := edges[i], edges[i+1]
		if end > len(signal) {
			break
		}

		var energy float64

		for _, sample := range signal[start:end] {
			value := float64(sample)
			energy += value * value
		}

		levels = append(levels, powerDBFS(energy/float64(end-start)))
	}

	return levels
}

// powerDBFS converts a mean square to dB, with silence at the envelope floor.
func powerDBFS(meanSquare float64) float64 {
	if meanSquare <= 0 {
		return envelopeFloorDBFS
	}

	return math.Max(envelopeFloorDBFS, 10*math.Log10(meanSquare))
}

// measure takes every term for an already rendered candidate.
func (r *compositeReference) measure(rendered, reference []float32, plan *AlignmentPlan, model []modelPartial) Metrics {
	metrics := unmeasuredMetrics()

	lag := plan.BestLag(rendered)
	aligned, target := alignSlices(rendered, reference, lag)
	metrics.Lag = lag
	metrics.Overlap = minInt(len(aligned), len(target))

	if metrics.Overlap == 0 {
		return metrics
	}

	// Level gain first: the ratio of the RMS levels over the overlap, in
	// closed form. The spectral and envelope terms compare the candidate at
	// this gain, so a candidate that has the right shape at the wrong level
	// pays nothing for the level, and every amplitude the model carries is
	// relative from here on.
	candEnergy, cross, refEnergy := crossSums(aligned, target)
	metrics.GainDB = levelGainDB(candEnergy, refEnergy)
	metrics.WaveformGainDB = math.NaN()

	if candEnergy > 0 && cross > 0 {
		metrics.WaveformGainDB = 20 * math.Log10(cross/candEnergy)
	}

	if refEnergy > 0 {
		residual := refEnergy
		if candEnergy > 0 {
			residual = math.Max(0, refEnergy-cross*cross/candEnergy)
		}

		metrics.Waveform = math.Sqrt(residual / refEnergy)
	}

	gainDB := metrics.GainDB
	if math.IsInf(gainDB, 0) || math.IsNaN(gainDB) {
		gainDB = 0
	}

	// The reference frames were taken from the reference as given, which is
	// the aligned reference unless the alignment dropped its head.
	retake := lag < 0
	metrics.SpectralFineDB = r.fine.errorDB(aligned, target, retake, gainDB)
	metrics.SpectralCoarseDB = r.coarse.errorDB(aligned, target, retake, gainDB)

	// The envelope and the slope are taken from the strike on. With a
	// negative lag the reference has lost -lag samples of its head, so the
	// onset moves up by that much.
	onset := r.onset
	if lag < 0 {
		onset = maxInt(0, onset+lag)
	}

	if onset < metrics.Overlap {
		candStrike := aligned[onset:metrics.Overlap]
		metrics.EnvelopeDB = r.envelopeError(candStrike, gainDB)

		if slope := analysis.DecaySlopeDBps(candStrike, r.sampleRate); !math.IsNaN(slope) && !math.IsNaN(r.slopeDBps) {
			metrics.DecaySlopeDBps = math.Abs(slope - r.slopeDBps)
		}
	}

	r.partialTerms(&metrics, model)

	return metrics
}

// levelGainDB is the gain that matches the candidate's RMS to the reference's.
func levelGainDB(candEnergy, refEnergy float64) float64 {
	if candEnergy <= 0 || refEnergy <= 0 {
		return math.NaN()
	}

	return 10 * math.Log10(refEnergy/candEnergy)
}

// envelopeError is the RMS dB difference between the candidate's envelope at
// the gain and the reference's, over the windows both cover, with both
// clamped to the reference's floor.
func (r *compositeReference) envelopeError(candidate []float32, gainDB float64) float64 {
	levels := envelopeLevels(candidate, r.edges)
	count := minInt(len(levels), len(r.levels))

	if count == 0 {
		return math.NaN()
	}

	var sum float64

	for i := range count {
		delta := math.Max(levels[i]+gainDB, r.floorDB) - math.Max(r.levels[i], r.floorDB)
		sum += delta * delta
	}

	return math.Sqrt(sum / float64(count))
}

// partialTerms matches the model's partials to the reference's and fills in
// the five partial terms.
//
// Each reference partial, strongest first, takes the nearest unclaimed model
// partial within partialMatchCents. The matched pairs give the pitch, level
// and decay errors, weighted by the reference partial's level above the
// floor; the level offset between the two lists is solved as the weighted
// mean difference before the level error is taken. Whatever the reference
// has left over is missing; whatever the model has left over, and sits above
// the floor in the reference's scale, is extra.
func (r *compositeReference) partialTerms(metrics *Metrics, model []modelPartial) {
	metrics.ReferencePartials = len(r.partials)
	metrics.ModelPartials = len(model)

	if len(r.partials) == 0 || r.weightTotal <= 0 {
		return
	}

	claimed := make([]bool, len(model))
	matches := make([]int, len(r.partials))

	for i := range matches {
		matches[i] = -1
	}

	order := make([]int, len(r.partials))
	for i := range order {
		order[i] = i
	}

	sort.SliceStable(order, func(a, b int) bool {
		return r.partials[order[a]].weight > r.partials[order[b]].weight
	})

	for _, i := range order {
		partial := r.partials[i]
		best, bestCents := -1, partialMatchCents

		for j, candidate := range model {
			if claimed[j] || candidate.frequencyHz <= 0 {
				continue
			}

			cents := math.Abs(1200 * math.Log2(candidate.frequencyHz/partial.frequencyHz))
			if cents <= bestCents {
				best, bestCents = j, cents
			}
		}

		if best >= 0 {
			claimed[best] = true
			matches[i] = best
		}
	}

	var (
		matchedWeight, offsetSum float64
		missing                  float64
	)

	for i, partial := range r.partials {
		if matches[i] < 0 {
			missing += partial.weight

			continue
		}

		metrics.Matched++
		matchedWeight += partial.weight
		offsetSum += partial.weight * (model[matches[i]].levelDB - partial.levelDB)
	}

	metrics.PartialMissing = missing / r.weightTotal

	offset := 0.0
	if matchedWeight > 0 {
		offset = offsetSum / matchedWeight
	}

	var extra float64

	for j, candidate := range model {
		if claimed[j] {
			continue
		}

		extra += math.Max(0, candidate.levelDB-offset-r.partialFloor)
	}

	metrics.PartialExtra = extra / r.weightTotal

	if matchedWeight <= 0 {
		return
	}

	var centsSum, levelSum, decaySum, decayWeight float64

	for i, partial := range r.partials {
		if matches[i] < 0 {
			continue
		}

		matched := model[matches[i]]
		cents := 1200 * math.Log2(matched.frequencyHz/partial.frequencyHz)
		level := matched.levelDB - offset - partial.levelDB

		centsSum += partial.weight * cents * cents
		levelSum += partial.weight * level * level

		if partial.halfLifeMs > 0 && matched.halfLifeMs > 0 && !math.IsNaN(partial.halfLifeMs) {
			octaves := math.Log2(matched.halfLifeMs / partial.halfLifeMs)
			decaySum += partial.weight * octaves * octaves
			decayWeight += partial.weight
		}
	}

	metrics.PartialCents = math.Sqrt(centsSum / matchedWeight)
	metrics.PartialLevelDB = math.Sqrt(levelSum / matchedWeight)

	if decayWeight > 0 {
		metrics.PartialDecayOctaves = math.Sqrt(decaySum / decayWeight)
	}
}
