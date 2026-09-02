package model

import (
	"math"
	"testing"
)

func TestExcitationResponseIsUnityBelowTheCutoffAndFallsAbove(t *testing.T) {
	const sampleRate = 44100.0

	if got := ExcitationResponse(10000, 20, sampleRate); math.Abs(got-1) > 1e-3 {
		t.Fatalf("response far below the cutoff = %g, want 1", got)
	}

	// A Butterworth lowpass is -3 dB at its cutoff.
	if got := 20 * math.Log10(ExcitationResponse(1000, 1000, sampleRate)); math.Abs(got+3.01) > 0.1 {
		t.Fatalf("response at the cutoff = %.2f dB, want -3.01", got)
	}

	// And falls 12 dB per octave above it, a little steeper once the bilinear
	// warp towards Nyquist sets in.
	octave := 20 * math.Log10(ExcitationResponse(1000, 2000, sampleRate)/ExcitationResponse(1000, 4000, sampleRate))
	if math.Abs(octave-12) > 1.5 {
		t.Fatalf("slope two octaves above the cutoff = %.2f dB/octave, want 12", octave)
	}

	if got := ExcitationResponse(1000, 100, 0); got != 0 {
		t.Fatalf("response at a zero sample rate = %g, want 0", got)
	}
}
