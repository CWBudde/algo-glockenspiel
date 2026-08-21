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

// sameBuffer reports whether two slices share a backing array.
func sameBuffer(a, b []float32) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}

	return &a[0] == &b[0]
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

// TestNoteOnAllocatesNothingBeyondTheVoice pins the fix for the per-note-on
// block buffer: what NoteOn allocates is what constructing the voice costs, and
// nothing on top of it.
//
// Constructing a voice is not free -- it builds a transposed bar -- and making
// it free is not this change's job. The comparison against NewVoice is what
// makes the assertion stable regardless of what that costs.
func TestNoteOnAllocatesNothingBeyondTheVoice(t *testing.T) {
	engine := newTestEngine(t)

	const note = 72

	engine.NoteOn(note, 100)

	voiceAllocs := testing.AllocsPerRun(20, func() {
		if _, err := engine.synth.NewVoice(note, 100, engine.noteDuration, engine.renderOptions); err != nil {
			t.Fatalf("NewVoice failed: %v", err)
		}
	})

	noteOnAllocs := testing.AllocsPerRun(20, func() {
		engine.NoteOn(note, 100)
	})

	if noteOnAllocs > voiceAllocs {
		t.Fatalf("NoteOn allocated %.1f times, the voice it builds costs %.1f", noteOnAllocs, voiceAllocs)
	}
}

// TestNoteOnKeepsVoiceSlotBuffers covers two of the three paths that hand a
// slot a new stream -- retrigger and voice stealing -- and checks each keeps
// the buffer the slot already owned. The third, reclaiming a slot a retired
// voice left behind, needs a real retirement and lives in
// TestRetirementThroughProcessBlockKeepsBuffersDistinct.
func TestNoteOnKeepsVoiceSlotBuffers(t *testing.T) {
	engine := newTestEngine(t)
	engine.maxVoices = 3

	engine.NoteOn(72, 100)

	first := engine.voices[0].buffer
	if len(first) != defaultRealtimeBlockFrames {
		t.Fatalf("expected a preallocated block buffer, got len %d", len(first))
	}

	engine.NoteOn(72, 100)

	if !sameBuffer(first, engine.voices[0].buffer) {
		t.Fatal("retriggering the same note dropped the slot's buffer")
	}

	engine.NoteOn(73, 100)
	engine.NoteOn(74, 100)

	stolen := engine.voices[0].buffer

	engine.NoteOn(75, 100)

	if engine.ActiveVoices() != 3 {
		t.Fatalf("expected the voice list to stay at maxVoices, got %d", engine.ActiveVoices())
	}

	last := engine.voices[len(engine.voices)-1]
	if last.note != 75 {
		t.Fatalf("expected the stolen slot to carry the new note, got %d", last.note)
	}

	if !sameBuffer(stolen, last.buffer) {
		t.Fatal("voice stealing dropped the stolen slot's buffer")
	}
}

// TestRetirementThroughProcessBlockKeepsBuffersDistinct drives the swap-based
// retirement in ProcessBlock instead of truncating the slice by hand, which is
// the only way to cover it: the compaction is what could overwrite a retired
// slot's buffer and leave two live voices rendering into one backing array.
func TestRetirementThroughProcessBlockKeepsBuffersDistinct(t *testing.T) {
	engine := newTestEngine(t)

	// Two voices that retire inside the block loop and one that outlives it,
	// in that order: the survivor has to be moved down past both dead slots,
	// which is the only arrangement that exercises the compaction at all.
	engine.noteDuration = 0.01

	engine.NoteOn(73, 100)
	engine.NoteOn(74, 100)

	engine.noteDuration = defaultVoiceDuration

	engine.NoteOn(72, 100)

	if engine.ActiveVoices() != 3 {
		t.Fatalf("expected three voices before the block loop, got %d", engine.ActiveVoices())
	}

	slots := engine.voices[:cap(engine.voices)]
	survivorBuffer := engine.voices[2].buffer
	shortBuffers := [][]float32{engine.voices[0].buffer, engine.voices[1].buffer}

	for block := 0; engine.ActiveVoices() > 1 && block < 200; block++ {
		engine.ProcessBlock(defaultRealtimeBlockFrames)
	}

	if engine.ActiveVoices() != 1 {
		t.Fatalf("expected the short voices to retire, %d still active", engine.ActiveVoices())
	}

	if engine.voices[0].note != 72 {
		t.Fatalf("the wrong voice survived: note %d", engine.voices[0].note)
	}

	if !sameBuffer(survivorBuffer, engine.voices[0].buffer) {
		t.Fatal("the compaction moved the survivor onto a different buffer")
	}

	assertDistinctBuffers(t, slots[:3])

	// The retired slots keep their buffers past the end of the list, so the
	// next two note-ons get them back rather than allocating.
	engine.NoteOn(75, 100)
	engine.NoteOn(76, 100)

	for _, reclaimed := range engine.voices[1:3] {
		if !sameBuffer(reclaimed.buffer, shortBuffers[0]) && !sameBuffer(reclaimed.buffer, shortBuffers[1]) {
			t.Fatal("a reclaimed slot did not get one of the retired buffers back")
		}
	}

	assertDistinctBuffers(t, engine.voices)
}

// TestEngineOwnsEveryVoiceBufferFromTheStart is the deterministic half of the
// note-on allocation claim: no slot can allocate on the audio thread, because
// every slot the engine will ever hand out already has its buffer.
func TestEngineOwnsEveryVoiceBufferFromTheStart(t *testing.T) {
	engine := newTestEngine(t)

	slots := engine.voices[:cap(engine.voices)]
	if len(slots) != defaultRealtimeMaxVoices {
		t.Fatalf("expected room for %d voices, got %d", defaultRealtimeMaxVoices, len(slots))
	}

	for i := range slots {
		if got := len(slots[i].buffer); got != defaultRealtimeBlockFrames {
			t.Fatalf("slot %d starts with a %d-frame buffer, want %d", i, got, defaultRealtimeBlockFrames)
		}
	}

	assertDistinctBuffers(t, slots)
}

// assertDistinctBuffers fails unless every slot owns its own backing array.
func assertDistinctBuffers(t *testing.T, slots []realtimeVoice) {
	t.Helper()

	for i := range slots {
		for j := i + 1; j < len(slots); j++ {
			if sameBuffer(slots[i].buffer, slots[j].buffer) {
				t.Fatalf("voice slots %d and %d share a buffer", i, j)
			}
		}
	}
}
