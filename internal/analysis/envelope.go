package analysis

import "math"

const (
	// envelopeHopSeconds is the spacing of the broadband envelope the strike
	// end is read from. 10 ms resolves a second strike to well within the
	// alignment slack of a fit and averages over hundreds of samples, so the
	// noise floor reads flat rather than jittering by a few dB.
	envelopeHopSeconds = 0.010

	// envelopeSpanSeconds is the window each envelope point averages over.
	// Longer than the hop, so a single beat null between two close partials
	// does not read as the tail bottoming out.
	envelopeSpanSeconds = 0.050

	// secondOnsetRiseDB is how far the envelope has to climb back above the
	// quietest it has been since the strike for a new event to be declared.
	// A decaying bar with beating partials wobbles by a dB or two; a second
	// strike, a knock or a voice climbs far further.
	secondOnsetRiseDB = 6.0

	// floorHoldSeconds is how long the envelope has to stop falling for the
	// tail to count as having reached the floor.
	floorHoldSeconds = 0.5

	// floorSlackDB is how much the envelope may sit above its running minimum
	// while still counting as not falling.
	floorSlackDB = 1.0

	// attackSearchSeconds is how far past the onset the strike's peak is
	// looked for. The onset is by definition within 20 dB of the loudest
	// block, so the peak is at most a few tens of milliseconds away; looking
	// further would let a second strike of equal strength become the peak.
	attackSearchSeconds = 0.2

	// silenceDB is the floor for the log of a zero envelope.
	silenceDB = -200.0
)

// envelope is a broadband RMS envelope in dB with a fixed hop.
type envelope struct {
	// levelDB holds one value per hop.
	levelDB []float64
	// hop is the spacing in samples.
	hop int
}

// broadbandEnvelope measures the signal's RMS in dB every hop over a span
// centred on the hop, from start to the end of the signal.
func broadbandEnvelope(signal []float32, start, sampleRate int) envelope {
	hop := max(1, int(math.Round(envelopeHopSeconds*float64(sampleRate))))
	span := max(hop, int(math.Round(envelopeSpanSeconds*float64(sampleRate))))
	points := (len(signal) - start + hop - 1) / hop
	levels := make([]float64, 0, max(points, 0))

	for centre := start; centre < len(signal); centre += hop {
		blockStart := max(0, centre-span/2)
		mean := blockMeanSquare(signal, blockStart, span)
		levels = append(levels, powerDB(mean))
	}

	return envelope{levelDB: levels, hop: hop}
}

// strikeEnd finds the sample at which the strike that begins at onset stops
// being the only thing in the signal, and says what decided it.
//
// Two things end a strike short of the file. A second event -- the envelope
// climbing secondOnsetRiseDB above the quietest it has been since the peak --
// ends it just before the climb. Failing that, the tail decaying into the
// floor -- the envelope not undercutting its running minimum by more than
// floorSlackDB for floorHoldSeconds -- ends it where the falling stopped.
func strikeEnd(signal []float32, onset, sampleRate int) (int, EndRule) {
	env := broadbandEnvelope(signal, onset, sampleRate)
	if len(env.levelDB) == 0 {
		return len(signal), EndOfFile
	}

	peakIndex := 0
	attackPoints := min(len(env.levelDB), int(math.Round(attackSearchSeconds/envelopeHopSeconds))+1)

	for i, level := range env.levelDB[:attackPoints] {
		if level > env.levelDB[peakIndex] {
			peakIndex = i
		}
	}

	holdPoints := max(1, int(math.Round(floorHoldSeconds/envelopeHopSeconds)))
	runningMin := env.levelDB[peakIndex]

	// The anchor is the last point at which the tail was still clearly
	// falling: it moves only on a drop of floorSlackDB below itself, so a slow
	// decay keeps moving it while a flat floor leaves it behind.
	anchor := peakIndex
	anchorLevel := env.levelDB[peakIndex]

	for i := peakIndex + 1; i < len(env.levelDB); i++ {
		level := env.levelDB[i]

		if level >= runningMin+secondOnsetRiseDB {
			return onset + anchor*env.hop, EndSecondOnset
		}

		runningMin = math.Min(runningMin, level)

		if level < anchorLevel-floorSlackDB {
			anchor = i
			anchorLevel = level

			continue
		}

		if i-anchor >= holdPoints {
			return onset + anchor*env.hop, EndFloor
		}
	}

	return len(signal), EndOfFile
}

// powerDB converts a mean square to dB, with silence pinned at silenceDB.
func powerDB(meanSquare float64) float64 {
	if meanSquare <= 0 {
		return silenceDB
	}

	return 10 * math.Log10(meanSquare)
}

const (
	// slopeFitRangeDB and slopeFitSeconds bound the broadband decay fit the
	// same way the narrowband half-life fit is bounded: from the envelope's
	// peak down slopeFitRangeDB, or over slopeFitSeconds, whichever comes
	// first.
	slopeFitRangeDB = 30.0
	slopeFitSeconds = 1.0

	// slopeMinDropDB is the least the envelope has to fall for a slope to be
	// reported at all.
	slopeMinDropDB = 3.0
)

// DecaySlopeDBps is the broadband decay of a strike in dB per second: the
// least-squares slope of the RMS envelope from its peak down to where it has
// fallen slopeFitRangeDB, or over slopeFitSeconds, whichever comes first.
// The signal is taken to start at the strike. NaN when the envelope never
// falls slopeMinDropDB, or rises.
func DecaySlopeDBps(signal []float32, sampleRate int) float64 {
	if sampleRate <= 0 {
		return math.NaN()
	}

	env := broadbandEnvelope(signal, 0, sampleRate)
	if len(env.levelDB) == 0 {
		return math.NaN()
	}

	peak := 0
	attackPoints := min(len(env.levelDB), int(math.Round(attackSearchSeconds/envelopeHopSeconds))+1)

	for i, level := range env.levelDB[:attackPoints] {
		if level > env.levelDB[peak] {
			peak = i
		}
	}

	stop := env.levelDB[peak] - slopeFitRangeDB
	limit := min(len(env.levelDB), peak+1+int(math.Round(slopeFitSeconds/envelopeHopSeconds)))
	end := peak + 1

	for end < limit && env.levelDB[end] > stop {
		end++
	}

	if end-peak < 2 || env.levelDB[peak]-env.levelDB[end-1] < slopeMinDropDB {
		return math.NaN()
	}

	slope, _ := fitLine(env.levelDB[peak:end], float64(env.hop)/float64(sampleRate))
	if slope >= 0 {
		return math.NaN()
	}

	return slope
}
