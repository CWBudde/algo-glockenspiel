package oscbank

import "testing"

func harmonicOscillators(count, harmonics int) []Oscillator {
	out := make([]Oscillator, count)

	for i := range out {
		gains := make([]float64, harmonics)
		for k := range gains {
			gains[k] = 1 / float64(k+1)
		}

		out[i] = Oscillator{
			Amplitude: 1 / float64(i+1),
			Frequency: 440 * float64(i+1),
			DecayMs:   200,
			Harmonics: gains,
		}
	}

	return out
}

// TestSetOscillatorsIsAllocationFreeWhenTheShapeIsUnchanged pins the bank half
// of the retune path: a pooled voice reaching a new note must not allocate, and
// SetOscillators is on that path via Bar.UpdateParams.
func TestSetOscillatorsIsAllocationFreeWhenTheShapeIsUnchanged(t *testing.T) {
	bank := New(48000)
	oscillators := harmonicOscillators(4, 3)

	for i := 0; i < 4; i++ {
		if err := bank.SetOscillators(oscillators); err != nil {
			t.Fatalf("SetOscillators: %v", err)
		}
	}

	allocs := testing.AllocsPerRun(200, func() {
		if err := bank.SetOscillators(oscillators); err != nil {
			t.Fatalf("SetOscillators: %v", err)
		}
	})

	if allocs != 0 {
		t.Fatalf("SetOscillators allocated %v times per call on an unchanged shape, want 0", allocs)
	}
}

// TestSetOscillatorsShrinkingDropsStaleHarmonics is the correctness half: the
// reused slices must never let a previous configuration show through.
func TestSetOscillatorsShrinkingDropsStaleHarmonics(t *testing.T) {
	bank := New(48000)

	if err := bank.SetOscillators(harmonicOscillators(6, 5)); err != nil {
		t.Fatalf("SetOscillators wide: %v", err)
	}

	narrow := []Oscillator{
		{Amplitude: 1, Frequency: 440, DecayMs: 100},
		{Amplitude: 0.5, Frequency: 880, DecayMs: 100, Harmonics: []float64{1, 0.25}},
	}

	if err := bank.SetOscillators(narrow); err != nil {
		t.Fatalf("SetOscillators narrow: %v", err)
	}

	if got := bank.NumOscillators(); got != len(narrow) {
		t.Fatalf("NumOscillators: want %d, got %d", len(narrow), got)
	}

	if got := bank.NumHarmonics(); got != 2 {
		t.Fatalf("NumHarmonics: want 2, got %d", got)
	}

	stored := bank.Oscillators()
	if stored[0].Harmonics != nil {
		t.Fatalf("stale harmonics survived the shrink: %v", stored[0].Harmonics)
	}

	if len(stored[1].Harmonics) != 2 || stored[1].Harmonics[1] != 0.25 {
		t.Fatalf("harmonics not copied correctly: %v", stored[1].Harmonics)
	}

	// A bank built fresh from the same configuration must render identically.
	reference := New(48000)
	if err := reference.SetOscillators(narrow); err != nil {
		t.Fatalf("SetOscillators on the reference: %v", err)
	}

	input := make([]float32, 512)
	input[0] = 1

	got := make([]float32, len(input))
	want := make([]float32, len(input))

	bank.ProcessBlock(input, got)
	reference.ProcessBlock(input, want)

	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("sample %d differs after reconfiguring in place: want %v, got %v", i, want[i], got[i])
		}
	}
}

// TestSetOscillatorsDoesNotAliasTheCallerSlice guards the deep copy that
// storeOscillators still has to perform even while reusing its buffers.
func TestSetOscillatorsDoesNotAliasTheCallerSlice(t *testing.T) {
	bank := New(48000)
	oscillators := harmonicOscillators(2, 3)

	if err := bank.SetOscillators(oscillators); err != nil {
		t.Fatalf("SetOscillators: %v", err)
	}

	oscillators[0].Harmonics[0] = 0
	oscillators[0].Frequency = 9000

	stored := bank.Oscillators()
	if stored[0].Harmonics[0] != 1 || stored[0].Frequency != 440 {
		t.Fatalf("bank aliases the caller's configuration: %+v", stored[0])
	}
}
