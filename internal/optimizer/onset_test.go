package optimizer

import (
	"math"
	"testing"
)

// strikeWithClick builds a reference-shaped signal: a decaying low partial,
// optionally with a short burst of a high partial on top of its first few
// milliseconds. The burst is the mallet: brief, well above the partial, and
// gone before the envelope's second window ends.
func strikeWithClick(sampleRate, length int, lowHz, clickHz float64, clickAmplitude float64, clickMs float64) []float32 {
	signal := make([]float32, length)
	clickSamples := int(clickMs * float64(sampleRate) / 1000)

	for i := range signal {
		t := float64(i) / float64(sampleRate)
		value := 0.5 * math.Exp(-t/0.3) * math.Sin(2*math.Pi*lowHz*t)

		if i < clickSamples {
			decay := math.Exp(-float64(i) / float64(clickSamples) * 3)
			value += clickAmplitude * decay * math.Sin(2*math.Pi*clickHz*t)
		}

		signal[i] = float32(value)
	}

	return signal
}

// TestOnsetHearsAStrikeTheEnvelopeAndSpectrumMiss is the term's reason to
// exist, written as the case that made it: two signals whose only difference
// is the mallet's high-frequency burst over the first few milliseconds.
//
// The burst is short enough that the broadband envelope barely moves and small
// enough in total energy that averaging a spectrogram over the whole strike
// hides it. If either of those terms could already tell the two apart, the
// onset term would be redundant, so this asserts that they cannot and that the
// onset term can.
func TestOnsetHearsAStrikeTheEnvelopeAndSpectrumMiss(t *testing.T) {
	const (
		sampleRate = 48000
		length     = sampleRate / 2
	)

	withClick := strikeWithClick(sampleRate, length, 1000, 6000, 0.35, 8)
	withoutClick := strikeWithClick(sampleRate, length, 1000, 6000, 0, 8)

	reference := newCompositeReference(withClick, sampleRate, nil)
	plan := NewAlignmentPlan(withClick, sampleRate, AlignOnsetCorrelation, GainNone, 0)
	metrics := reference.measure(withoutClick, withClick, plan, nil)

	if math.IsNaN(metrics.OnsetDB) {
		t.Fatal("the onset term was not measured on a half-second strike")
	}

	// The two terms that were already there cannot see it. These bounds are
	// the measured behaviour, not aspirations: if a later change makes either
	// term hear the attack, this test should be revisited rather than relaxed.
	if metrics.EnvelopeDB > 3 {
		t.Fatalf("the envelope term separated the two strikes (%.2f dB); the onset term may no longer be needed", metrics.EnvelopeDB)
	}

	if metrics.OnsetDB <= metrics.EnvelopeDB {
		t.Fatalf("onset %.2f dB did not exceed envelope %.2f dB on a difference that is entirely in the attack",
			metrics.OnsetDB, metrics.EnvelopeDB)
	}

	if metrics.OnsetDB < 10 {
		t.Fatalf("onset term is %.2f dB for a missing 6 kHz burst, want it to read as a large error", metrics.OnsetDB)
	}
}

// TestOnsetIsBlindToLevel pins that the term compares shape, not loudness: the
// solved gain is applied before the bands are compared, so the same strike at
// a different level costs nothing. Without that, the term would just restate
// the level the objective already solves in closed form.
func TestOnsetIsBlindToLevel(t *testing.T) {
	const (
		sampleRate = 48000
		length     = sampleRate / 2
	)

	reference := strikeWithClick(sampleRate, length, 1000, 6000, 0.35, 8)
	quiet := make([]float32, len(reference))

	for i, sample := range reference {
		quiet[i] = sample / 8
	}

	composite := newCompositeReference(reference, sampleRate, nil)
	plan := NewAlignmentPlan(reference, sampleRate, AlignOnsetCorrelation, GainNone, 0)
	metrics := composite.measure(quiet, reference, plan, nil)

	if metrics.OnsetDB > 0.5 {
		t.Fatalf("the same strike eighteen decibels down scored %.2f dB on the onset term, want it near zero", metrics.OnsetDB)
	}
}

// TestOnsetBandsPartitionTheBins pins the walk in onsetBands: bands ascend,
// never share a bin, and never claim a bin twice. A band that repeated a bin
// would count the low end many times over, which is the bug the walk exists
// to prevent.
func TestOnsetBandsPartitionTheBins(t *testing.T) {
	for _, sampleRate := range []int{44100, 48000, 96000} {
		bands := onsetBands(sampleRate)
		if len(bands) < 8 {
			t.Fatalf("%d Hz: %d bands, want enough to separate the mallet band from the partials", sampleRate, len(bands))
		}

		previous := -1

		for i, band := range bands {
			if band.firstBin > band.lastBin {
				t.Fatalf("%d Hz: band %d is empty (%d..%d)", sampleRate, i, band.firstBin, band.lastBin)
			}

			if band.firstBin <= previous {
				t.Fatalf("%d Hz: band %d starts at bin %d, which band %d already claimed", sampleRate, i, band.firstBin, i-1)
			}

			if band.lastBin > onsetFrameSize/2 {
				t.Fatalf("%d Hz: band %d ends at bin %d, past the frame's last bin %d", sampleRate, i, band.lastBin, onsetFrameSize/2)
			}

			previous = band.lastBin
		}
	}
}

// TestOnsetIsUnmeasuredOnAStrikeTooShortToHold is the NaN path: a reference
// shorter than the frame has no attack spectrum, and the term has to say so
// rather than report a zero the search would read as perfect.
func TestOnsetIsUnmeasuredOnAStrikeTooShortToHold(t *testing.T) {
	const sampleRate = 48000

	short := strikeWithClick(sampleRate, onsetFrameSize/2, 1000, 6000, 0.35, 2)

	if levels := onsetLevels(short, onsetBands(sampleRate), 1); levels != nil {
		t.Fatalf("a strike of %d samples reported %d band levels, want none", len(short), len(levels))
	}
}
