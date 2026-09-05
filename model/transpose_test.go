package model

import (
	"math"
	"testing"
)

// The tests in this file vary the base note on purpose. The bug they exist for
// survived a full round of review precisely because every test that touched
// decay validation used base note 69: DecayMsValidationMax was derived from
// that one note, the derivation was checked at that one note, and the fact that
// Preset.Note is authorable across the whole MIDI range never entered any
// assertion. A ceiling that is a constant while the constraint it stands for is
// a function of the base note cannot be validated at a single base note.

// authoredTestParams returns a one-mode parameter set carrying decayMs.
func authoredTestParams(decayMs float64) BarParams {
	return BarParams{
		InputMix:        1.0,
		FilterFrequency: 2000,
		BaseFrequency:   440,
		Modes: []ModeParams{
			{Amplitude: 0.5, Frequency: 440, DecayMs: decayMs},
		},
	}
}

// baseNotesUnderTest spans the authorable MIDI range and deliberately includes
// 75 and 76, the pair that straddles the point where DecayMsSearchMax stops
// being an authorable decay: at note 76 a decay inside the optimizer's own
// search box already exceeds the validation ceiling at the bottom key.
var baseNotesUnderTest = []int{0, 36, 60, 69, 75, 76, 96, 100, 127}

// TestAuthoredCeilingIsExactlyReachable pins the boundary from below: a decay
// sitting exactly on the ceiling for its base note must validate.
//
// This is not a tautology, and it is the reason ValidateAuthoredBarParams
// transposes for real instead of comparing against AuthoredDecayMsMax. The
// ceiling is DecayMsValidationMax * r and the check divides by the same r, and
// in floating point (V*r)/r is not identically V. Restating the bound in a
// second place would let the two disagree by an ulp at exactly the value an
// author who read the error message would type in.
func TestAuthoredCeilingIsExactlyReachable(t *testing.T) {
	for _, baseNote := range baseNotesUnderTest {
		params := authoredTestParams(AuthoredDecayMsMax(baseNote, DecayKeytrackDefault))

		if err := ValidateAuthoredBarParams(&params, baseNote); err != nil {
			t.Errorf("base note %d: the ceiling itself (%g ms) was rejected: %v",
				baseNote, AuthoredDecayMsMax(baseNote, DecayKeytrackDefault), err)
		}
	}
}

// TestAuthoredCeilingRejectsWhatTheBottomKeyCannotBuild pins it from above.
func TestAuthoredCeilingRejectsWhatTheBottomKeyCannotBuild(t *testing.T) {
	for _, baseNote := range baseNotesUnderTest {
		ceiling := AuthoredDecayMsMax(baseNote, DecayKeytrackDefault)
		params := authoredTestParams(ceiling * 1.001)

		if err := ValidateAuthoredBarParams(&params, baseNote); err == nil {
			t.Errorf("base note %d: %g ms was accepted, though it is past the %g ms ceiling",
				baseNote, ceiling*1.001, ceiling)
		}
	}
}

// TestAuthoredCeilingTracksTheBaseNote is the claim the constant cannot make.
// A single decay is legal or illegal depending only on where the preset sits,
// so the same two-second mode has to be accepted low on the keyboard and
// refused high on it.
func TestAuthoredCeilingTracksTheBaseNote(t *testing.T) {
	tests := []struct {
		baseNote int
		decayMs  float64
		wantOK   bool
	}{
		// Below the bottom key nothing is transposed down, so the plain
		// validation ceiling is the only one that applies.
		{baseNote: 36, decayMs: DecayMsSearchMax, wantOK: true},
		{baseNote: 69, decayMs: DecayMsSearchMax, wantOK: true},
		{baseNote: KeyboardFirstNote, decayMs: DecayMsSearchMax, wantOK: true},
		{baseNote: KeyboardFirstNote, decayMs: DecayMsValidationMax, wantOK: true},
		{baseNote: KeyboardFirstNote, decayMs: DecayMsValidationMax + 1, wantOK: false},
		// The crossover with the search box, either side of note 94.
		{baseNote: 94, decayMs: DecayMsSearchMax, wantOK: true},
		{baseNote: 95, decayMs: DecayMsSearchMax, wantOK: false},
		// And the ceiling tightening as the preset climbs.
		{baseNote: 100, decayMs: 1486, wantOK: true},
		{baseNote: 100, decayMs: 1487, wantOK: false},
		{baseNote: 108, decayMs: 936, wantOK: true},
		{baseNote: 108, decayMs: 937, wantOK: false},
		{baseNote: 127, decayMs: DecayMsSearchMax, wantOK: false},
		{baseNote: 127, decayMs: 312, wantOK: true},
	}

	for _, test := range tests {
		params := authoredTestParams(test.decayMs)

		err := ValidateAuthoredBarParams(&params, test.baseNote)
		if gotOK := err == nil; gotOK != test.wantOK {
			t.Errorf("base note %d, %g ms: accepted = %t, want %t (err: %v)",
				test.baseNote, test.decayMs, gotOK, test.wantOK, err)
		}
	}
}

// TestAuthoredValidationIsStrictlyStrongerThanBarValidation states the split the
// review asked for as an assertion rather than as prose: the two constraints are
// different, and the authored one is the tighter of the pair everywhere above
// the bottom key.
func TestAuthoredValidationIsStrictlyStrongerThanBarValidation(t *testing.T) {
	// Legal as a transposed parameter set, illegal as a preset at note 100.
	params := authoredTestParams(2000)

	if err := ValidateBarParams(&params); err != nil {
		t.Fatalf("2000 ms is inside the validation ceiling, so ValidateBarParams must accept it: %v", err)
	}

	if err := ValidateAuthoredBarParams(&params, 100); err == nil {
		t.Fatalf("a 2000 ms decay at base note 100 was accepted, but it is %.0f ms at note %d",
			2000*math.Pow(2, float64(100-KeyboardFirstNote)/12), KeyboardFirstNote)
	}

	// At the bottom key the two coincide: nothing is transposed down any further.
	if err := ValidateAuthoredBarParams(&params, KeyboardFirstNote); err != nil {
		t.Fatalf("at base note %d the two constraints are the same one: %v", KeyboardFirstNote, err)
	}
}

// TestAuthoredDecayMsMaxMatchesTheTranspositionLaw checks the advertised bound
// against the arithmetic the synthesizer actually performs, so the number in an
// error message cannot drift away from the number being enforced.
func TestAuthoredDecayMsMaxMatchesTheTranspositionLaw(t *testing.T) {
	for _, baseNote := range baseNotesUnderTest {
		params := authoredTestParams(AuthoredDecayMsMax(baseNote, DecayKeytrackDefault))
		TransposeToNote(&params, baseNote, KeyboardFirstNote)

		// Below KeyboardFirstNote the bound is the plain ceiling and the bottom
		// key transposes up, so the result lands under it rather than on it.
		want := DecayMsValidationMax
		if baseNote < KeyboardFirstNote {
			want = AuthoredDecayMsMax(baseNote, DecayKeytrackDefault) / math.Pow(2, float64(KeyboardFirstNote-baseNote)/12)
		}

		if got := params.Modes[0].DecayMs; math.Abs(got-want) > 1e-6 {
			t.Errorf("base note %d: the ceiling transposes to %g ms at note %d, want %g",
				baseNote, got, KeyboardFirstNote, want)
		}
	}
}

// TestTransposeToNoteIsAnIdentityAtItsOwnNote guards the shortcut in
// TransposeToNote: a preset played at the note it was fitted at must come back
// untouched, bit for bit, not merely multiplied by a ratio that rounds to one.
func TestTransposeToNoteIsAnIdentityAtItsOwnNote(t *testing.T) {
	params := authoredTestParams(188.223281860352)
	original := params.Clone()

	TransposeToNote(&params, 69, 69)

	if params.BaseFrequency != original.BaseFrequency ||
		params.Modes[0].Frequency != original.Modes[0].Frequency ||
		params.Modes[0].DecayMs != original.Modes[0].DecayMs {
		t.Fatalf("transposing to the base note changed the params: %+v vs %+v", params.Modes[0], original.Modes[0])
	}
}

// TestTransposeToNoteScalesBothDirections pins the decay law itself: down is
// longer, up is shorter, and an octave is a factor of two either way.
func TestTransposeToNoteScalesBothDirections(t *testing.T) {
	tests := []struct {
		name      string
		toNote    int
		wantDecay float64
	}{
		{name: "an octave down doubles the decay", toNote: 57, wantDecay: 400},
		{name: "an octave up halves it", toNote: 81, wantDecay: 100},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := authoredTestParams(200)
			TransposeToNote(&params, 69, test.toNote)

			if got := params.Modes[0].DecayMs; math.Abs(got-test.wantDecay) > 1e-9 {
				t.Fatalf("decay = %g ms, want %g", got, test.wantDecay)
			}
		})
	}
}

// TestAuthoredCeilingCrossesTheSearchBoxAtNote95 pins the numbers the package
// overview quotes, and the direction of the relationship they illustrate.
//
// DecayMsSearchMax is not an authoring bound, and the clearest evidence is that
// it is on the wrong side of the real one for the top of the keyboard: above
// note 94 the box reaches past what a preset at that base note may legally
// carry, which is why the optimizer narrows its box to AuthoredDecayMsMax for
// the note it fits at. A reader who takes the two for the same thing gets a
// dead low register below the crossover and spurious rejections above it, so
// both halves are worth failing a build over.
//
// The crossover sits at 94/95 rather than the 51/52 this test pinned until the
// keyboard became a glockenspiel's G5..C8. It moved by exactly the 43 semitones
// the bottom key did, and the two ceiling values either side of it are
// unchanged to the last digit -- which is the tidiest possible evidence that
// this is one relationship expressed in two places and not two facts.
func TestAuthoredCeilingCrossesTheSearchBoxAtNote95(t *testing.T) {
	for _, tc := range []struct {
		note int
		want float64
	}{
		// Anything at or below the bottom key transposes nowhere lower, so the
		// authoring ceiling is the plain validation ceiling.
		{note: 36, want: DecayMsValidationMax},
		{note: 69, want: DecayMsValidationMax},
		{note: KeyboardFirstNote, want: DecayMsValidationMax},
		{note: 94, want: 2102.24},
		{note: 95, want: 1984.25},
		{note: 100, want: 1486.51},
		{note: KeyboardLastNote, want: 936.44},
		{note: 127, want: 312.50},
	} {
		if got := AuthoredDecayMsMax(tc.note, DecayKeytrackDefault); math.Abs(got-tc.want) > 0.01 {
			t.Errorf("AuthoredDecayMsMax(%d) = %.2f ms, want %.2f", tc.note, got, tc.want)
		}
	}

	if AuthoredDecayMsMax(94, DecayKeytrackDefault) <= DecayMsSearchMax {
		t.Errorf("the authoring ceiling at note 94 (%.2f ms) should still exceed the search box (%g ms)",
			AuthoredDecayMsMax(94, DecayKeytrackDefault), DecayMsSearchMax)
	}

	if AuthoredDecayMsMax(95, DecayKeytrackDefault) >= DecayMsSearchMax {
		t.Errorf("the authoring ceiling at note 95 (%.2f ms) should have fallen below the search box (%g ms)",
			AuthoredDecayMsMax(95, DecayKeytrackDefault), DecayMsSearchMax)
	}
}
