package oscbank

import (
	"math"
	"testing"
)

// The assertions in this file migrated from model.QuadDecayOscillator's tests
// when that fixed four-mode oscillator was retired. They describe the rotor
// recursion itself -- its coefficients, its envelope and its long-run stability
// -- which is behaviour the bank inherited rather than behaviour of the four
// hand-unrolled modes, so it keeps its coverage here.

func TestBankCoefficientsMatchTheDecayedRotation(t *testing.T) {
	const (
		sampleRate = 48000.0
		frequency  = 1000.0
		decayMs    = 100.0
	)

	bank := New(sampleRate)
	if err := bank.SetOscillators([]Oscillator{{
		Amplitude: 1,
		Frequency: frequency,
		DecayMs:   decayMs,
		Harmonics: []float64{1, 0.5},
	}}); err != nil {
		t.Fatalf("SetOscillators: %v", err)
	}

	wantDecay := math.Exp(-math.Ln2 / (0.001 * decayMs * sampleRate))

	// Rotor k runs at (k+1) times the fundamental and shares the decay, so both
	// rotors must carry the same decay factor on a different rotation angle.
	for harmonic := range 2 {
		phase := 2 * math.Pi * float64(harmonic+1) * frequency / sampleRate
		sinVal, cosVal := math.Sincos(phase)

		if got, want := float64(bank.cosCoeff[harmonic]), wantDecay*cosVal; math.Abs(got-want) > 1e-6 {
			t.Fatalf("rotor %d cosCoeff = %.9f, want %.9f", harmonic, got, want)
		}

		if got, want := float64(bank.sinCoeff[harmonic]), wantDecay*sinVal; math.Abs(got-want) > 1e-6 {
			t.Fatalf("rotor %d sinCoeff = %.9f, want %.9f", harmonic, got, want)
		}

		if got := bank.decayFactor[harmonic]; math.Abs(got-wantDecay) > 1e-12 {
			t.Fatalf("rotor %d decayFactor = %.15f, want %.15f", harmonic, got, wantDecay)
		}
	}
}

func TestBankDecayEnvelopeFollowsTheDecayFactor(t *testing.T) {
	const (
		sampleRate = 48000.0
		decayMs    = 100.0
	)

	// At zero frequency the rotor does not rotate, so consecutive outputs differ
	// by exactly the per-sample decay factor and the envelope is directly
	// readable from the signal.
	bank := New(sampleRate)
	if err := bank.SetOscillators([]Oscillator{{Amplitude: 1, Frequency: 0, DecayMs: decayMs}}); err != nil {
		t.Fatalf("SetOscillators: %v", err)
	}

	input := strikeInput(4)
	out := make([]float32, len(input))
	bank.ProcessBlock(input, out)

	if out[1] == 0 || out[2] == 0 || out[3] == 0 {
		t.Fatalf("expected a non-zero decaying response, got %v", out)
	}

	want := bank.MaxDecayFactor()

	for i := 2; i < len(out); i++ {
		ratio := float64(out[i] / out[i-1])
		if math.Abs(ratio-want) > 1e-5 {
			t.Fatalf("sample %d/%d ratio = %.6f, want %.6f", i, i-1, ratio, want)
		}
	}
}

func TestBankLongerDecayRetainsMoreEnergy(t *testing.T) {
	const sampleRate = 48000.0

	render := func(decayMs float64) []float32 {
		bank := New(sampleRate)
		if err := bank.SetOscillators([]Oscillator{{Amplitude: 1, Frequency: 0, DecayMs: decayMs}}); err != nil {
			t.Fatalf("SetOscillators: %v", err)
		}

		input := strikeInput(64)
		out := make([]float32, len(input))
		bank.ProcessBlock(input, out)

		return out
	}

	short := render(0.1)
	long := render(500)

	if math.Abs(float64(long[63])) <= math.Abs(float64(short[63])) {
		t.Fatalf("expected the longer decay to retain more energy: long=%g short=%g", long[63], short[63])
	}
}

func TestBankRemainsStableOverLongRuns(t *testing.T) {
	const sampleRate = 48000.0

	// The recursion is only drift-free while the decay factor stays below one.
	// Two million samples is roughly forty seconds: long enough that a rotor
	// that grows instead of decaying reaches infinity.
	bank := New(sampleRate)

	oscillators := make([]Oscillator, 4)
	for i := range oscillators {
		oscillators[i] = Oscillator{Amplitude: 1, Frequency: float64(500 * (i + 1)), DecayMs: 250}
	}

	if err := bank.SetOscillators(oscillators); err != nil {
		t.Fatalf("SetOscillators: %v", err)
	}

	input := strikeInput(1024)
	out := make([]float32, len(input))

	for block := range 2000 {
		bank.ProcessBlock(input, out)

		for i, v := range out {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("unstable output at sample %d of block %d: %v", i, block, v)
			}
		}

		input[0] = 0
	}
}
