package model

import (
	"math"
	"testing"
)

func TestQuadDecayOscillatorCoefficientCalculation(t *testing.T) {
	const (
		sr      = 48000.0
		freq    = 1000.0
		decayMs = 100.0
	)

	osc := NewQuadDecayOscillator(sr)
	osc.SetAmplitude(0, 1)
	osc.SetFrequency(0, freq)
	osc.SetDecay(0, decayMs)

	wantDecay := math.Exp(-math.Ln2 / (0.001 * decayMs * sr))
	wantPhase := 2 * math.Pi * freq / sr
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
	y1 := osc.ProcessSample32(0)
	y2 := osc.ProcessSample32(0)
	y3 := osc.ProcessSample32(0)

	if y1 == 0 || y2 == 0 || y3 == 0 {
		t.Fatal("expected non-zero decaying response")
	}

	r1 := float64(y2 / y1)
	r2 := float64(y3 / y2)

	want := osc.decayFactor[0]
	if !approxEqual(r1, want, 1e-5) || !approxEqual(r2, want, 1e-5) {
		t.Fatalf("unexpected decay ratios: r1=%.6f r2=%.6f want=%.6f", r1, r2, want)
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

func BenchmarkQuadDecayOscillatorProcessSample32(b *testing.B) {
	osc := NewQuadDecayOscillator(48000)
	for i := 0; i < NumModes; i++ {
		osc.SetAmplitude(i, 0.5)
	}

	var x float32 = 1

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		x = osc.ProcessSample32(x * 0)
	}

	_ = x
}

func BenchmarkQuadDecayOscillatorProcessBlock32(b *testing.B) {
	osc := NewQuadDecayOscillator(48000)
	for i := 0; i < NumModes; i++ {
		osc.SetAmplitude(i, 0.5)
	}

	in := make([]float32, 512)
	out := make([]float32, 512)
	in[0] = 1

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		osc.ProcessBlock32(in, out)
		in[0] = 0
	}
}

func approxEqual(got, want, tol float64) bool {
	return math.Abs(got-want) <= tol
}
