package synth

import (
	"math"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/assets"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/model"
)

// The tests in this file exist because every other test in the package renders
// at the preset's own note, where the transposition ratio is exactly 1. That
// blind spot is how the dead low register shipped: transposing down divides
// DecayMs by a ratio below 1, so note 36 turned the shipped preset's 188.2 ms
// mode into 1266.2 ms, ValidateBarParams refused it against a 500 ms ceiling,
// model.NewBar returned an error, and NoteOn dropped the note without a sound
// or a trace. Nothing in the suite constrained transposition at all, so nothing
// failed.
//
// Anything added here must therefore exercise notes away from p.Note, and the
// full keyboard rather than a sample of it: the failure was confined to the
// bottom 17 of the 61 playable keys, which any spot check near the middle of
// the range would have walked straight past.

// defaultPresetNote is the note the shipped preset was fitted at, and the base
// note both transposition paths -- the synthesizer's and the plugin's -- scale
// from.
const defaultPresetNote = 69

// silenceThreshold is deliberately crude, about -60 dBFS. These are not level
// assertions -- the peak spread across the keyboard is large and levelling it
// is separate, deliberate work -- they are assertions that the note rendered at
// all, and a note that did not render comes back as exact silence.
const silenceThreshold = 1e-3

// peakOf returns the largest absolute sample in the block, i.e. the render's
// peak level in linear units.
func peakOf(samples []float32) float64 {
	peak := 0.0

	for _, sample := range samples {
		if abs := math.Abs(float64(sample)); abs > peak {
			peak = abs
		}
	}

	return peak
}

// TestEveryKeyboardNoteRendersAudio strikes every note the player can reach and
// asserts each one produces sound.
//
// A dropped note comes back as a nil slice from RenderNoteWithOptions, because
// the voice could not be built; a note whose bar was built but never excited
// comes back as digital silence. Both are what this catches.
//
// It sweeps every embedded preset, not only the default one. The top of the
// keyboard is the mirror of the failure this file was written for and is
// guarded by nothing else: transposing up multiplies mode frequencies, so a
// preset whose highest mode passes model.FrequencyMaxHz at note 96 fails
// NewBar and is dropped exactly as the low register was. Nothing in preset
// validation catches it -- ValidateAuthoredBarParams transposes to the bottom
// key and looks only at the decay ceiling -- so a preset authored at note 69
// may not carry a mode above roughly 10.5 kHz, and this is where that is felt.
func TestEveryKeyboardNoteRendersAudio(t *testing.T) {
	ids, err := assets.IDs()
	if err != nil {
		t.Fatalf("assets.IDs failed: %v", err)
	}

	for _, id := range ids {
		p, err := assets.Preset(id)
		if err != nil {
			t.Fatalf("load preset %q: %v", id, err)
		}

		synthesizer, err := NewSynthesizer(p, 48000)
		if err != nil {
			t.Fatalf("NewSynthesizer(%q) failed: %v", id, err)
		}

		for note := KeyboardFirstNote; note <= KeyboardLastNote; note++ {
			rendered := synthesizer.RenderNote(note, 100, 1.0)
			if len(rendered) == 0 {
				t.Errorf("preset %q note %d rendered nothing: the voice could not be built", id, note)

				continue
			}

			if peak := peakOf(rendered); peak < silenceThreshold {
				t.Errorf("preset %q note %d rendered silence: peak %g", id, note, peak)
			}
		}
	}
}

// TestRealtimeEngineSoundsEveryKeyboardNote is the same claim through the audio
// path the browser and the plugin actually drive.
//
// It checks the engine's own dropped-note counter as well as the rendered
// block, because those are two different failures: a note that never became a
// voice increments the counter, while a note that became a voice and rendered
// nothing does not. Only the counter distinguishes "the engine refused the
// note" from "the note decayed away", which is the distinction that was missing
// entirely while NoteOn discarded the error.
// One engine is shared across the sweep rather than one per note: building an
// engine calibrates the level trims, which is 61 renders, and paying that 61
// times over would make this test the slowest in the package for no added
// coverage. Each note is drained before the next is struck, so the isolation
// that matters -- one voice at a time -- is preserved.
func TestRealtimeEngineSoundsEveryKeyboardNote(t *testing.T) {
	engine := newTestEngine(t)

	for note := KeyboardFirstNote; note <= KeyboardLastNote; note++ {
		before := engine.DroppedNoteOns()

		engine.NoteOn(note, 100)

		if dropped := engine.DroppedNoteOns() - before; dropped != 0 {
			t.Errorf("note %d: engine dropped the note-on (%d dropped, last dropped note %d)",
				note, dropped, engine.LastDroppedNote())

			continue
		}

		if engine.ActiveVoices() != 1 {
			t.Errorf("note %d: expected one active voice, got %d", note, engine.ActiveVoices())

			continue
		}

		peak := 0.0

		for block := 0; block < 4000 && engine.ActiveVoices() > 0; block++ {
			out := engine.ProcessBlock(defaultRealtimeBlockFrames)
			if blockPeak := peakOf(out); blockPeak > peak {
				peak = blockPeak
			}
		}

		if engine.ActiveVoices() != 0 {
			t.Errorf("note %d never retired, so the next note would sum on top of it", note)

			return
		}

		if peak < silenceThreshold {
			t.Errorf("note %d: engine rendered silence, peak %g", note, peak)
		}
	}
}

// TestDroppedNoteOnsCountsRefusedNotes pins the counter itself against a note
// the engine genuinely cannot build, so the sweep above cannot pass merely
// because the counter never moves.
//
// MIDI note 0 transposes the shipped preset down by 69 semitones, a ratio of
// 0.0183, which multiplies every decay by 54.6: the shipped preset's 188.2 ms
// mode becomes 10.3 seconds, past the validation ceiling. The note is outside
// the playable range on purpose -- the counter exists for the note-ons the
// engine has to refuse, and refusing a note no keyboard can send is correct.
func TestDroppedNoteOnsCountsRefusedNotes(t *testing.T) {
	engine := newTestEngine(t)

	if got := engine.DroppedNoteOns(); got != 0 {
		t.Fatalf("a fresh engine reports %d dropped note-ons, want 0", got)
	}

	engine.NoteOn(0, 100)

	if got := engine.DroppedNoteOns(); got != 1 {
		t.Fatalf("dropped note-ons = %d after an unbuildable note, want 1", got)
	}

	if got := engine.LastDroppedNote(); got != 0 {
		t.Fatalf("last dropped note = %d, want 0", got)
	}

	if got := engine.ActiveVoices(); got != 0 {
		t.Fatalf("a refused note left %d voices behind, want 0", got)
	}
}

// TestValidationCeilingAdmitsTheWorstCaseTransposition is the arithmetic behind
// model.DecayMsValidationMax, checked against what the ceiling has to clear
// rather than against the number itself.
//
// The worst case a preset can present is a mode sitting at the top of the
// box a fit at the default note searches -- the authoring ceiling for that
// note, which the optimizer narrows its decay box to -- transposed to the
// bottom of the playable keyboard. Every fit can produce such a preset, so
// the validation ceiling has to admit it -- otherwise the optimizer emits
// presets the synthesizer refuses to play, which is precisely the failure
// this change repairs.
func TestValidationCeilingAdmitsTheWorstCaseTransposition(t *testing.T) {
	// The transposition in scaledParamsForNote, restated here so the test
	// fails if either side of the relationship moves.
	ratio := math.Pow(2, float64(KeyboardFirstNote-defaultPresetNote)/12)
	worstCase := model.AuthoredDecayMsMax(defaultPresetNote, model.DecayKeytrackDefault) / ratio

	// A round trip through the ratio lands within an ulp of the ceiling.
	if worstCase > model.DecayMsValidationMax*(1+1e-12) {
		t.Fatalf("a preset at the authored ceiling (%g ms) transposed to note %d needs %.1f ms, "+
			"past the validation ceiling of %g ms",
			model.AuthoredDecayMsMax(defaultPresetNote, model.DecayKeytrackDefault), KeyboardFirstNote, worstCase, model.DecayMsValidationMax)
	}

	worstCase = math.Min(worstCase, model.DecayMsValidationMax)

	params := model.BarParams{
		InputMix:        1.0,
		FilterFrequency: 2000,
		BaseFrequency:   440,
		Modes: []model.ModeParams{
			{Amplitude: 0.5, Frequency: 440, DecayMs: worstCase},
		},
	}

	if err := model.ValidateBarParams(&params); err != nil {
		t.Fatalf("the worst-case decay of %.1f ms was rejected: %v", worstCase, err)
	}
}

// TestSearchBoundIsTwoSeconds pins the search box Phase 8.3 set. Raising the
// validation ceiling widens what a preset file may contain; it must not widen
// what a fit searches, which is this box narrowed to the note's authoring
// ceiling. The box itself is a physical claim: the one recording in the
// repository has a fundamental with a 677 ms half-life, and a box that cannot
// hold it cannot fit it.
func TestSearchBoundIsTwoSeconds(t *testing.T) {
	if model.DecayMsSearchMax != 2000.0 || model.DecayMsSearchMin != 0.5 {
		t.Fatalf("optimizer decay box = [%g, %g] ms, want [0.5, 2000]", model.DecayMsSearchMin, model.DecayMsSearchMax)
	}

	// The other half of this guard -- that the optimizer's search box actually
	// takes DecayMsSearchMax rather than the validation ceiling -- lives in
	// TestDefaultBoundsUseTheAuthoringCeiling over in internal/optimizer, which
	// is where the bounds moved when model stopped carrying them. It cannot be
	// asserted here: internal/optimizer imports this package.
}

// presetAtBaseNote builds a preset fitted at baseNote whose first mode sits
// exactly on the authoring ceiling for that note, i.e. the widest decay the
// format allows a preset in that position to carry.
func presetAtBaseNote(baseNote int, decayMs float64) *preset.Preset {
	return &preset.Preset{
		Version: preset.VersionV2,
		Name:    "base-note sweep",
		Note:    baseNote,
		Parameters: model.BarParams{
			InputMix:        1.0,
			FilterFrequency: 2000,
			BaseFrequency:   440,
			Modes: []model.ModeParams{
				{Amplitude: 0.5, Frequency: 440, DecayMs: decayMs},
				{Amplitude: 0.3, Frequency: 1174, DecayMs: decayMs / 4},
			},
		},
	}
}

// sweptBaseNotes spans the keyboard and past both ends of it. Note 76 is in the
// list for a specific reason: it is the first base note at which a decay inside
// the optimizer's own search box no longer fits under the validation ceiling at
// the bottom key, so it is where a preset that looks entirely ordinary stops
// being playable.
var sweptBaseNotes = []int{36, 48, 60, 69, 75, 76, 84, 96, 100}

// TestValidPresetsPlayEveryKeyboardNoteAtEveryBaseNote is the invariant the
// authoring ceiling exists to establish: whatever base note a preset was
// authored at, if preset.Validate accepts it then every key sounds.
//
// The sweep over base notes is the whole point. The previous version of this
// suite fixed the base note at 69, which is exactly the assumption that let the
// bug through -- DecayMsValidationMax was derived from note 69 and therefore
// guaranteed nothing for a preset authored anywhere else. A preset at note 100
// with a 500 ms decay passed validation and was refused at note 36, which is
// the dead low register all over again.
func TestValidPresetsPlayEveryKeyboardNoteAtEveryBaseNote(t *testing.T) {
	for _, baseNote := range sweptBaseNotes {
		p := presetAtBaseNote(baseNote, model.AuthoredDecayMsMax(baseNote, model.DecayKeytrackDefault))

		if err := preset.Validate(p); err != nil {
			t.Errorf("base note %d: a preset at its own ceiling was rejected: %v", baseNote, err)

			continue
		}

		synthesizer, err := NewSynthesizer(p, 48000)
		if err != nil {
			t.Errorf("base note %d: NewSynthesizer refused a valid preset: %v", baseNote, err)

			continue
		}

		for note := KeyboardFirstNote; note <= KeyboardLastNote; note++ {
			rendered := synthesizer.RenderNote(note, 100, 0.2)
			if len(rendered) == 0 {
				t.Errorf("base note %d, note %d: the voice could not be built", baseNote, note)

				continue
			}

			if peak := peakOf(rendered); peak < silenceThreshold {
				t.Errorf("base note %d, note %d: rendered silence, peak %g", baseNote, note, peak)
			}
		}
	}
}

// TestPresetsPastTheirBaseNoteCeilingAreRefusedAtLoad is the other half: the
// presets the sweep above cannot cover are rejected outright, with a diagnostic,
// rather than loading and then dropping notes silently.
//
// A dropped note-on is the failure mode this whole change exists to kill, and
// refusing the file is the only way to kill it for a preset the format cannot
// play at all. The error therefore has to name the base note, since the decay
// the author wrote is legal for a preset positioned lower.
func TestPresetsPastTheirBaseNoteCeilingAreRefusedAtLoad(t *testing.T) {
	for _, baseNote := range sweptBaseNotes {
		ceiling := model.AuthoredDecayMsMax(baseNote, model.DecayKeytrackDefault)
		p := presetAtBaseNote(baseNote, ceiling*1.01)

		err := preset.Validate(p)
		if err == nil {
			t.Errorf("base note %d: %g ms was accepted, %g ms past the ceiling",
				baseNote, ceiling*1.01, ceiling*0.01)

			continue
		}

		if !strings.Contains(err.Error(), "decay_ms") {
			t.Errorf("base note %d: the error does not name the offending field: %v", baseNote, err)
		}
	}
}

// TestSearchBoundIsAuthorableOnlyUpToNote94 states, as an assertion, the fact
// the old derivation of DecayMsValidationMax silently assumed away: the
// optimizer's decay box and the authoring ceiling agree only over part of the
// keyboard. Above note 94 the box is wider than a preset at that position may
// carry, which is why the objective narrows it to the ceiling for its note.
//
// This is a real edge rather than a curiosity, and pinning it is what keeps the
// two constants honest with each other: if either moves, this test says where
// the crossover went. It has moved once already -- the crossover was note 51
// while the keyboard bottomed out at MIDI 36, and followed the bottom key up to
// 94 when the keyboard became a glockenspiel's G5..C8. That is the whole point
// of pinning it: the number is a consequence of two constants, not a fact about
// the instrument, and it should be re-derived rather than preserved.
const searchBoundCrossoverNote = 94

func TestSearchBoundIsAuthorableOnlyUpToNote94(t *testing.T) {
	if got := model.AuthoredDecayMsMax(searchBoundCrossoverNote, model.DecayKeytrackDefault); got < model.DecayMsSearchMax {
		t.Errorf("at base note %d the ceiling is %g ms, below the search bound %g",
			searchBoundCrossoverNote, got, model.DecayMsSearchMax)
	}

	if got := model.AuthoredDecayMsMax(searchBoundCrossoverNote+1, model.DecayKeytrackDefault); got >= model.DecayMsSearchMax {
		t.Errorf("at base note %d the ceiling is %g ms, still at or above the search bound %g",
			searchBoundCrossoverNote+1, got, model.DecayMsSearchMax)
	}

	// And the crossover really is where the constant says, rather than the
	// constant merely being consistent with two lucky notes.
	for note := model.KeyboardFirstNote; note <= model.KeyboardLastNote; note++ {
		wide := model.AuthoredDecayMsMax(note, model.DecayKeytrackDefault) >= model.DecayMsSearchMax
		if want := note <= searchBoundCrossoverNote; wide != want {
			t.Fatalf("note %d: ceiling %g ms is wider-than-search=%v, want %v -- the crossover is not %d",
				note, model.AuthoredDecayMsMax(note, model.DecayKeytrackDefault), wide, want, searchBoundCrossoverNote)
		}
	}
}

// TestBaseFrequencyDoesNotReachTheAudio pins the property the optimizer's
// codec leans on when it writes the starting preset's base_frequency through
// rather than searching it.
//
// base_frequency is carried through TransposeToNote and range-checked by
// ValidateBarParams, and that is the whole of its life in the model: Bar builds
// its oscillators from each mode's own Frequency, so nothing downstream ever
// reads it. Until Phase 8.3 internal/optimizer encoded a mode's frequency as
// log10(Frequency / BaseFrequency), which made the base a gauge freedom -- the
// fit that produced the shipped preset drifted it to 1499 Hz and the recipe
// said to set it back to 440 by hand. The codec now encodes frequencies in
// hertz and leaves the base alone.
//
// Setting it to anything has to be free, and this says it is. The notes are the
// two ends of the keyboard plus the preset's own, because transposition scales
// base_frequency by the same ratio as everything else, so a note far from the
// preset's is where an accidental dependency would show first.
func TestBaseFrequencyDoesNotReachTheAudio(t *testing.T) {
	shipped := loadTestPreset(t)

	drifted := shipped.Clone()
	drifted.Parameters.BaseFrequency = 1499.2075119356841

	if drifted.Parameters.BaseFrequency == shipped.Parameters.BaseFrequency {
		t.Fatal("the two presets carry the same base_frequency, so this proves nothing")
	}

	for _, note := range []int{KeyboardFirstNote, shipped.Note, KeyboardLastNote} {
		want := renderWholeNote(t, shipped, note)
		got := renderWholeNote(t, drifted, note)

		if len(got) != len(want) {
			t.Fatalf("note %d: %d samples against %d", note, len(got), len(want))
		}

		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("note %d: sample %d is %v, want %v", note, i, got[i], want[i])
			}
		}
	}
}

func renderWholeNote(t *testing.T, p *preset.Preset, note int) []float32 {
	t.Helper()

	synthesizer, err := NewSynthesizer(p, 44100)
	if err != nil {
		t.Fatalf("NewSynthesizer: %v", err)
	}

	return synthesizer.RenderNote(note, 100, 1.0)
}
