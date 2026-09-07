package synth

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

// wantKeyboardSpreadDB is the level spread the realtime engine is allowed
// across the playable keyboard, peak to peak.
//
// The measured figure is 4.08 dB, and all of it is the pan law: the loudest
// channel of a note panned hard to one side carries 0.8 of its energy while a
// centred note splits 0.5/0.5, and 20*log10(0.8/0.5) = 4.08 dB exactly. The
// level trim itself contributes nothing measurable -- the same 4.08 dB comes
// out of testdata/presets/minimal, whose untrimmed spread is a completely
// different shape. The bound is set just above the measurement rather than at a
// round number, so that a regression in either law has to move it.
//
// This is not a target for further reduction. Narrowing it would mean flattening
// the pan, which redistributes energy on purpose and preserves it: left + right
// is 1 for every note. What was worth removing was the 27.78 dB of modal level
// tilt, which was not a pan and did not preserve anything.
const wantKeyboardSpreadDB = 4.5

// TestRealtimeKeyboardIsLevelAndUnclipped is the outcome assertion for the
// per-note level law, run at the worst case the engine can be driven at:
// maximum master gain and maximum MIDI velocity.
//
// Before the law, this range clipped. At the engine's *default* master gain of
// 0.7 and an ordinary velocity of 100, MIDI notes 36..50 hit the limiter in
// ProcessBlock -- 33425 clipped samples on note 36 alone -- because the shipped
// preset renders 27.78 dB louder at the bottom of the keyboard than at the top.
// Raising the validation ceiling turned 17 silent keys into 15 distorted ones;
// this is what turns them into 15 notes that play.
func TestRealtimeKeyboardIsLevelAndUnclipped(t *testing.T) {
	engine := newTestEngine(t)
	engine.SetMasterGain(1)

	minDB, maxDB := math.Inf(1), math.Inf(-1)

	for note := KeyboardFirstNote; note <= KeyboardLastNote; note++ {
		engine.NoteOn(note, 127)

		if engine.ActiveVoices() != 1 {
			t.Fatalf("note %d: expected exactly one voice, got %d -- the notes must not overlap "+
				"or this measures a sum rather than a note", note, engine.ActiveVoices())
		}

		peak := 0.0
		clipped := 0

		// Drain the note completely before striking the next one. The cap is
		// generous enough for the longest note on the keyboard and is a
		// runaway guard, not a window: a note still sounding when it expires
		// fails below.
		for block := 0; block < 4000 && engine.ActiveVoices() > 0; block++ {
			for _, sample := range engine.ProcessBlock(defaultRealtimeBlockFrames) {
				abs := math.Abs(float64(sample))
				if abs > peak {
					peak = abs
				}

				// hardClip pins a clipped sample to exactly +/-1, so equality
				// against full scale is the clipping test.
				if abs >= 1 {
					clipped++
				}
			}
		}

		if engine.ActiveVoices() != 0 {
			t.Fatalf("note %d never retired", note)
		}

		if clipped != 0 {
			t.Errorf("note %d clipped %d samples at peak %.6f", note, clipped, peak)
		}

		if peak < silenceThreshold {
			t.Errorf("note %d rendered silence: peak %g", note, peak)
		}

		db := 20 * math.Log10(peak)
		minDB, maxDB = math.Min(minDB, db), math.Max(maxDB, db)
	}

	if spread := maxDB - minDB; spread > wantKeyboardSpreadDB {
		t.Errorf("keyboard level spread = %.2f dB (%.2f to %.2f dBFS), want at most %.2f dB",
			spread, minDB, maxDB, wantKeyboardSpreadDB)
	}
}

// TestNoteTrimsLevelEveryNoteToTheReference is the law itself, checked directly
// rather than through the mix: every note, multiplied by its trim, has to land
// on the level of the preset's own note.
//
// The tolerance is float32 rounding plus nothing. The trim is measured from
// exactly the render it is asserted against, so anything larger would mean the
// table is not being applied, is indexed wrongly, or was clamped.
func TestNoteTrimsLevelEveryNoteToTheReference(t *testing.T) {
	const toleranceDB = 0.01

	engine := newTestEngine(t)

	reference := engine.synth.peakForNote(engine.synth.preset.Note, 127)
	if reference <= 0 {
		t.Fatal("the preset is silent at its own note")
	}

	for note := KeyboardFirstNote; note <= KeyboardLastNote; note++ {
		trimmed := engine.synth.peakForNote(note, 127) * float64(engine.trimForNote(note))

		if deviation := math.Abs(20 * math.Log10(trimmed/reference)); deviation > toleranceDB {
			t.Errorf("note %d sits %.3f dB off the reference after trimming (%.6f vs %.6f)",
				note, deviation, trimmed, reference)
		}
	}
}

// TestNoteTrimIsUnityAtThePresetsOwnNote pins the choice of reference. A preset
// is authored, fitted and level-checked at one note; normalising to that note is
// what keeps this change a redistribution of the *other* notes rather than a
// global volume change nobody asked for.
func TestNoteTrimIsUnityAtThePresetsOwnNote(t *testing.T) {
	engine := newTestEngine(t)

	if trim := engine.trimForNote(engine.synth.preset.Note); math.Abs(float64(trim)-1) > 1e-6 {
		t.Fatalf("trim at the preset's own note = %g, want 1", trim)
	}

	// A note-on outside the keyboard gets no trim rather than an extrapolated
	// one, which is the same refusal to invent a value that gainsForNote makes
	// when it clamps an out-of-range pan.
	for _, note := range []int{0, KeyboardFirstNote - 1, KeyboardLastNote + 1, 127} {
		if trim := engine.trimForNote(note); trim != 1 {
			t.Fatalf("trim at out-of-keyboard note %d = %g, want 1", note, trim)
		}
	}
}

// TestTrimTableIsFiniteAndUnclamped is the guard on the assumption every other
// level test rests on: that calibrateNoteTrims produced a usable table at all.
//
// It earns its place because minimal.json is authored at note 69, which is
// *below* the keyboard since that became the glockenspiel's G5..C8. Its trims
// are therefore all boosts -- every playable note is quieter than the reference
// note, which is not itself playable -- and it asks for up to +20.5 dB. That is
// well inside the +/-36 dB clamp, but it is close enough to it that a future
// preset or a future range could push a note onto the clamp, and a clamped trim
// is a note the engine has quietly given up on levelling. The clamp exists for
// pathology, so nothing shipped should reach it.
//
// The two shipped presets used to be the alarming pair here for the same reason.
// Both were labelled note 69 while sounding two octaves higher, and re-declaring
// them at the note they sound, MIDI 93, moved their reference note inside the
// keyboard: measured on 2026-09-07 at 44.1 kHz, default.json now asks for -6.8 dB
// at the bottom key and +9.8 dB at the top, and recorded-bar.json -1.7 dB to
// +5.9 dB. A reference note the player can actually strike is what turns a table
// of boosts into a table that cuts as well as boosts, and it is the reason the
// only preset still near the clamp is the one fixture that is not an instrument.
func TestTrimTableIsFiniteAndUnclamped(t *testing.T) {
	for _, path := range []string{
		"../../assets/presets/default.json",
		"../../assets/presets/recorded-bar.json",
		"../../testdata/presets/minimal.json",
	} {
		p, err := preset.Load(filepath.FromSlash(path))
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}

		synthesizer, err := NewSynthesizer(p, 44100)
		if err != nil {
			t.Fatalf("NewSynthesizer for %s: %v", path, err)
		}

		trims := calibrateNoteTrims(synthesizer)
		if want := KeyboardLastNote - KeyboardFirstNote + 1; len(trims) != want {
			t.Fatalf("%s: trim table holds %d notes, want %d", path, len(trims), want)
		}

		for i, trim := range trims {
			note := KeyboardFirstNote + i

			if math.IsNaN(float64(trim)) || math.IsInf(float64(trim), 0) || trim <= 0 {
				t.Fatalf("%s: trim at note %d is %g, which is not a usable gain", path, note, trim)
			}

			// The clamp is 1/64..64. Anything within a hair of it is a note the
			// trim gave up on rather than levelled.
			if trim <= 1.0/64+1e-6 || trim >= 64-1e-6 {
				t.Errorf("%s: trim at note %d is %g, on the clamp -- that note is not levelled",
					path, note, trim)
			}
		}
	}
}

// TestTheLevelLawIsMeasuredNotAssumed is the evidence for building the trim
// table by rendering instead of by formula, kept as a test so the claim stays
// true rather than becoming a comment that once was.
//
// Any fixed curve fitted to one preset is wrong for the others, and that is what
// this asserts. What it does *not* assert is a particular reason, because the
// reason has changed twice.
//
// Over the old 36..96 keyboard the mechanism was mode beating: the shipped preset
// fell about 0.46 dB per semitone because its four modes beat against each other
// and how much of that pattern fits inside the decay grows as the decay does,
// while minimal.json -- whose modes are exact harmonics of 440 Hz, so the sum is
// periodic -- was flat to within 0.3 dB. The test asserted that flatness.
//
// Over 79..108 the excitation lowpass took over. FilterFrequency is absolute and
// TransposeToNote deliberately leaves it alone, so how far a preset's modes have
// slid past their own cutoff by the top key is what sets the tilt, and that is a
// property of the individual file.
//
// The measurement was a least-squares slope in dB per semitone until the two
// A4-labelled presets were re-declared at the note they sound, MIDI 93. That
// moved default.json's modes two octaves down against its fixed 1303.7 Hz cutoff
// and steepened its tilt from -0.49 to -0.62 dB/semitone, close enough to
// minimal.json's -0.69 that the old "at least 0.1 apart" bound failed -- while
// the thing the bound was a proxy for got no weaker at all. The slope was the
// wrong instrument: it collapses a whole curve to one number and two presets can
// share a slope while disagreeing everywhere along it.
//
// So the divergence is measured directly now, as the largest gap in dB between
// two presets' level curves once each is referred to its own level at the bottom
// key -- which is exactly how wrong a curve fitted to one is when used for
// another. Measured on 2026-09-07 at 48 kHz: default against minimal 2.83 dB,
// default against recorded-bar 12.26 dB, minimal against recorded-bar 15.09 dB,
// every one of them at the top key. The bound is 2 dB, still nearly seven times
// the 0.3 dB the trims are expected to hold a note to, so a regression has to be
// substantial rather than incidental.
func TestTheLevelLawIsMeasuredNotAssumed(t *testing.T) {
	// curveOf returns peak level in dB at every playable note, referred to the
	// bottom key. Referring each preset to its own bottom key is what makes the
	// comparison a comparison of *shapes*: an overall level difference between
	// two presets is not something a per-note curve would ever have to explain.
	curveOf := func(path string) []float64 {
		t.Helper()

		p, err := preset.Load(filepath.FromSlash(path))
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}

		s, err := NewSynthesizer(p, 48000)
		if err != nil {
			t.Fatalf("NewSynthesizer for %s: %v", path, err)
		}

		curve := make([]float64, 0, KeyboardLastNote-KeyboardFirstNote+1)

		for note := KeyboardFirstNote; note <= KeyboardLastNote; note++ {
			peak := s.peakForNote(note, 127)
			if peak <= 0 {
				t.Fatalf("%s renders silence at note %d, so it has no level curve", path, note)
			}

			curve = append(curve, 20*math.Log10(peak))
		}

		for i := len(curve) - 1; i >= 0; i-- {
			curve[i] -= curve[0]
		}

		return curve
	}

	paths := []string{
		"../../assets/presets/default.json",
		"../../assets/presets/recorded-bar.json",
		"../../testdata/presets/minimal.json",
	}

	curves := make([][]float64, len(paths))
	for i, path := range paths {
		curves[i] = curveOf(path)
	}

	// The keyboard has to be worth levelling at all before disagreement about
	// how to level it means anything.
	for i, path := range paths {
		if span := math.Abs(curves[i][len(curves[i])-1]); span < 3 {
			t.Errorf("%s falls only %.2f dB from the bottom key to the top; "+
				"with a keyboard that flat there would be nothing for the trims to do", path, span)
		}
	}

	for i := range paths {
		for j := i + 1; j < len(paths); j++ {
			worst, at := 0.0, KeyboardFirstNote

			for k := range curves[i] {
				if d := math.Abs(curves[i][k] - curves[j][k]); d > worst {
					worst, at = d, KeyboardFirstNote+k
				}
			}

			if worst < 2 {
				t.Errorf("%s and %s never diverge by more than %.2f dB (worst at note %d), "+
					"closer than the 2 dB bound; if these ever agree, a fixed per-note curve "+
					"would become defensible and this test should be revisited rather than deleted",
					paths[i], paths[j], worst, at)
			}
		}
	}
}

// TestOfflineRenderIsDeliberatelyNotLevelled pins the decision that the trim
// belongs to the realtime engine alone.
//
// Three reasons, and all three are load-bearing:
//
//   - internal/optimizer/objective.go fits against RenderNote's output. Changing
//     what that returns moves the objective, so every existing checkpoint and
//     every fitted preset would silently mean something different.
//   - default_level_test.go pins the offline peak of the shipped preset near
//     -3 dBFS, which is a statement about the preset, not about playback.
//   - the CLI renders one note at a time to a file. There is no mix bus, nothing
//     to clip against, and --normalize-gain already exists for callers who want
//     a level chosen for them.
//
// Only the realtime engine sums many notes onto a shared bus behind a limiter,
// so it is the only place where the spread turns into distortion. Two paths at
// different levels is a decision; this is where it is written down.
//
// It used to assert this by measuring the tilt between note 36 and note 69 and
// requiring at least 10 dB of it -- a proxy, and one that depended on the
// shipped preset having a big enough spread rather than on the property being
// tested. The re-fit landed at 9.70 dB and it failed, having found nothing
// wrong. The assertion is now the property itself: every offline peak is the
// untrimmed one, checked against the trim table that would have to have been
// applied, with a separate guard that the table is not all ones.
//
// Verified by mutation. Levelling the measured peak by the trim, which is what
// moving the trim into RenderNote would do, fails it with "note 36 renders
// 9.692 dB off its untrimmed level offline" and 60 more like it.
func TestOfflineRenderIsDeliberatelyNotLevelled(t *testing.T) {
	engine := newTestEngine(t)
	synthesizer := engine.synth

	// The trim table has to be doing something, or everything below is true of
	// a no-op and proves nothing. This is the only preset-dependent number
	// here, and it is a floor rather than a target: 0.5 dB against a shipped
	// spread of 27 dB.
	widestTrimDB := 0.0

	for note := KeyboardFirstNote; note <= KeyboardLastNote; note++ {
		if deviation := math.Abs(20 * math.Log10(float64(engine.trimForNote(note)))); deviation > widestTrimDB {
			widestTrimDB = deviation
		}
	}

	if widestTrimDB < 0.5 {
		t.Fatalf("the widest trim is %.3f dB, so the table levels nothing and this test "+
			"cannot tell a levelled offline path from an unlevelled one", widestTrimDB)
	}

	// The claim itself: every offline peak is the untrimmed one. If the trim
	// were ever applied inside RenderNote, the peaks would already sit at the
	// reference and dividing by the trim would move them off it.
	reference := synthesizer.peakForNote(synthesizer.preset.Note, 127)
	if reference <= 0 {
		t.Fatal("the preset is silent at its own note")
	}

	const toleranceDB = 0.01

	for note := KeyboardFirstNote; note <= KeyboardLastNote; note++ {
		peak := synthesizer.peakForNote(note, 127)
		if peak <= 0 {
			continue
		}

		untrimmed := reference / float64(engine.trimForNote(note))
		if deviation := math.Abs(20 * math.Log10(peak/untrimmed)); deviation > toleranceDB {
			t.Errorf("note %d renders %.3f dB off its untrimmed level offline (%.6f against %.6f); "+
				"if levelling was intentionally moved into RenderNote, the optimizer objective "+
				"and default_level_test.go have to be reconsidered with it",
				note, deviation, peak, untrimmed)
		}
	}
}
