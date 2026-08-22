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
		params := authoredTestParams(AuthoredDecayMsMax(baseNote))

		if err := ValidateAuthoredBarParams(&params, baseNote); err != nil {
			t.Errorf("base note %d: the ceiling itself (%g ms) was rejected: %v",
				baseNote, AuthoredDecayMsMax(baseNote), err)
		}
	}
}

// TestAuthoredCeilingRejectsWhatTheBottomKeyCannotBuild pins it from above.
func TestAuthoredCeilingRejectsWhatTheBottomKeyCannotBuild(t *testing.T) {
	for _, baseNote := range baseNotesUnderTest {
		ceiling := AuthoredDecayMsMax(baseNote)
		params := authoredTestParams(ceiling * 1.001)

		if err := ValidateAuthoredBarParams(&params, baseNote); err == nil {
			t.Errorf("base note %d: %g ms was accepted, though it is past the %g ms ceiling",
				baseNote, ceiling*1.001, ceiling)
		}
	}
}

// TestAuthoredCeilingTracksTheBaseNote is the claim the constant cannot make.
// A single decay is legal or illegal depending only on where the preset sits,
// so the same 500 ms mode has to be accepted low on the keyboard and refused
// high on it.
func TestAuthoredCeilingTracksTheBaseNote(t *testing.T) {
	tests := []struct {
		baseNote int
		decayMs  float64
		wantOK   bool
	}{
		{baseNote: 36, decayMs: DecayMsSearchMax, wantOK: true},
		{baseNote: 69, decayMs: DecayMsSearchMax, wantOK: true},
		{baseNote: 69, decayMs: 743, wantOK: true},
		{baseNote: 69, decayMs: 744, wantOK: false},
		{baseNote: 69, decayMs: 1000, wantOK: false},
		{baseNote: 75, decayMs: DecayMsSearchMax, wantOK: true},
		{baseNote: 76, decayMs: DecayMsSearchMax, wantOK: false},
		{baseNote: 100, decayMs: DecayMsSearchMax, wantOK: false},
		{baseNote: 100, decayMs: 124, wantOK: true},
		{baseNote: 127, decayMs: DecayMsSearchMax, wantOK: false},
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
	// Legal as a transposed parameter set, illegal as a preset at note 69.
	params := authoredTestParams(1000)

	if err := ValidateBarParams(&params); err != nil {
		t.Fatalf("1000 ms is inside the validation ceiling, so ValidateBarParams must accept it: %v", err)
	}

	if err := ValidateAuthoredBarParams(&params, 69); err == nil {
		t.Fatal("a 1000 ms decay at base note 69 was accepted, but it is 6727 ms at note 36")
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
		params := authoredTestParams(AuthoredDecayMsMax(baseNote))
		TransposeToNote(&params, baseNote, KeyboardFirstNote)

		// Below KeyboardFirstNote the bound is the plain ceiling and the bottom
		// key transposes up, so the result lands under it rather than on it.
		want := DecayMsValidationMax
		if baseNote < KeyboardFirstNote {
			want = AuthoredDecayMsMax(baseNote) / math.Pow(2, float64(KeyboardFirstNote-baseNote)/12)
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
