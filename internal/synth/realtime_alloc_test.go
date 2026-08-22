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

// TestProcessBlockDoesNotAllocateAfterFirstBlock pins the audio path against a
// callback wider than the engine's own block: it costs one growth of the mix
// buffer and nothing per block thereafter.
//
// The voice buffers do not grow with it. A callback wider than the
// synthesizer's block size is rendered as several passes of at most that size,
// so a slot buffer sized at construction is always large enough and the growth
// branch in mapLanes stays a guard rather than a thing that fires. The second
// assertion is that guard's other half: whatever the callback width, the slots
// must still be holding the buffers the constructor gave them.
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

	for i := range engine.voices {
		if got := cap(engine.voices[i].buffer); got != defaultRealtimeBlockFrames {
			t.Fatalf("voice %d carries a %d-frame buffer after a %d-frame block, want %d",
				i, got, frames, defaultRealtimeBlockFrames)
		}
	}
}

// TestNoteOnAllocatesNothing is the note-on half of Phase 2's "no allocation on
// the audio path": a note-on allocates nothing at all, not merely nothing on
// top of the voice it used to build.
//
// The earlier form of this test compared NoteOn against NewVoice, because
// constructing a voice cost 18 allocations -- a transposed BarParams, a bar,
// its oscillator bank -- and making that free was a separate piece of work.
// It is done: the engine pools a voice per slot and restrikes it in place
// through Synthesizer.ResetVoice, so there is no construction left to allow for.
//
// All three arms of NoteOn are measured, because they take different paths to a
// slot and only the retrigger arm would be exercised by striking one note over
// and over.
func TestNoteOnAllocatesNothing(t *testing.T) {
	engine := newTestEngine(t)

	// Retrigger: the note is already in a slot, so NoteOn finds it and
	// restrikes it in place.
	engine.NoteOn(72, 100)

	if allocs := testing.AllocsPerRun(20, func() { engine.NoteOn(72, 100) }); allocs != 0 {
		t.Fatalf("retriggering a sounding note allocated %.1f times, want 0", allocs)
	}

	// Fresh slots: each note claims a slot that has never sounded, until the
	// engine is full. AllocsPerRun would retrigger, so the sweep is measured as
	// a whole rather than per note-on.
	fresh := newTestEngine(t)
	fresh.maxVoices = 8

	allocs := testing.AllocsPerRun(1, func() {
		for note := 60; note < 68; note++ {
			fresh.NoteOn(note, 100)
		}
	})

	if allocs != 0 {
		t.Fatalf("filling every voice slot allocated %.1f times, want 0", allocs)
	}

	if fresh.ActiveVoices() != fresh.maxVoices {
		t.Fatalf("expected the engine to be full, got %d voices", fresh.ActiveVoices())
	}

	// Voice stealing: the engine is full and none of these notes is sounding,
	// so each one rotates the oldest slot to the back and restrikes it there.
	// The two batches are disjoint and alternate, so no note in a batch is
	// already sounding when it is struck and every one of the eight is a steal
	// rather than a retrigger.
	batch := 0

	stealAllocs := testing.AllocsPerRun(4, func() {
		base := 80 + batch%2*8
		batch++

		for note := base; note < base+8; note++ {
			fresh.NoteOn(note, 100)
		}
	})

	if stealAllocs != 0 {
		t.Fatalf("stealing a voice allocated %.1f times, want 0", stealAllocs)
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
