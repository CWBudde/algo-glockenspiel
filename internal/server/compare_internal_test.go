package server

// This file is package server, not server_test: the shared-floor rule is a
// property of the reduction itself, and pinning it here says it deterministically
// rather than hoping a particular fit happens to render quietly enough for it
// to show at the HTTP surface.

import (
	"math"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// tone is a decaying partial at a level, which is enough for a spectrogram to
// derive a floor from: a quiet signal's floor is its own peak less the
// dynamic range, so two levels give two floors.
func tone(sampleRate, sampleCount int, level float64) []float32 {
	signal := make([]float32, sampleCount)

	for i := range signal {
		seconds := float64(i) / float64(sampleRate)
		signal[i] = float32(level * math.Sin(2*math.Pi*880*seconds) * math.Exp(-seconds/0.3))
	}

	return signal
}

// The objective clamps the candidate and the reference alike to the
// reference's floor. The reduction has to do the same, or the render's picture
// shows detail the score counted as nothing -- and the levels here are exactly
// the case that happens in: a peak-normalised reference beside a render the
// fit left forty decibels quieter.
func TestTheReductionPaintsEverySideAgainstTheFloorItIsGiven(t *testing.T) {
	const (
		sampleRate  = 44100
		sampleCount = 44100
		frames      = 8
	)

	loud := optimizer.ComputeSpectrogram(tone(sampleRate, sampleCount, 1), sampleRate, optimizer.SpectrogramCoarseFrameSize)
	quiet := optimizer.ComputeSpectrogram(tone(sampleRate, sampleCount, 0.01), sampleRate, optimizer.SpectrogramCoarseFrameSize)

	if loud == nil || quiet == nil {
		t.Fatal("one of the two signals produced no transform")
	}

	// The premise: two signals at two levels have two floors of their own, so
	// "each side keeps its own" and "both take the reference's" are different
	// pictures rather than the same one written twice.
	if quiet.FloorDB >= loud.FloorDB {
		t.Fatalf("the quiet signal's floor is %v and the loud one's %v, so this proves nothing",
			quiet.FloorDB, loud.FloorDB)
	}

	shared := reduceSpectrogram(quiet, sampleRate, frames, loud.FloorDB)
	if shared == nil {
		t.Fatal("the reduction produced nothing")
	}

	for column, row := range shared.DB {
		for bin, value := range row {
			if value < roundTo(loud.FloorDB, compareDBDigits) {
				t.Fatalf("column %d bin %d is %v, below the reference floor %v",
					column, bin, value, loud.FloorDB)
			}
		}
	}

	// And the picture the old rule would have drawn does hold values under
	// that floor, which is what made it a picture the score disagrees with.
	own := reduceSpectrogram(quiet, sampleRate, frames, quiet.FloorDB)

	below := 0

	for _, row := range own.DB {
		for _, value := range row {
			if value < loud.FloorDB {
				below++
			}
		}
	}

	if below == 0 {
		t.Fatal("reducing against its own floor held nothing under the reference floor, so the two rules agree here")
	}
}
