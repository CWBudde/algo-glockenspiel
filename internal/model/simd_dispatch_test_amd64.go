//go:build amd64

package model

import (
	"math"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/cpufeat"
)

func TestProcessBlock32AVX2ReturnsFalseWhenAVX2ForcedOff(t *testing.T) {
	t.Cleanup(cpufeat.ResetDetection)
	cpufeat.SetForcedFeatures(cpufeat.Features{HasAVX2: false})

	osc := NewQuadDecayOscillator(48000)
	input := []float32{1, 0, 0, 0}
	output := make([]float32, len(input))

	if processBlock32AVX2(osc, input, output) {
		t.Fatal("expected AVX2 oscillator path to be disabled")
	}
}

func TestProcessChebyshevBlockAVX2ReturnsFalseWhenAVX2ForcedOff(t *testing.T) {
	t.Cleanup(cpufeat.ResetDetection)
	cpufeat.SetForcedFeatures(cpufeat.Features{HasAVX2: false})

	input := make([]float32, 32)
	output := make([]float32, len(input))
	gains4 := [4]float32{1, 0.5, 0.3, 0.2}

	if processChebyshevBlockAVX2(input, output, &gains4) {
		t.Fatal("expected AVX2 Chebyshev path to be disabled")
	}
}

func TestProcessBlock32AVX2DefaultStrategyMatchesModeParallelKernel(t *testing.T) {
	t.Cleanup(resetAVX2OscillatorStrategy)

	def := NewQuadDecayOscillator(48000)
	par := NewQuadDecayOscillator(48000)
	for i := 0; i < NumModes; i++ {
		freq := float64(400 + 275*i)
		amp := 0.25 + float64(i)*0.15
		decay := 40.0 + float64(i)*55.0
		def.SetMode(i, amp, freq, decay)
		par.SetMode(i, amp, freq, decay)
	}

	input := make([]float32, 257)
	for i := range input {
		input[i] = float32(i%7) * 0.1
	}
	got := make([]float32, len(input))
	want := make([]float32, len(input))

	if !processBlock32AVX2(def, input, got) {
		t.Fatal("expected default AVX2 oscillator path to be active")
	}
	processBlock4AVX2Asm(
		&par.realState[0],
		&par.imagState[0],
		&par.amplitude[0],
		&par.cosCoeff[0],
		&par.sinCoeff[0],
		&input[0],
		&want[0],
		len(input),
	)

	for i := range input {
		if got[i] != want[i] {
			t.Fatalf("output mismatch at %d: got %.8f want %.8f", i, got[i], want[i])
		}
	}
}

func TestProcessBlock32AVX2CanForceModeBlock4Strategy(t *testing.T) {
	t.Cleanup(resetAVX2OscillatorStrategy)
	setForcedAVX2OscillatorStrategy(avx2OscillatorStrategyModeBlock4)

	avx := NewQuadDecayOscillator(48000)
	gen := NewQuadDecayOscillator(48000)
	for i := 0; i < NumModes; i++ {
		freq := float64(400 + 275*i)
		amp := 0.25 + float64(i)*0.15
		decay := 40.0 + float64(i)*55.0
		avx.SetMode(i, amp, freq, decay)
		gen.SetMode(i, amp, freq, decay)
	}

	input := make([]float32, 259)
	for i := range input {
		input[i] = float32(i%9) * 0.07
	}
	avxOut := make([]float32, len(input))
	genOut := make([]float32, len(input))

	if !processBlock32AVX2(avx, input, avxOut) {
		t.Fatal("expected forced mode-block AVX2 path to be active")
	}
	gen.processBlock32Generic(input, genOut)

	for i := range input {
		if math.Abs(float64(avxOut[i]-genOut[i])) > 1e-5 {
			t.Fatalf("output mismatch at %d: got %.8f want %.8f", i, avxOut[i], genOut[i])
		}
	}
}
