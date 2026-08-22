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
	for i := range updated.Modes {
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
	noCheby := append([]float32(nil), bar.ProcessExcitation(excitation)...)

	params.Chebyshev.Enabled = true

	params.Chebyshev.HarmonicGains = []float64{1.0, 1.0}
	if err := bar.UpdateParams(&params); err != nil {
		t.Fatalf("update params failed: %v", err)
	}

	bar.Reset()
	withCheby := append([]float32(nil), bar.ProcessExcitation(excitation)...)

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

	low := append([]float32(nil), bar.Synthesize(20, 1024)...)
	bar.Reset()
	high := append([]float32(nil), bar.Synthesize(120, 1024)...)

	if rms(high) <= rms(low) {
		t.Fatalf("expected higher velocity to increase output energy: low=%.6f high=%.6f",
			rms(low), rms(high))
	}
}

func BenchmarkBarUpdateParams(b *testing.B) {
	params := validTestParams()

	bar, err := NewBar(&params, 48000)
	if err != nil {
		b.Fatalf("failed to create bar: %v", err)
	}

	updated := params
	updated.FilterFrequency = 640
	updated.Modes[0].Amplitude = 0.93
	updated.Modes[0].Frequency = 1810
	updated.Modes[0].DecayMs = 170

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if i&1 == 0 {
			if err := bar.UpdateParams(&params); err != nil {
				b.Fatalf("update params failed: %v", err)
			}
		} else {
			if err := bar.UpdateParams(&updated); err != nil {
				b.Fatalf("update params failed: %v", err)
			}
		}
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

func rmsDiff(first, second []float32) float64 {
	if len(first) != len(second) {
		return math.Inf(1)
	}

	if len(first) == 0 {
		return 0
	}

	sum := 0.0

	for i := range first {
		d := float64(first[i] - second[i])
		sum += d * d
	}

	return math.Sqrt(sum / float64(len(first)))
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

func approxEqual(got, want, tol float64) bool {
	return math.Abs(got-want) <= tol
}

// TestFinishBankOutputRejectsAShortDestination pins the contract the comment on
// FinishBankOutput states. The slice expression it does would panic on a short
// destination anyway; what this asks for is that it panics with a message that
// names the cause, at the entry rather than partway through the post-bank
// chain, the way oscbank's two buffer checks do.
func TestFinishBankOutputRejectsAShortDestination(t *testing.T) {
	params := validTestParams()

	bar, err := NewBar(&params, 48000)
	if err != nil {
		t.Fatalf("failed to create bar: %v", err)
	}

	const frames = 64

	bankIn := bar.StartBankInput(100, frames)
	if len(bankIn) != frames {
		t.Fatalf("StartBankInput returned %d frames, want %d", len(bankIn), frames)
	}

	bankOut := make([]float32, frames)
	copy(bankOut, bankIn)

	// A nil destination and a merely-too-short one are the same bug, so both
	// have to be refused rather than only the one that is easy to spot.
	for name, dst := range map[string][]float32{
		"nil":       nil,
		"too short": make([]float32, frames-1),
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					t.Fatal("expected a panic on a destination shorter than the bank output")
				}

				if msg, ok := recovered.(string); !ok || msg != "model: destination buffer too small" {
					t.Fatalf("panicked with %v, want the named buffer message", recovered)
				}
			}()

			bar.FinishBankOutput(bankOut, dst)
		})
	}

	// An exactly-sized destination is not too small.
	if got := bar.FinishBankOutput(bankOut, make([]float32, frames)); len(got) != frames {
		t.Fatalf("FinishBankOutput returned %d frames for an exact destination, want %d", len(got), frames)
	}
}
