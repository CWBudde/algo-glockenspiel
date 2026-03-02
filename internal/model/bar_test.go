package model

import (
	"math"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/cpufeat"
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

func TestProcessChebyshevBlockAVX2MatchesScalar(t *testing.T) {
	if !cpufeat.Detect().HasAVX2 {
		t.Skip("AVX2 not available")
	}

	input := make([]float32, 257)
	for i := range input {
		input[i] = float32(math.Sin(float64(i)*0.17) * 1.3)
	}
	gains := []float64{1.0, 0.5, 0.3, 0.2}
	gains4 := [4]float32{1.0, 0.5, 0.3, 0.2}

	got := make([]float32, len(input))
	want := make([]float32, len(input))

	if !processChebyshevBlockAVX2(input, got, &gains4) {
		t.Fatal("expected AVX2 Chebyshev path to be active")
	}
	for i := range input {
		want[i] = float32(applyChebyshev(float64(input[i]), gains))
	}

	for i := range input {
		if !approxEqual(float64(got[i]), float64(want[i]), 1e-5) {
			t.Fatalf("chebyshev mismatch at %d: got %.8f want %.8f", i, got[i], want[i])
		}
	}
}

func BenchmarkProcessChebyshevBlock(b *testing.B) {
	input := make([]float32, 512)
	output := make([]float32, 512)
	gains := []float64{1.0, 0.5, 0.3, 0.2}
	gains4 := [4]float32{1.0, 0.5, 0.3, 0.2}
	for i := range input {
		input[i] = float32(math.Sin(float64(i)*0.17) * 1.3)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		processChebyshevBlock(input, output, gains, &gains4, true)
	}
}

func BenchmarkChebyshevOscillatorSeparate(b *testing.B) {
	input := make([]float32, 512)
	distorted := make([]float32, 512)
	output := make([]float32, 512)
	gains := []float64{1.0, 0.5, 0.3, 0.2}
	gains4 := [4]float32{1.0, 0.5, 0.3, 0.2}
	osc := benchmarkConfiguredOscillator()
	for i := range input {
		input[i] = float32(math.Sin(float64(i)*0.17) * 1.3)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		osc.Reset()
		processChebyshevBlock(input, distorted, gains, &gains4, true)
		osc.ProcessBlock32(distorted, output)
	}
}

func BenchmarkChebyshevOscillatorFused(b *testing.B) {
	if !cpufeat.Detect().HasAVX2 {
		b.Skip("AVX2 not available")
	}

	input := make([]float32, 512)
	output := make([]float32, 512)
	gains4 := [4]float32{1.0, 0.5, 0.3, 0.2}
	osc := benchmarkConfiguredOscillator()
	for i := range input {
		input[i] = float32(math.Sin(float64(i)*0.17) * 1.3)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		osc.Reset()
		if !processChebyshev4OscillatorBlockAVX2(osc, input, output, &gains4) {
			b.Fatal("expected fused AVX2 path to be active")
		}
	}
}

func benchmarkConfiguredOscillator() *QuadDecayOscillator {
	osc := NewQuadDecayOscillator(48000)
	for mode := 0; mode < NumModes; mode++ {
		freq := float64(430 + 300*mode)
		amp := 0.2 + float64(mode)*0.2
		decay := 30.0 + float64(mode)*60.0
		osc.SetMode(mode, amp, freq, decay)
	}

	return osc
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

func TestProcessChebyshev4OscillatorBlockAVX2MatchesScalar(t *testing.T) {
	if !cpufeat.Detect().HasAVX2 {
		t.Skip("AVX2 not available")
	}

	avx := NewQuadDecayOscillator(48000)
	gen := NewQuadDecayOscillator(48000)
	for i := 0; i < NumModes; i++ {
		freq := float64(430 + 300*i)
		amp := 0.2 + float64(i)*0.2
		decay := 30.0 + float64(i)*60.0
		avx.SetMode(i, amp, freq, decay)
		gen.SetMode(i, amp, freq, decay)
	}

	input := make([]float32, 257)
	for i := range input {
		input[i] = float32(math.Sin(float64(i)*0.11) * 1.2)
	}
	gains4 := [4]float32{1.0, 0.5, 0.3, 0.2}
	gains := []float64{1.0, 0.5, 0.3, 0.2}

	got := make([]float32, len(input))
	want := make([]float32, len(input))

	if !processChebyshev4OscillatorBlockAVX2(avx, input, got, &gains4) {
		t.Fatal("expected fused AVX2 path to be active")
	}
	for i := range input {
		want[i] = gen.ProcessSample32(float32(applyChebyshev(float64(input[i]), gains)))
	}

	for i := range input {
		if !approxEqual(float64(got[i]), float64(want[i]), 1e-5) {
			t.Fatalf("fused cheby+osc mismatch at %d: got %.8f want %.8f", i, got[i], want[i])
		}
	}

	for i := 0; i < NumModes; i++ {
		if !approxEqual(avx.realState[i], gen.realState[i], 1e-6) {
			t.Fatalf("real state mismatch at mode %d: got %.12f want %.12f", i, avx.realState[i], gen.realState[i])
		}
		if !approxEqual(avx.imagState[i], gen.imagState[i], 1e-6) {
			t.Fatalf("imag state mismatch at mode %d: got %.12f want %.12f", i, avx.imagState[i], gen.imagState[i])
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
