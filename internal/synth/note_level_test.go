package synth

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/preset"
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

// TestTheLevelLawIsMeasuredNotAssumed is the evidence for building the trim
// table by rendering instead of by formula, kept as a test so the claim stays
// true rather than becoming a comment that once was.
//
// The shipped preset's level falls about 0.46 dB per semitone because its four
// modes beat against each other and how much of that beat pattern fits inside
// the decay grows with the decay. A single-mode preset has nothing to beat
// against and is therefore nearly flat. Any fixed curve fitted to one of them is
// wrong for the other, which is exactly the failure mode this asserts: the two
// presets disagree about the slope by more than an order of magnitude.
func TestTheLevelLawIsMeasuredNotAssumed(t *testing.T) {
	slopeOf := func(path string) float64 {
		t.Helper()

		p, err := preset.Load(filepath.FromSlash(path))
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}

		s, err := NewSynthesizer(p, 48000)
		if err != nil {
			t.Fatalf("NewSynthesizer for %s: %v", path, err)
		}

		// Least-squares slope of peak level in dB against semitone offset.
		var n, sx, sy, sxx, sxy float64

		for note := KeyboardFirstNote; note <= KeyboardLastNote; note++ {
			peak := s.peakForNote(note, 127)
			if peak <= 0 {
				continue
			}

			x := float64(note - p.Note)
			y := 20 * math.Log10(peak)

			n, sx, sy, sxx, sxy = n+1, sx+x, sy+y, sxx+x*x, sxy+x*y
		}

		return (n*sxy - sx*sy) / (n*sxx - sx*sx)
	}

	shipped := slopeOf("../../assets/presets/default.json")
	single := slopeOf("../../testdata/presets/minimal.json")

	if shipped > -0.3 {
		t.Errorf("the shipped preset's level slope is %.4f dB/semitone, expected a clear tilt", shipped)
	}

	if math.Abs(single) > math.Abs(shipped)/2 {
		t.Errorf("the single-mode preset tilts %.4f dB/semitone against the shipped preset's %.4f; "+
			"if these ever agree, a fixed per-note curve would become defensible and this test "+
			"should be revisited rather than deleted", single, shipped)
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
func TestOfflineRenderIsDeliberatelyNotLevelled(t *testing.T) {
	engine := newTestEngine(t)
	s := engine.synth

	low := peakOf(s.RenderNote(KeyboardFirstNote, 100, 2.0))
	own := peakOf(s.RenderNote(s.preset.Note, 100, 2.0))

	if low <= 0 || own <= 0 {
		t.Fatal("expected both notes to render")
	}

	// The untrimmed tilt is about 16 dB between these two notes. Asserting it
	// is still there is asserting that nothing quietly started levelling the
	// offline path.
	if tilt := 20 * math.Log10(low/own); tilt < 10 {
		t.Errorf("the offline path is only %.2f dB louder at note %d than at note %d; "+
			"if levelling was intentionally moved into RenderNote, the optimizer objective "+
			"and default_level_test.go have to be reconsidered with it",
			tilt, KeyboardFirstNote, s.preset.Note)
	}
}
