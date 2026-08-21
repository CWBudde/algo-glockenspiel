package synth

import "testing"

func newTestEngine(t *testing.T) *RealtimeEngine {
	t.Helper()

	p := loadTestPreset(t)

	s, err := NewSynthesizer(p, 48000)
	if err != nil {
		t.Fatalf("NewSynthesizer failed: %v", err)
	}

	return NewRealtimeEngine(s)
}

// TestProcessBlockDoesNotAllocateAfterFirstBlock pins the buffer growth: a
// block wider than the voice buffer must allocate once, not once per block.
//
// The growth used to be written to a per-iteration copy of the voice struct,
// and survived only because the compaction wrote that copy back into the slice
// afterwards. This is the assertion that keeps it surviving on purpose.
func TestProcessBlockDoesNotAllocateAfterFirstBlock(t *testing.T) {
	engine := newTestEngine(t)

	const frames = defaultRealtimeBlockFrames * 2

	for note := 72; note < 76; note++ {
		engine.NoteOn(note, 100)
	}

	engine.ProcessBlock(frames)

	allocs := testing.AllocsPerRun(20, func() {
		engine.ProcessBlock(frames)
	})

	if allocs != 0 {
		t.Fatalf("ProcessBlock allocated %.1f times per block at %d frames, want 0", allocs, frames)
	}

	if engine.ActiveVoices() == 0 {
		t.Fatal("all voices retired during the run, so the measurement covered nothing")
	}

	// The direct form of the same claim, and the one that still catches the bug
	// on a compiler that manages to keep the discarded buffer on the stack:
	// after a wide block the growth has to be visible in the engine's own slots.
	for i := range engine.voices {
		if got := cap(engine.voices[i].buffer); got < frames {
			t.Fatalf("voice %d still carries a %d-frame buffer after a %d-frame block", i, got, frames)
		}
	}
}
