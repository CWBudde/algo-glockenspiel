package model

import (
	"fmt"
	"math"
)

// KeyboardFirstNote and KeyboardLastNote are the MIDI notes at the two ends of
// the playable keyboard. They live here, rather than next to the engine that
// reads MIDI, because they are what makes a preset *valid*: a preset is only
// well-formed if it still describes a buildable bar at every note the player
// can strike, and that check has to run in the package that owns the bounds.
//
// They are the range ValidateAuthoredBarParams and AuthoredDecayMsMax reason
// over, so a host that lays out a different keyboard should treat them as this
// instrument's declared span rather than as its own.
//
// The span is the orchestral glockenspiel's real sounding range, G5 to C8, and
// that is a correctness property rather than a cosmetic one. It used to be
// 36..96, C2 to C7, which is not an instrument anybody builds: it put the
// bottom key four octaves below anything a glockenspiel plays, and since
// validation transposes a preset *down* to the bottom key before checking its
// decays, every real bar blew up there. The hollandm reference pack measures
// half-lives to 808 ms at MIDI 85; at note 36 that is 13.7 s against a 5 s
// ceiling, so thirteen of its twenty bars were unauthorable. At note 79 the
// same bar needs 1.1 s and the ceiling never binds. The top key moved for the
// mirror reason: that pack reaches MIDI 103 and the old keyboard stopped at 96,
// so seven of its notes were outside the instrument altogether.
const (
	KeyboardFirstNote = 79
	KeyboardLastNote  = 108
)

// TransposeToNote rescales params in place from fromNote to toNote.
//
// This is the single definition of what transposition means, and every caller
// that transposes has to go through it. That is not tidiness: preset validation
// decides whether a preset is playable by transposing it to the bottom of the
// keyboard and validating the result, so if validation and playback computed
// the ratio even slightly differently, a preset could pass validation by a
// fraction of an ulp and still be refused by NewBar at the note it was cleared
// for. Sharing the arithmetic makes the two bit-identical.
//
// Dividing DecayMs by the ratio is the physically right thing: a bar an octave
// lower rings roughly twice as long, so transposing down lengthens the decay.
// The consequence is that the decays reaching NewBar are systematically wider
// than the ones the preset file holds -- see [DecayMsValidationMax].
//
// params is mutated rather than copied so that the audio path can transpose
// into a buffer it already owns. Callers that must not disturb their source
// transpose a [BarParams.Clone] of it.
func TransposeToNote(params *BarParams, fromNote, toNote int) {
	if params == nil || fromNote == toNote {
		return
	}

	ratio := math.Pow(2, float64(toNote-fromNote)/12)

	params.BaseFrequency *= ratio

	for i := range params.Modes {
		params.Modes[i].Frequency *= ratio

		if ratio > 0 {
			params.Modes[i].DecayMs /= ratio
		}
	}
}

// AuthoredDecayMsMax returns the widest decay a mode may carry in a preset
// authored at baseNote.
//
// It is the authoring-side counterpart to [DecayMsValidationMax] and, unlike
// it, is not a constant: transposing down multiplies every decay by
// 2^((baseNote-note)/12), so how much decay a preset may be written with
// depends entirely on how far down the keyboard reaches beneath its base note.
// A preset at or below the bottom key may use the full 5000 ms, one at note 94
// may use 2102 ms, and one at note 108 only 936 ms -- all three ring for the
// same five seconds once played at the bottom key, which is the quantity the
// ceiling actually bounds.
//
// [ValidateAuthoredBarParams] uses it only to tell an author what the bound is.
// It does not decide anything: that decision is made by transposing the preset
// for real and reading the decay off the result, so validation and the
// synthesizer can never disagree about where the boundary sits.
func AuthoredDecayMsMax(baseNote int) float64 {
	// Below KeyboardFirstNote nothing is transposed down at all -- the bottom
	// key transposes such a preset *up*, which shortens its decays -- so the
	// bound there is the plain ceiling the params must clear as written. Without
	// the min, a preset at note 0 would be told it may carry 40 s of decay, a
	// figure ValidateBarParams rejects on sight.
	return math.Min(DecayMsValidationMax, DecayMsValidationMax*math.Pow(2, float64(KeyboardFirstNote-baseNote)/12))
}

// ValidateAuthoredBarParams validates params as written in a preset authored at
// baseNote, which is a strictly stronger claim than [ValidateBarParams] makes.
//
// The two are separate constraints and neither implies the other.
// ValidateBarParams asks whether one already-transposed parameter set can be
// handed to NewBar. This asks whether a preset can be handed to the *player*:
// whether the decays it carries still clear DecayMsValidationMax at the bottom
// of the keyboard, where transposition stretches them furthest. That is what a
// preset file has to promise and what nothing checked before.
//
// The gap between the two was a real bug, not a theoretical one. A preset at
// note 69 with a 1000 ms decay passed preset validation, reached NewBar as
// 6727 ms at note 36, and went silent -- exactly the dead low register this
// change set out to repair, moved from the shipped preset to an authored one.
// A preset authored anywhere above note 75 did the same thing with a decay well
// inside the optimizer's own search box: 500 ms at note 100 is 20159 ms at
// note 36.
// No value of DecayMsValidationMax can prevent that on its own, because the
// stretch factor is a function of the base note and the ceiling does not know
// it.
//
// Only KeyboardFirstNote is checked, and only the decay ceiling. Transposing
// down is monotonic, so the lowest playable note is the worst case for that
// bound and clearing it clears every note above. Two bounds at the *other* end
// of the keyboard can also be violated -- transposing up shrinks decays toward
// DecayMsMin and pushes mode frequencies toward FrequencyMaxHz, and a preset
// carrying either extreme will still be refused at the top of the keyboard.
// Both are the same class of defect and neither is checked here, because
// closing them means making the optimizer's search box keyboard-aware at its
// decay floor and its frequency ceiling, which is a separate change with its
// own risk to every fit. They are also far harder to hit by accident: a 0.1 ms
// decay or a 50 kHz mode is degenerate on its own terms, whereas a 500 ms decay
// is what the optimizer is told to search for.
func ValidateAuthoredBarParams(params *BarParams, baseNote int) error {
	if err := ValidateBarParams(params); err != nil {
		return err
	}

	// Transposing a clone, rather than comparing against a precomputed ceiling,
	// is what makes this bit-exact with playback: the value checked here is the
	// same float the synthesizer will hand to NewBar, not a bound restated in a
	// second place that could disagree by an ulp at the boundary.
	transposed := params.Clone()
	TransposeToNote(&transposed, baseNote, KeyboardFirstNote)

	for i := range transposed.Modes {
		if decay := transposed.Modes[i].DecayMs; decay > DecayMsValidationMax {
			return fmt.Errorf(
				"modes[%d].decay_ms: %g ms at base note %d becomes %g ms at note %d, past the %g ms ceiling"+
					" (a preset at note %d may carry at most %.1f ms)",
				i, params.Modes[i].DecayMs, baseNote, decay, KeyboardFirstNote, DecayMsValidationMax,
				baseNote, AuthoredDecayMsMax(baseNote),
			)
		}
	}

	return nil
}
