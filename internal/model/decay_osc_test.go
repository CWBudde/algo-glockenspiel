package model

import (
	"math"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/cpufeat"
)

func TestQuadDecayOscillatorCoefficientCalculation(t *testing.T) {
	const (
		sampleRate = 48000.0
		freq       = 1000.0
		decayMs    = 100.0
	)

	osc := NewQuadDecayOscillator(sampleRate)
	osc.SetAmplitude(0, 1)
	osc.SetFrequency(0, freq)
	osc.SetDecay(0, decayMs)

	wantDecay := math.Exp(-math.Ln2 / (0.001 * decayMs * sampleRate))
	wantPhase := 2 * math.Pi * freq / sampleRate
	wantSin, wantCos := math.Sincos(wantPhase)

	if !approxEqual(osc.decayFactor[0], wantDecay, 1e-12) {
		t.Fatalf("decayFactor mismatch: got %.15f want %.15f", osc.decayFactor[0], wantDecay)
	}

	if !approxEqual(osc.cosCoeff[0], wantDecay*wantCos, 1e-12) {
		t.Fatalf("cosCoeff mismatch: got %.15f want %.15f", osc.cosCoeff[0], wantDecay*wantCos)
	}

	if !approxEqual(osc.sinCoeff[0], wantDecay*wantSin, 1e-12) {
		t.Fatalf("sinCoeff mismatch: got %.15f want %.15f", osc.sinCoeff[0], wantDecay*wantSin)
	}
}

func TestQuadDecayOscillatorDecayEnvelope(t *testing.T) {
	osc := NewQuadDecayOscillator(48000)
	osc.SetAmplitude(0, 1)
	osc.SetFrequency(0, 0)
	osc.SetDecay(0, 100)

	for i := 1; i < NumModes; i++ {
		osc.SetAmplitude(i, 0)
	}

	// Impulse excites imag state; outputs follow geometric decay for freq=0.
	_ = osc.ProcessSample32(1)
	firstOutput := osc.ProcessSample32(0)
	secondOutput := osc.ProcessSample32(0)
	thirdOutput := osc.ProcessSample32(0)

	if firstOutput == 0 || secondOutput == 0 || thirdOutput == 0 {
		t.Fatal("expected non-zero decaying response")
	}

	firstRatio := float64(secondOutput / firstOutput)
	secondRatio := float64(thirdOutput / secondOutput)

	want := osc.decayFactor[0]
	if !approxEqual(firstRatio, want, 1e-5) || !approxEqual(secondRatio, want, 1e-5) {
		t.Fatalf("unexpected decay ratios: r1=%.6f r2=%.6f want=%.6f", firstRatio, secondRatio, want)
	}
}

func TestQuadDecayOscillatorReset(t *testing.T) {
	osc := NewQuadDecayOscillator(44100)
	osc.SetAmplitude(0, 1)
	_ = osc.ProcessSample32(1)
	_ = osc.ProcessSample32(0)

	osc.Reset()

	for i := 0; i < NumModes; i++ {
		if osc.realState[i] != 0 || osc.imagState[i] != 0 {
			t.Fatalf("state not reset at mode %d", i)
		}
	}
}

func TestQuadDecayOscillatorNumericalStability(t *testing.T) {
	osc := NewQuadDecayOscillator(48000)
	for i := 0; i < NumModes; i++ {
		osc.SetAmplitude(i, 1.0)
		osc.SetFrequency(i, float64(500*(i+1)))
		osc.SetDecay(i, 250.0)
	}

	_ = osc.ProcessSample32(1)
	for i := 0; i < 2_000_000; i++ {
		out := osc.ProcessSample32(0)
		if math.IsNaN(float64(out)) || math.IsInf(float64(out), 0) {
			t.Fatalf("unstable output at sample %d: %v", i, out)
		}
	}
}

func TestQuadDecayOscillatorEdgeCases(t *testing.T) {
	osc := NewQuadDecayOscillator(48000)
	osc.SetAmplitude(0, 1)
	osc.SetFrequency(0, 0)
	osc.SetDecay(0, DecayMsMin)

	for i := 1; i < NumModes; i++ {
		osc.SetAmplitude(i, 0)
	}

	_ = osc.ProcessSample32(1)
	shortDecay := osc.ProcessSample32(0)

	osc.Reset()
	osc.SetDecay(0, DecayMsMax)
	_ = osc.ProcessSample32(1)
	longDecay := osc.ProcessSample32(0)

	if math.Abs(float64(longDecay)) <= math.Abs(float64(shortDecay)) {
		t.Fatalf("expected longer decay to retain more energy: long=%f short=%f", longDecay, shortDecay)
	}
}

func TestQuadDecayOscillatorFlushesDenormals(t *testing.T) {
	osc := NewQuadDecayOscillator(48000)
	osc.realState[0] = 1e-320
	osc.imagState[0] = -1e-320

	osc.ProcessBlock32([]float32{0, 0, 0, 0}, make([]float32, 4))

	if osc.realState[0] != 0 || osc.imagState[0] != 0 {
		t.Fatalf("expected denormal state to be flushed, got real=%g imag=%g", osc.realState[0], osc.imagState[0])
	}
}

func BenchmarkQuadDecayOscillatorProcessSample32(b *testing.B) {
	osc := NewQuadDecayOscillator(48000)
	for i := 0; i < NumModes; i++ {
		osc.SetAmplitude(i, 0.5)
	}

	var sample float32 = 1

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		sample = osc.ProcessSample32(sample * 0)
	}

	_ = sample
}

func BenchmarkQuadDecayOscillatorProcessBlock32(b *testing.B) {
	osc := NewQuadDecayOscillator(48000)
	for i := 0; i < NumModes; i++ {
		osc.SetAmplitude(i, 0.5)
	}

	input := make([]float32, 512)
	out := make([]float32, 512)
	input[0] = 1

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		osc.ProcessBlock32(input, out)
		input[0] = 0
	}
}

func BenchmarkQuadDecayOscillatorProcessBlock32Generic(b *testing.B) {
	osc := NewQuadDecayOscillator(48000)
	for i := 0; i < NumModes; i++ {
		osc.SetAmplitude(i, 0.5)
	}

	input := make([]float32, 512)
	out := make([]float32, 512)
	input[0] = 1

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		osc.processBlock32Generic(input, out)
		input[0] = 0
	}
}

func BenchmarkProcessModeBlock4AVX2(b *testing.B) {
	if !cpufeat.Detect().HasAVX2 {
		b.Skip("AVX2 not available")
	}

	osc := NewQuadDecayOscillator(48000)
	mode := 1
	osc.SetMode(mode, 0.41, 1275, 145)
	input := [4]float32{0.35, -0.2, 0.1, 0.45}
	output := [4]float64{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		realState := osc.realState[mode]
		imagState := osc.imagState[mode]
		amplitude := osc.amplitude[mode]
		if !processModeBlock4AVX2(&realState, &imagState, &amplitude, &osc.block4Coeff[mode], &input, &output) {
			b.Fatal("expected AVX2 mode-block path to be active")
		}
	}
}

func BenchmarkQuadDecayOscillatorProcessBlock32ModeBlock4PrototypeAVX2(b *testing.B) {
	if !cpufeat.Detect().HasAVX2 {
		b.Skip("AVX2 not available")
	}

	osc := NewQuadDecayOscillator(48000)
	for i := 0; i < NumModes; i++ {
		osc.SetAmplitude(i, 0.5)
	}

	input := make([]float32, 512)
	out := make([]float32, 512)
	input[0] = 1

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !processBlock32ModeBlock4PrototypeAVX2(osc, input, out) {
			b.Fatal("expected mode-block prototype path to be active")
		}
		input[0] = 0
	}
}

func BenchmarkQuadDecayOscillatorProcessBlock32ModeBlock4KernelAVX2(b *testing.B) {
	if !cpufeat.Detect().HasAVX2 {
		b.Skip("AVX2 not available")
	}

	osc := NewQuadDecayOscillator(48000)
	for i := 0; i < NumModes; i++ {
		osc.SetAmplitude(i, 0.5)
	}

	input := make([]float32, 512)
	out := make([]float32, 512)
	input[0] = 1

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !processBlock32ModeBlock4KernelAVX2(osc, input, out) {
			b.Fatal("expected mode-block kernel path to be active")
		}
		input[0] = 0
	}
}

func TestQuadDecayOscillatorProcessBlock32AVX2MatchesGeneric(t *testing.T) {
	if !cpufeat.Detect().HasAVX2 {
		t.Skip("AVX2 not available")
	}

	avx := NewQuadDecayOscillator(48000)
	gen := NewQuadDecayOscillator(48000)
	for i := 0; i < NumModes; i++ {
		freq := float64(400 + 275*i)
		amp := 0.25 + float64(i)*0.15
		decay := 40.0 + float64(i)*55.0
		avx.SetFrequency(i, freq)
		avx.SetAmplitude(i, amp)
		avx.SetDecay(i, decay)
		gen.SetFrequency(i, freq)
		gen.SetAmplitude(i, amp)
		gen.SetDecay(i, decay)
	}

	input := make([]float32, 257)
	for i := range input {
		input[i] = float32(math.Sin(float64(i)*0.11) * 0.4)
	}
	avxOut := make([]float32, len(input))
	genOut := make([]float32, len(input))

	if !processBlock32AVX2(avx, input, avxOut) {
		t.Fatal("expected AVX2 block path to be active")
	}
	gen.processBlock32Generic(input, genOut)

	for i := range input {
		if !approxEqual(float64(avxOut[i]), float64(genOut[i]), 1e-5) {
			t.Fatalf("output mismatch at %d: got %.8f want %.8f", i, avxOut[i], genOut[i])
		}
	}

	for i := 0; i < NumModes; i++ {
		if !approxEqual(avx.realState[i], gen.realState[i], 1e-9) {
			t.Fatalf("real state mismatch at mode %d: got %.12f want %.12f", i, avx.realState[i], gen.realState[i])
		}
		if !approxEqual(avx.imagState[i], gen.imagState[i], 1e-9) {
			t.Fatalf("imag state mismatch at mode %d: got %.12f want %.12f", i, avx.imagState[i], gen.imagState[i])
		}
	}
}

func TestQuadDecayOscillatorProcessBlock32ModeBlock4PrototypeMatchesGeneric(t *testing.T) {
	if !cpufeat.Detect().HasAVX2 {
		t.Skip("AVX2 not available")
	}

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
		input[i] = float32(math.Sin(float64(i)*0.11) * 0.4)
	}
	avxOut := make([]float32, len(input))
	genOut := make([]float32, len(input))

	if !processBlock32ModeBlock4PrototypeAVX2(avx, input, avxOut) {
		t.Fatal("expected mode-block prototype path to be active")
	}
	gen.processBlock32Generic(input, genOut)

	for i := range input {
		if !approxEqual(float64(avxOut[i]), float64(genOut[i]), 1e-5) {
			t.Fatalf("output mismatch at %d: got %.8f want %.8f", i, avxOut[i], genOut[i])
		}
	}

	for i := 0; i < NumModes; i++ {
		if !approxEqual(avx.realState[i], gen.realState[i], 1e-9) {
			t.Fatalf("real state mismatch at mode %d: got %.12f want %.12f", i, avx.realState[i], gen.realState[i])
		}
		if !approxEqual(avx.imagState[i], gen.imagState[i], 1e-9) {
			t.Fatalf("imag state mismatch at mode %d: got %.12f want %.12f", i, avx.imagState[i], gen.imagState[i])
		}
	}
}

func TestQuadDecayOscillatorProcessBlock32ModeBlock4KernelMatchesGeneric(t *testing.T) {
	if !cpufeat.Detect().HasAVX2 {
		t.Skip("AVX2 not available")
	}

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
		input[i] = float32(math.Sin(float64(i)*0.11) * 0.4)
	}
	avxOut := make([]float32, len(input))
	genOut := make([]float32, len(input))

	if !processBlock32ModeBlock4KernelAVX2(avx, input, avxOut) {
		t.Fatal("expected mode-block kernel path to be active")
	}
	gen.processBlock32Generic(input, genOut)

	for i := range input {
		if !approxEqual(float64(avxOut[i]), float64(genOut[i]), 1e-5) {
			t.Fatalf("output mismatch at %d: got %.8f want %.8f", i, avxOut[i], genOut[i])
		}
	}

	for i := 0; i < NumModes; i++ {
		if !approxEqual(avx.realState[i], gen.realState[i], 1e-9) {
			t.Fatalf("real state mismatch at mode %d: got %.12f want %.12f", i, avx.realState[i], gen.realState[i])
		}
		if !approxEqual(avx.imagState[i], gen.imagState[i], 1e-9) {
			t.Fatalf("imag state mismatch at mode %d: got %.12f want %.12f", i, avx.imagState[i], gen.imagState[i])
		}
	}
}

func TestProcessModeBlock4MatchesSampleBySample(t *testing.T) {
	osc := NewQuadDecayOscillator(48000)
	mode := 2
	osc.SetMode(mode, 0.63, 1430, 180)

	x0, x1, x2, x3 := 0.35, -0.2, 0.1, 0.45
	block := processModeBlock4(
		osc.realState[mode],
		osc.imagState[mode],
		osc.amplitude[mode],
		osc.cosCoeff[mode],
		osc.sinCoeff[mode],
		osc.block4Coeff[mode],
		x0, x1, x2, x3,
	)

	r, im := osc.realState[mode], osc.imagState[mode]
	a, c, s := osc.amplitude[mode], osc.cosCoeff[mode], osc.sinCoeff[mode]
	inputs := [4]float64{x0, x1, x2, x3}
	var outputs [4]float64
	for i, in := range inputs {
		temp := im*c + r*s
		r = r*c - im*s
		im = a*in + temp
		outputs[i] = temp
	}

	if !approxEqual(block.out0, outputs[0], 1e-12) {
		t.Fatalf("out0 mismatch: got %.15f want %.15f", block.out0, outputs[0])
	}
	if !approxEqual(block.out1, outputs[1], 1e-12) {
		t.Fatalf("out1 mismatch: got %.15f want %.15f", block.out1, outputs[1])
	}
	if !approxEqual(block.out2, outputs[2], 1e-12) {
		t.Fatalf("out2 mismatch: got %.15f want %.15f", block.out2, outputs[2])
	}
	if !approxEqual(block.out3, outputs[3], 1e-12) {
		t.Fatalf("out3 mismatch: got %.15f want %.15f", block.out3, outputs[3])
	}
	if !approxEqual(block.real, r, 1e-12) {
		t.Fatalf("real mismatch: got %.15f want %.15f", block.real, r)
	}
	if !approxEqual(block.imag, im, 1e-12) {
		t.Fatalf("imag mismatch: got %.15f want %.15f", block.imag, im)
	}
}

func TestQuadDecayOscillatorProcessBlock32GenericMatchesSampleBySample(t *testing.T) {
	gen := NewQuadDecayOscillator(48000)
	ref := NewQuadDecayOscillator(48000)
	for i := 0; i < NumModes; i++ {
		freq := float64(410 + 235*i)
		amp := 0.2 + float64(i)*0.17
		decay := 45.0 + float64(i)*70.0
		gen.SetMode(i, amp, freq, decay)
		ref.SetMode(i, amp, freq, decay)
	}

	input := make([]float32, 259)
	for i := range input {
		input[i] = float32(math.Sin(float64(i)*0.09) * 0.7)
	}
	got := make([]float32, len(input))
	want := make([]float32, len(input))

	gen.processBlock32Generic(input, got)
	for i, in := range input {
		want[i] = ref.ProcessSample32(in)
	}

	for i := range input {
		if !approxEqual(float64(got[i]), float64(want[i]), 1e-5) {
			t.Fatalf("output mismatch at %d: got %.8f want %.8f", i, got[i], want[i])
		}
	}

	for i := 0; i < NumModes; i++ {
		if !approxEqual(gen.realState[i], ref.realState[i], 1e-9) {
			t.Fatalf("real state mismatch at mode %d: got %.12f want %.12f", i, gen.realState[i], ref.realState[i])
		}
		if !approxEqual(gen.imagState[i], ref.imagState[i], 1e-9) {
			t.Fatalf("imag state mismatch at mode %d: got %.12f want %.12f", i, gen.imagState[i], ref.imagState[i])
		}
	}
}

func TestProcessModeBlock4AVX2MatchesScalar(t *testing.T) {
	if !cpufeat.Detect().HasAVX2 {
		t.Skip("AVX2 not available")
	}

	osc := NewQuadDecayOscillator(48000)
	mode := 1
	osc.SetMode(mode, 0.41, 1275, 145)

	realState := osc.realState[mode]
	imagState := osc.imagState[mode]
	amplitude := osc.amplitude[mode]
	input := [4]float32{0.35, -0.2, 0.1, 0.45}
	output := [4]float64{}

	want := processModeBlock4(
		realState,
		imagState,
		amplitude,
		osc.cosCoeff[mode],
		osc.sinCoeff[mode],
		osc.block4Coeff[mode],
		float64(input[0]),
		float64(input[1]),
		float64(input[2]),
		float64(input[3]),
	)

	gotReal := realState
	gotImag := imagState
	if !processModeBlock4AVX2(&gotReal, &gotImag, &amplitude, &osc.block4Coeff[mode], &input, &output) {
		t.Fatal("expected AVX2 mode-block path to be active")
	}

	if !approxEqual(output[0], want.out0, 1e-12) {
		t.Fatalf("out0 mismatch: got %.15f want %.15f", output[0], want.out0)
	}
	if !approxEqual(output[1], want.out1, 1e-12) {
		t.Fatalf("out1 mismatch: got %.15f want %.15f", output[1], want.out1)
	}
	if !approxEqual(output[2], want.out2, 1e-12) {
		t.Fatalf("out2 mismatch: got %.15f want %.15f", output[2], want.out2)
	}
	if !approxEqual(output[3], want.out3, 1e-12) {
		t.Fatalf("out3 mismatch: got %.15f want %.15f", output[3], want.out3)
	}
	if !approxEqual(gotReal, want.real, 1e-12) {
		t.Fatalf("real mismatch: got %.15f want %.15f", gotReal, want.real)
	}
	if !approxEqual(gotImag, want.imag, 1e-12) {
		t.Fatalf("imag mismatch: got %.15f want %.15f", gotImag, want.imag)
	}
}

func approxEqual(got, want, tol float64) bool {
	return math.Abs(got-want) <= tol
}
