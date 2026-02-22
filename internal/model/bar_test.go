package model

import (
	"math"
	"testing"
)

func TestBarSynthesizeProducesNonZeroOutput(t *testing.T) {
	params := validTestParams()
	bar, err := NewBar(&params, 48000)
	if err != nil {
		t.Fatalf("failed to create bar: %v", err)
	}

	out := bar.Synthesize(100, 2048)
	if len(out) != 2048 {
		t.Fatalf("unexpected output length: got %d", len(out))
	}

	if peakAbs(out) <= 0 {
		t.Fatal("expected non-zero output")
	}
}

func TestBarUpdateParams(t *testing.T) {
	params := validTestParams()
	params.InputMix = 0
	bar, err := NewBar(&params, 48000)
	if err != nil {
		t.Fatalf("failed to create bar: %v", err)
	}

	out1 := bar.Synthesize(100, 512)
	if peakAbs(out1) == 0 {
		t.Fatal("expected non-zero baseline output")
	}

	updated := params
	for i := 0; i < NumModes; i++ {
		updated.Modes[i].Amplitude = 0
	}
	if err := bar.UpdateParams(&updated); err != nil {
		t.Fatalf("failed to update params: %v", err)
	}

	bar.Reset()
	out2 := bar.Synthesize(100, 512)
	if peakAbs(out2) != 0 {
		t.Fatalf("expected silent output after zeroing amplitudes, peak=%.8f", peakAbs(out2))
	}
}

func TestBarSetSampleRate(t *testing.T) {
	params := validTestParams()
	bar, err := NewBar(&params, 44100)
	if err != nil {
		t.Fatalf("failed to create bar: %v", err)
	}

	if err := bar.SetSampleRate(96000); err != nil {
		t.Fatalf("set sample rate failed: %v", err)
	}

	out := bar.Synthesize(100, 512)
	if peakAbs(out) <= 0 {
		t.Fatal("expected non-zero output after sample-rate change")
	}
}

func TestBarProcessingChainChebyshevToggle(t *testing.T) {
	params := validTestParams()
	params.InputMix = 0
	params.Chebyshev.Enabled = false
	bar, err := NewBar(&params, 48000)
	if err != nil {
		t.Fatalf("failed to create bar: %v", err)
	}

	excitation := make([]float32, 512)
	excitation[0] = 0.8
	noCheby := bar.ProcessExcitation(excitation)

	params.Chebyshev.Enabled = true
	params.Chebyshev.HarmonicGains = []float64{1.0, 1.0}
	if err := bar.UpdateParams(&params); err != nil {
		t.Fatalf("update params failed: %v", err)
	}
	bar.Reset()
	withCheby := bar.ProcessExcitation(excitation)

	diff := rmsDiff(noCheby, withCheby)
	if diff == 0 {
		t.Fatal("expected chebyshev stage to alter output")
	}
}

func TestBarVelocityScaling(t *testing.T) {
	params := validTestParams()
	params.Chebyshev.Enabled = false
	params.InputMix = 0
	for i := range params.Modes {
		params.Modes[i].Amplitude = 0
	}
	params.Modes[0].Amplitude = 0.5
	bar, err := NewBar(&params, 48000)
	if err != nil {
		t.Fatalf("failed to create bar: %v", err)
	}

	low := bar.Synthesize(20, 1024)
	bar.Reset()
	high := bar.Synthesize(120, 1024)

	if rms(high) <= rms(low) {
		t.Fatalf("expected higher velocity to increase output energy: low=%.6f high=%.6f",
			rms(low), rms(high))
	}
}

func peakAbs(buf []float32) float64 {
	peak := 0.0
	for _, x := range buf {
		ax := math.Abs(float64(x))
		if ax > peak {
			peak = ax
		}
	}
	return peak
}

func rmsDiff(a, b []float32) float64 {
	if len(a) != len(b) {
		return math.Inf(1)
	}
	if len(a) == 0 {
		return 0
	}
	sum := 0.0
	for i := range a {
		d := float64(a[i] - b[i])
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(a)))
}

func rms(buf []float32) float64 {
	if len(buf) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range buf {
		v := float64(x)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(buf)))
}
