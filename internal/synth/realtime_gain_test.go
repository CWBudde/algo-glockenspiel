package synth

import (
	"math"
	"testing"
)

// TestMasterGainReachesARingingNote is the regression test for the bug where
// SetMasterGain only affected notes struck afterwards: the pan coefficients had
// the master gain baked into them at note-on, so turning the volume down left
// every sounding note at its old level.
//
// It renders one block, halves the gain, renders the next block from the same
// voice, and compares that block against the same voice on a second engine left
// at the original gain. Comparing the second block against the first would only
// show that the output got quieter, which a decaying note does on its own;
// comparing against an untouched reference engine isolates the gain change from
// the decay and gives an exact ratio to assert.
func TestMasterGainReachesARingingNote(t *testing.T) {
	p := loadTestPreset(t)

	const (
		blockFrames = 128
		note        = 72
		velocity    = 100
		initialGain = 0.8
		loweredGain = 0.4
		relativeTol = 1e-5
	)

	newEngine := func() *RealtimeEngine {
		s, err := NewSynthesizer(p, 48000)
		if err != nil {
			t.Fatalf("NewSynthesizer failed: %v", err)
		}

		engine := NewRealtimeEngine(s)
		engine.SetMasterGain(initialGain)
		engine.NoteOn(note, velocity)

		return engine
	}

	engine := newEngine()
	reference := newEngine()

	firstPeak, _ := blockStats(engine.ProcessBlock(blockFrames))
	if firstPeak == 0 {
		t.Fatal("expected the first block to carry signal")
	}

	if referencePeak, _ := blockStats(reference.ProcessBlock(blockFrames)); referencePeak != firstPeak {
		t.Fatalf("the two engines diverged before the gain change: %g vs %g", firstPeak, referencePeak)
	}

	engine.SetMasterGain(loweredGain)

	lowered := engine.ProcessBlock(blockFrames)
	unchanged := reference.ProcessBlock(blockFrames)

	if len(lowered) != len(unchanged) {
		t.Fatalf("block length mismatch: got %d want %d", len(lowered), len(unchanged))
	}

	wantRatio := float64(loweredGain) / float64(initialGain)
	compared := 0

	for i := range unchanged {
		// Skip samples too small to carry a meaningful ratio: the block crosses
		// zero repeatedly, and near a crossing float32 cancellation dominates.
		if math.Abs(float64(unchanged[i])) < 1e-5 {
			continue
		}

		ratio := float64(lowered[i]) / float64(unchanged[i])
		if math.Abs(ratio-wantRatio) > relativeTol {
			t.Fatalf("sample %d scaled by %g, want %g (lowered=%g unchanged=%g)",
				i, ratio, wantRatio, lowered[i], unchanged[i])
		}

		compared++
	}

	if compared == 0 {
		t.Fatal("no samples were large enough to compare")
	}
}

// TestGainsForNoteStayInRangeAcrossEveryMIDINote sweeps the whole MIDI range
// rather than only the keyboard's 36..96.
//
// The bug this guards against was exactly an out-of-range one: the mapping was
// calibrated for 24 semitones starting at note 72 and clamped nothing, so note
// 36 -- an ordinary note on a keyboard that starts at 36 -- produced a pan of
// -2.48, a left gain of 1.74 and a right gain of -0.74, i.e. over unity on one
// side and phase-inverted on the other. A note-on carrying a MIDI value outside
// the keyboard entirely must not be able to do that either.
func TestGainsForNoteStayInRangeAcrossEveryMIDINote(t *testing.T) {
	for note := 0; note <= 127; note++ {
		left, right := gainsForNote(note)

		if left < 0 || left > 1 {
			t.Fatalf("note %d: left gain = %g, want within [0, 1]", note, left)
		}

		if right < 0 || right > 1 {
			t.Fatalf("note %d: right gain = %g, want within [0, 1]", note, right)
		}

		// A pan law redistributes energy; it must not create or destroy it.
		if sum := float64(left) + float64(right); math.Abs(sum-1) > 1e-6 {
			t.Fatalf("note %d: gains sum to %g, want 1", note, sum)
		}
	}
}

// TestGainsForNoteSpansTheKeyboardRange pins the mapping to the span the UI
// draws, and pins the clamping at both ends.
func TestGainsForNoteSpansTheKeyboardRange(t *testing.T) {
	lowLeft, lowRight := gainsForNote(KeyboardFirstNote)
	if lowLeft <= lowRight {
		t.Fatalf("lowest note should sit left of centre: left=%g right=%g", lowLeft, lowRight)
	}

	highLeft, highRight := gainsForNote(KeyboardLastNote)
	if highRight <= highLeft {
		t.Fatalf("highest note should sit right of centre: left=%g right=%g", highLeft, highRight)
	}

	// The span is symmetric, so the two extremes mirror each other exactly.
	if math.Abs(float64(lowLeft-highRight)) > 1e-6 || math.Abs(float64(lowRight-highLeft)) > 1e-6 {
		t.Fatalf("keyboard extremes are not mirrored: low=(%g, %g) high=(%g, %g)",
			lowLeft, lowRight, highLeft, highRight)
	}

	centre := (KeyboardFirstNote + KeyboardLastNote) / 2

	centreLeft, centreRight := gainsForNote(centre)
	if math.Abs(float64(centreLeft-centreRight)) > 1e-6 {
		t.Fatalf("middle of the keyboard should be centred: left=%g right=%g", centreLeft, centreRight)
	}

	// Out-of-range note-ons clamp to the ends rather than running off the
	// scale, which is what makes the [0, 1] guarantee above hold.
	for _, pair := range [][2]int{{0, KeyboardFirstNote}, {127, KeyboardLastNote}} {
		outLeft, outRight := gainsForNote(pair[0])
		edgeLeft, edgeRight := gainsForNote(pair[1])

		if outLeft != edgeLeft || outRight != edgeRight {
			t.Fatalf("note %d should clamp to note %d: got (%g, %g) want (%g, %g)",
				pair[0], pair[1], outLeft, outRight, edgeLeft, edgeRight)
		}
	}
}
