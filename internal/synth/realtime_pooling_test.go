package synth

import "testing"

// The engine pools one voice per slot and restrikes it in place, so a note is
// no longer rendered by a bar that was built for it and nothing else. The bar
// carries state -- oscillator phase and amplitude, and the excitation lowpass's
// delay line -- and model.Bar.setLowpass deliberately retunes the filter
// *without* clearing that delay line, because a parameter change mid-note is
// not a discontinuity in the signal. A new note is one. Synthesizer.ResetVoice
// therefore calls Bar.Reset after UpdateParams, and the tests below are what
// pin that it does: each renders a note through a slot that has already played
// something else and requires the result to be bit-identical to the same note
// on a voice built from scratch.
//
// Bit-identical rather than close, because there is no tolerance to pick that
// distinguishes "the reset works" from "the leak is small". Any leak of the
// previous note's state changes the first samples of the new one, and equality
// catches it whatever its size.

const (
	poolingBlocks = 64

	// dirtyBlocks is how much of the previous note a slot plays before it is
	// reused, and it is deliberately one block rather than a comfortable
	// stretch. The oscillator bank is re-seeded by UpdateParams, so the state
	// that survives a missing Reset is the excitation lowpass's delay line --
	// and that is charged by a single-sample strike and decays fast. Reusing
	// the slot 40 ms in leaves so little of it that the leak rounds away in
	// float32 and the test passes with Bar.Reset deleted. One block catches it.
	dirtyBlocks = 1
)

func newTestSynthesizer(t *testing.T) *Synthesizer {
	t.Helper()

	s, err := NewSynthesizer(loadTestPreset(t), 48000)
	if err != nil {
		t.Fatalf("NewSynthesizer failed: %v", err)
	}

	return s
}

// renderVoiceBlocks renders a fixed number of blocks from a voice, returning
// them concatenated. A voice that retires early simply contributes fewer
// samples, which the comparison then requires of both sides alike.
func renderVoiceBlocks(v *Voice, blocks int) []float32 {
	out := make([]float32, 0, blocks*defaultBlockSize)
	buf := make([]float32, defaultBlockSize)

	for i := 0; i < blocks && v.Active(); i++ {
		n := v.RenderInto(buf)
		out = append(out, buf[:n]...)
	}

	return out
}

// freshVoiceReference renders a note on a voice that has never played anything
// else, which is what a pooled slot has to reproduce exactly.
func freshVoiceReference(t *testing.T, synthesizer *Synthesizer, note, velocity int) []float32 {
	t.Helper()

	voice, err := synthesizer.NewVoice(note, velocity, defaultVoiceDuration, RenderOptions{AutoStop: true, DecayDBFS: defaultVoiceDecayDBFS})
	if err != nil {
		t.Fatalf("NewVoice(%d) failed: %v", note, err)
	}

	return renderVoiceBlocks(voice, poolingBlocks)
}

func assertIdentical(t *testing.T, what string, got, want []float32) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("%s rendered %d samples, the fresh voice rendered %d", what, len(got), len(want))
	}

	if len(got) == 0 {
		t.Fatalf("%s rendered nothing, so the comparison covered nothing", what)
	}

	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s differs from a fresh voice at sample %d: %v vs %v", what, i, got[i], want[i])
		}
	}
}

// TestPooledVoiceMatchesAFreshVoiceForADifferentNote is the central claim of
// voice pooling: reusing a voice for another note is indistinguishable from
// building one.
func TestPooledVoiceMatchesAFreshVoiceForADifferentNote(t *testing.T) {
	synthesizer := newTestSynthesizer(t)

	const (
		firstNote  = 60
		secondNote = 79
		velocity   = 100
	)

	want := freshVoiceReference(t, synthesizer, secondNote, velocity)

	pooled, err := synthesizer.NewVoice(firstNote, velocity, defaultVoiceDuration, RenderOptions{AutoStop: true, DecayDBFS: defaultVoiceDecayDBFS})
	if err != nil {
		t.Fatalf("NewVoice(%d) failed: %v", firstNote, err)
	}

	// Render only part of the first note, so the voice is reset with the bar
	// mid-ring: full oscillators, a charged filter delay line and a non-zero
	// quiet-block count are exactly the state that could leak.
	if got := renderVoiceBlocks(pooled, dirtyBlocks); len(got) == 0 {
		t.Fatal("the first note rendered nothing, so the voice was never dirtied")
	}

	if err := synthesizer.ResetVoice(pooled, secondNote, velocity, defaultVoiceDuration, RenderOptions{AutoStop: true, DecayDBFS: defaultVoiceDecayDBFS}); err != nil {
		t.Fatalf("ResetVoice(%d) failed: %v", secondNote, err)
	}

	assertIdentical(t, "a pooled voice reused for another note", renderVoiceBlocks(pooled, poolingBlocks), want)
}

// TestPooledVoiceMatchesAFreshVoiceOnRetrigger covers the same-note case, where
// UpdateParams changes nothing at all and Bar.Reset is the only thing standing
// between the new strike and the old one's tail.
func TestPooledVoiceMatchesAFreshVoiceOnRetrigger(t *testing.T) {
	synthesizer := newTestSynthesizer(t)

	const (
		note     = 72
		velocity = 100
	)

	want := freshVoiceReference(t, synthesizer, note, velocity)

	pooled, err := synthesizer.NewVoice(note, velocity, defaultVoiceDuration, RenderOptions{AutoStop: true, DecayDBFS: defaultVoiceDecayDBFS})
	if err != nil {
		t.Fatalf("NewVoice(%d) failed: %v", note, err)
	}

	renderVoiceBlocks(pooled, dirtyBlocks)

	if err := synthesizer.ResetVoice(pooled, note, velocity, defaultVoiceDuration, RenderOptions{AutoStop: true, DecayDBFS: defaultVoiceDecayDBFS}); err != nil {
		t.Fatalf("ResetVoice(%d) failed: %v", note, err)
	}

	assertIdentical(t, "a retriggered pooled voice", renderVoiceBlocks(pooled, poolingBlocks), want)
}

// TestPooledVoiceSurvivesRepeatedReuse plays a slot through a long sequence of
// unrelated notes before the note under test, because a leak that is invisible
// after one reuse -- a buffer that grows, a shape that only drifts when the
// mode count changes -- would still be a leak after twenty.
func TestPooledVoiceSurvivesRepeatedReuse(t *testing.T) {
	synthesizer := newTestSynthesizer(t)

	const (
		target   = 67
		velocity = 100
	)

	want := freshVoiceReference(t, synthesizer, target, velocity)

	pooled, err := synthesizer.NewVoice(KeyboardFirstNote, velocity, defaultVoiceDuration, RenderOptions{AutoStop: true, DecayDBFS: defaultVoiceDecayDBFS})
	if err != nil {
		t.Fatalf("NewVoice failed: %v", err)
	}

	for note := KeyboardFirstNote; note <= KeyboardFirstNote+20; note++ {
		if err := synthesizer.ResetVoice(pooled, note, velocity, defaultVoiceDuration, RenderOptions{AutoStop: true, DecayDBFS: defaultVoiceDecayDBFS}); err != nil {
			t.Fatalf("ResetVoice(%d) failed: %v", note, err)
		}

		renderVoiceBlocks(pooled, 3)
	}

	if err := synthesizer.ResetVoice(pooled, target, velocity, defaultVoiceDuration, RenderOptions{AutoStop: true, DecayDBFS: defaultVoiceDecayDBFS}); err != nil {
		t.Fatalf("ResetVoice(%d) failed: %v", target, err)
	}

	assertIdentical(t, "a pooled voice reused twenty-one times", renderVoiceBlocks(pooled, poolingBlocks), want)
}

// TestStolenVoiceRendersLikeAFreshEngine raises the same claim to the engine,
// where the note goes through the pan law, the level trim and the mix. A single
// voice is enough to make the mix a scaled copy of one stream, so the two
// engines have to agree sample for sample even though one of them is playing
// the note on a slot that was stolen from another note.
func TestStolenVoiceRendersLikeAFreshEngine(t *testing.T) {
	const (
		firstNote  = 61
		stealNote  = 83
		velocity   = 100
		mixBlocks  = 32
		maxVoices1 = 1
	)

	reference := newTestEngine(t)
	reference.maxVoices = maxVoices1
	reference.NoteOn(stealNote, velocity)

	want := renderEngineBlocks(reference, mixBlocks)

	stealing := newTestEngine(t)
	stealing.maxVoices = maxVoices1
	stealing.NoteOn(firstNote, velocity)
	renderEngineBlocks(stealing, dirtyBlocks)
	stealing.NoteOn(stealNote, velocity)

	if stealing.ActiveVoices() != 1 {
		t.Fatalf("expected the note to have stolen the only slot, got %d voices", stealing.ActiveVoices())
	}

	assertIdentical(t, "a stolen engine voice", renderEngineBlocks(stealing, mixBlocks), want)
}

// renderEngineBlocks concatenates a number of interleaved stereo blocks.
func renderEngineBlocks(e *RealtimeEngine, blocks int) []float32 {
	out := make([]float32, 0, blocks*defaultRealtimeBlockFrames*2)

	for i := 0; i < blocks; i++ {
		out = append(out, e.ProcessBlock(defaultRealtimeBlockFrames)...)
	}

	return out
}
