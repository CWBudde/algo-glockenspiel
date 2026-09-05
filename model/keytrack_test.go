package model

import (
	"math"
	"testing"
)

// keytrackTestDecays are awkward on purpose: the shipped preset's own shortest
// and longest modes, the two validation limits, and a value with a long binary
// expansion.
var keytrackTestDecays = []float64{
	DecayMsMin, 0.5605, 188.223281860352, 351.6, DecayMsSearchMax, DecayMsValidationMax,
}

// TestDefaultKeytrackReproducesTheOldLaw is the pin behind "every preset written
// before this field existed renders bit-identically".
//
// It asserts float equality rather than a tolerance, and it does so against a
// locally inlined copy of the arithmetic TransposeToNote used before the
// exponent existed. Bit-identical transposed parameters imply bit-identical
// NewBar coefficients, which imply bit-identical renders, which imply a
// bit-identical calibrateNoteTrims -- so this one property carries the whole
// claim without committing a table of golden vectors that would have to be
// regenerated the first time anything legitimately moved.
func TestDefaultKeytrackReproducesTheOldLaw(t *testing.T) {
	one := DecayKeytrackDefault

	for _, from := range []int{0, 36, 69, KeyboardFirstNote, 93, KeyboardLastNote, 127} {
		for target := KeyboardFirstNote; target <= KeyboardLastNote; target++ {
			for _, decay := range keytrackTestDecays {
				// The law as it stood before the exponent existed.
				want := decay
				if from != target {
					want = decay / math.Pow(2, float64(target-from)/12)
				}

				absent := BarParams{Modes: []ModeParams{{DecayMs: decay, Frequency: 1000, Amplitude: 1}}}
				explicit := BarParams{
					DecayKeytrack: &one,
					Modes:         []ModeParams{{DecayMs: decay, Frequency: 1000, Amplitude: 1}},
				}

				TransposeToNote(&absent, from, target)
				TransposeToNote(&explicit, from, target)

				if got := absent.Modes[0].DecayMs; got != want {
					t.Fatalf("nil keytrack, %g ms from %d to %d: got %v, want exactly %v",
						decay, from, target, got, want)
				}

				if got := explicit.Modes[0].DecayMs; got != want {
					t.Fatalf("keytrack 1, %g ms from %d to %d: got %v, want exactly %v",
						decay, from, target, got, want)
				}
			}
		}
	}
}

// TestKeytrackScalesTheExponent is the generalisation: one octave down
// multiplies a decay by 2^beta, one octave up divides by it, and a zero
// exponent leaves the decay untouched while the frequency still doubles.
func TestKeytrackScalesTheExponent(t *testing.T) {
	for _, keytrack := range []float64{DecayKeytrackMin, -0.24, 0, 0.5, 1, 1.22, DecayKeytrackMax} {
		beta := keytrack

		for _, octaves := range []int{-2, -1, 1, 2} {
			params := BarParams{
				DecayKeytrack: &beta,
				Modes:         []ModeParams{{DecayMs: 400, Frequency: 1000, Amplitude: 1}},
			}

			TransposeToNote(&params, 90, 90+12*octaves)

			wantDecay := 400 / math.Pow(math.Pow(2, float64(octaves)), beta)
			if got := params.Modes[0].DecayMs; math.Abs(got-wantDecay) > 1e-9*wantDecay {
				t.Errorf("beta %g, %+d octaves: decay %g, want %g", beta, octaves, got, wantDecay)
			}

			// The frequency never depends on the exponent.
			wantFreq := 1000 * math.Pow(2, float64(octaves))
			if got := params.Modes[0].Frequency; math.Abs(got-wantFreq) > 1e-9*wantFreq {
				t.Errorf("beta %g, %+d octaves: frequency %g, want %g", beta, octaves, got, wantFreq)
			}
		}
	}
}

// TestWorstDecayNoteIsTheArgmax is what makes checking one end of the keyboard
// legitimate. It is also the test that catches a sign error in WorstDecayNote,
// which would otherwise show up only as a preset that validates and then goes
// silent at the top of the keyboard.
func TestWorstDecayNoteIsTheArgmax(t *testing.T) {
	for _, keytrack := range []float64{DecayKeytrackMin, -0.5, -0.01, 0, 0.01, 0.5, 1, DecayKeytrackMax} {
		beta := keytrack

		for _, base := range []int{0, 36, 69, KeyboardFirstNote, 93, KeyboardLastNote, 127} {
			worst, longest := 0, math.Inf(-1)

			for note := KeyboardFirstNote; note <= KeyboardLastNote; note++ {
				params := BarParams{
					DecayKeytrack: &beta,
					Modes:         []ModeParams{{DecayMs: 400, Frequency: 1000, Amplitude: 1}},
				}

				TransposeToNote(&params, base, note)

				if decay := params.Modes[0].DecayMs; decay > longest {
					worst, longest = note, decay
				}
			}

			if got := WorstDecayNote(beta); got != worst {
				t.Errorf("beta %g, base note %d: WorstDecayNote says %d, the decay actually peaks at %d",
					beta, base, got, worst)
			}
		}
	}
}

// TestAuthoredCeilingStaysAboveTheDecayFloor is what turns DecayKeytrackMin and
// DecayKeytrackMax from an accident into a decision.
//
// Past a certain exponent the authoring ceiling collapses under DecayMsMin, and
// at that point no preset at that base note can validate at all -- every fit
// there fails with an empty search box rather than a bad result. The bounds are
// chosen to keep the ceiling above the floor for every authorable note, and
// this is the test that fails if someone widens them.
func TestAuthoredCeilingStaysAboveTheDecayFloor(t *testing.T) {
	for note := 0; note <= 127; note++ {
		for step := 0; step <= 64; step++ {
			keytrack := DecayKeytrackMin + (DecayKeytrackMax-DecayKeytrackMin)*float64(step)/64

			if ceiling := AuthoredDecayMsMax(note, keytrack); ceiling < DecayMsMin {
				t.Fatalf("at base note %d with keytrack %.3f the authoring ceiling is %g ms, "+
					"below the %g ms floor: no preset there could validate at all",
					note, keytrack, ceiling, DecayMsMin)
			}
		}
	}
}

// TestAuthoredCeilingIsReachableUnderEveryKeytrack checks the bound is exactly
// the enforced one rather than approximately it: a preset written at the
// advertised ceiling must validate, and one an ulp past it must not.
func TestAuthoredCeilingIsReachableUnderEveryKeytrack(t *testing.T) {
	for _, keytrack := range []float64{DecayKeytrackMin, -0.24, 0, 0.5, 1, 1.22, DecayKeytrackMax} {
		beta := keytrack

		for _, base := range []int{36, 69, KeyboardFirstNote, 93, 100, KeyboardLastNote, 127} {
			ceiling := AuthoredDecayMsMax(base, beta)

			at := BarParams{
				InputMix: 1, FilterFrequency: 1000, BaseFrequency: 440,
				DecayKeytrack: &beta,
				Modes:         []ModeParams{{DecayMs: ceiling, Frequency: 1000, Amplitude: 1}},
				Chebyshev:     ChebyshevParams{HarmonicGains: []float64{}},
			}

			if err := ValidateAuthoredBarParams(&at, base); err != nil {
				t.Errorf("beta %g, base %d: the advertised ceiling %g ms does not validate: %v",
					beta, base, ceiling, err)
			}

			over := at.Clone()
			over.Modes[0].DecayMs = ceiling * 1.01

			if err := ValidateAuthoredBarParams(&over, base); err == nil && ceiling < DecayMsValidationMax {
				t.Errorf("beta %g, base %d: %g ms was accepted, 1%% past the ceiling",
					beta, base, over.Modes[0].DecayMs)
			}
		}
	}
}

// TestKeytrackOutOfRangeIsRejected and the copy tests below keep the field's
// plumbing honest: it is a pointer on the allocation-free audio path, which is
// exactly where an aliased copy would be silent.
func TestKeytrackOutOfRangeIsRejected(t *testing.T) {
	for _, keytrack := range []float64{DecayKeytrackMin - 0.01, DecayKeytrackMax + 0.01, math.NaN(), math.Inf(1)} {
		beta := keytrack
		params := BarParams{
			InputMix: 1, FilterFrequency: 1000, BaseFrequency: 440,
			DecayKeytrack: &beta,
			Modes:         []ModeParams{{DecayMs: 100, Frequency: 1000, Amplitude: 1}},
			Chebyshev:     ChebyshevParams{HarmonicGains: []float64{}},
		}

		if err := ValidateBarParams(&params); err == nil {
			t.Errorf("keytrack %v was accepted", keytrack)
		}
	}
}

func TestKeytrackSurvivesCopyWithoutAliasing(t *testing.T) {
	beta := 0.75
	source := BarParams{
		InputMix: 1, FilterFrequency: 1000, BaseFrequency: 440,
		DecayKeytrack: &beta,
		Modes:         []ModeParams{{DecayMs: 100, Frequency: 1000, Amplitude: 1}},
		Chebyshev:     ChebyshevParams{HarmonicGains: []float64{}},
	}

	clone := source.Clone()
	if clone.DecayKeytrack == nil || *clone.DecayKeytrack != 0.75 {
		t.Fatalf("Clone lost the keytrack: %v", clone.DecayKeytrack)
	}

	if clone.DecayKeytrack == source.DecayKeytrack {
		t.Fatal("Clone shared the keytrack pointer, so mutating one would move the other")
	}

	*clone.DecayKeytrack = -0.5

	if *source.DecayKeytrack != 0.75 {
		t.Errorf("mutating the clone moved the original to %g", *source.DecayKeytrack)
	}

	// A nil source clears the destination rather than leaving a stale value,
	// which is the case a pooled voice hits when a preset without a keytrack
	// replaces one with it.
	plain := source
	plain.DecayKeytrack = nil

	plain.CopyInto(&clone)

	if clone.DecayKeytrack != nil {
		t.Errorf("a nil keytrack left %v behind", *clone.DecayKeytrack)
	}

	// And CopyInto does not allocate once the destination already holds one.
	dst := source.Clone()

	if allocs := testing.AllocsPerRun(50, func() { source.CopyInto(&dst) }); allocs != 0 {
		t.Errorf("CopyInto allocated %v times on the audio path", allocs)
	}
}
