package synth

import "testing"

// TestAWideCallbackRendersItsWholeBlock pins the property the web demo needed
// and did not have: ProcessBlock must fill every frame it was asked for.
//
// A voice renders at most the synthesizer's block size per pass, and the engine
// used to make exactly one pass per callback. A host that asked for more -- the
// demo's ScriptProcessor asks for 512 against a 128-sample block -- got 128
// samples of note followed by 384 of silence, every callback, which is a 94 Hz
// gate rather than a note. The two assertions below are the two halves of that:
// the tail is not silent, and the wide callback is sample-for-sample what the
// narrow ones produce.
func TestAWideCallbackRendersItsWholeBlock(t *testing.T) {
	const (
		wide     = 512
		narrow   = 128
		note     = 72
		velocity = 100
		blocks   = 8
	)

	wideOut := renderNote(t, wide, blocks, note, velocity)
	narrowOut := renderNote(t, narrow, blocks*wide/narrow, note, velocity)

	if len(wideOut) != len(narrowOut) {
		t.Fatalf("frame counts differ: wide %d, narrow %d", len(wideOut), len(narrowOut))
	}

	// The tail of the first callback is where the truncation showed. Checking
	// it separately says which failure this is when both assertions break.
	silent := true

	for i := narrow * 2; i < wide*2; i++ {
		if wideOut[i] != 0 {
			silent = false

			break
		}
	}

	if silent {
		t.Errorf("frames %d..%d of the first callback are silent: the block was truncated", narrow, wide)
	}

	for i := range wideOut {
		if wideOut[i] != narrowOut[i] {
			t.Fatalf("sample %d differs: wide %v, narrow %v", i, wideOut[i], narrowOut[i])
		}
	}
}

// renderNote strikes one note and concatenates the interleaved output of the
// given number of callbacks of the given width.
func renderNote(t *testing.T, frames, blocks, note, velocity int) []float32 {
	t.Helper()

	engine := newTestEngine(t)
	engine.NoteOn(note, velocity)

	out := make([]float32, 0, frames*blocks*2)
	for range blocks {
		out = append(out, engine.ProcessBlock(frames)...)
	}

	return out
}

// TestRetirementIsUnaffectedByCallbackWidth covers the whole life of a note,
// past the point where it retires and its lane is handed back.
//
// A callback wider than one pass retires voices once at the end rather than
// between passes, so a voice that finishes in an early pass keeps its lane for
// the rest of the callback. That is a deliberate choice -- compacting the voice
// list per pass costs more than the lane gather it would save -- and it is only
// defensible if it cannot be heard. This is that claim: a 512-frame callback is
// sample-for-sample four 128-frame ones, retirement included.
//
// It does not pin the empty-bank skip, and no output-level test can: a bank
// whose lanes are all held by finished voices is fed silence either way, and
// the rotor state it would advance belongs to voices that never render again
// and is cleared when the lane is reused. Skipping it saves work and changes
// nothing audible. Verified by mutation, along with retiring between passes and
// running the passes one sample short -- none of the three moves a sample, so
// this test is an equivalence guard rather than a detector for any of them.
func TestRetirementIsUnaffectedByCallbackWidth(t *testing.T) {
	const (
		wide     = 512
		narrow   = 128
		note     = 72
		velocity = 100

		// Comfortably past defaultVoiceDuration at 48 kHz, so the run covers
		// the note's retirement and the silence after it.
		wideBlocks = 400
	)

	wideOut := renderNote(t, wide, wideBlocks, note, velocity)
	narrowOut := renderNote(t, narrow, wideBlocks*wide/narrow, note, velocity)

	if len(wideOut) != len(narrowOut) {
		t.Fatalf("frame counts differ: wide %d, narrow %d", len(wideOut), len(narrowOut))
	}

	for i := range wideOut {
		if wideOut[i] != narrowOut[i] {
			t.Fatalf("sample %d of %d differs: wide %v, narrow %v", i, len(wideOut), wideOut[i], narrowOut[i])
		}
	}
}
