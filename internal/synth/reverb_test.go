package synth

import (
	"math"
	"testing"
)

// renderNoteWithReverb strikes one note at a reverb setting and concatenates
// the interleaved output of the given number of callbacks.
func renderNoteWithReverb(t *testing.T, mix float32, frames, blocks, note, velocity int) []float32 {
	t.Helper()

	engine := newTestEngine(t)
	engine.SetReverbMix(mix)
	engine.NoteOn(note, velocity)

	out := make([]float32, 0, frames*blocks*2)
	for range blocks {
		out = append(out, engine.ProcessBlock(frames)...)
	}

	return out
}

// rms reports the root mean square of an interleaved buffer.
func rms(buf []float32) float64 {
	if len(buf) == 0 {
		return 0
	}

	var sum float64
	for _, v := range buf {
		sum += float64(v) * float64(v)
	}

	return math.Sqrt(sum / float64(len(buf)))
}

// TestAClosedReverbIsAnExactBypass is the compatibility half of the feature:
// the engine ships dry, and an engine nobody has turned the reverb up on must
// be sample-for-sample the engine that existed before there was one.
//
// Exact equality is the right assertion rather than a tolerance. The reverb
// adds to the buffer instead of replacing it, and a closed control skips the
// networks entirely, so there is no arithmetic left to be almost-right about.
// A tolerance here would pass for a bypass that still ran the delay lines and
// mixed in a very small amount of them, which is precisely the mistake worth
// catching -- it would cost CPU on every dry block forever and nobody would
// hear it.
func TestAClosedReverbIsAnExactBypass(t *testing.T) {
	const (
		frames   = 128
		blocks   = 64
		note     = 72
		velocity = 100
	)

	withReverb := renderNoteWithReverb(t, 0, frames, blocks, note, velocity)
	plain := renderNote(t, frames, blocks, note, velocity)

	if len(withReverb) != len(plain) {
		t.Fatalf("frame counts differ: %d and %d", len(withReverb), len(plain))
	}

	for i := range plain {
		if withReverb[i] != plain[i] {
			t.Fatalf("sample %d differs with the reverb closed: %v, want %v", i, withReverb[i], plain[i])
		}
	}
}

// TestTheReverbOutlivesEveryVoice is the property the reverb exists for, and
// the one that breaks anything downstream that treats ActiveVoices as "is
// something playing".
//
// A note is struck and rendered well past its own retirement. Once the engine
// holds no voices at all it must still be producing sound, and that sound must
// be decaying rather than sustaining -- a feedback delay network with its gains
// mis-set does not decay, it rings forever or it blows up, and both of those
// look like "the tail is still there" to a test that only asks whether the
// output is non-zero.
func TestTheReverbOutlivesEveryVoice(t *testing.T) {
	const (
		frames   = 128
		note     = 72
		velocity = 100

		// Comfortably past defaultVoiceDuration at 48 kHz, so every voice has
		// retired long before the run ends.
		blocks = 3000
	)

	engine := newTestEngine(t)
	engine.SetReverbMix(0.8)
	engine.NoteOn(note, velocity)

	var (
		firstSilentBlock = -1
		afterVoices      []float32
		lastBlock        []float32
	)

	for i := range blocks {
		block := engine.ProcessBlock(frames)

		if engine.ActiveVoices() == 0 && firstSilentBlock < 0 {
			firstSilentBlock = i

			afterVoices = append(afterVoices, block...)
		}

		lastBlock = append(lastBlock[:0], block...)
	}

	if firstSilentBlock < 0 {
		t.Fatalf("no voice retired in %d blocks: the run is too short to say anything", blocks)
	}

	tail := rms(afterVoices)
	if tail == 0 {
		t.Fatalf("the block after the last voice retired is silent: the tail does not outlive the note")
	}

	final := rms(lastBlock)
	if final >= tail {
		t.Errorf("the tail is not decaying: %v at retirement, %v at the end of the run", tail, final)
	}
}

// TestAWetRenderStaysFinite is the numerical guard. A feedback delay network is
// the one part of this engine with a loop in it, so it is the one part that can
// diverge, and it is fed a signal that has already been through an oscillator
// bank and a shaper.
func TestAWetRenderStaysFinite(t *testing.T) {
	const (
		frames   = 128
		blocks   = 750 // 2 s at 48 kHz.
		velocity = 127
	)

	engine := newTestEngine(t)
	engine.SetReverbMix(1)
	engine.SetMasterGain(1)

	// Every note at full velocity at once, which is the loudest thing that can
	// reach the reverb.
	for note := KeyboardFirstNote; note <= KeyboardLastNote; note++ {
		engine.NoteOn(note, velocity)
	}

	for range blocks {
		for i, v := range engine.ProcessBlock(frames) {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("sample %d is not finite: %v", i, v)
			}

			if v < -1 || v > 1 {
				t.Fatalf("sample %d is outside [-1,1]: %v", i, v)
			}
		}
	}
}

// TestTheReverbIsUnaffectedByCallbackWidth extends the guarantee
// realtime_blocksize_test.go makes for the dry path over the wet one: what a
// host hears must not depend on how much of it it asks for at a time.
//
// It is a real risk here rather than a formality. The wet gain moves per sample
// along a ramp, and the deinterleave scratch is sized per call, so a
// per-block-rather-than-per-sample slip in either would be inaudible at one
// width and wrong at another.
//
// The tolerance is not zero, and cannot be: a 512-frame callback deinterleaves
// into a longer scratch buffer than a 128-frame one and the delay lines read
// through a Hermite interpolator, so the two orders of the same additions
// differ in the last bits of a float32.
func TestTheReverbIsUnaffectedByCallbackWidth(t *testing.T) {
	const (
		wide     = 512
		narrow   = 128
		note     = 72
		velocity = 100
		blocks   = 200
		mix      = 0.6

		tolerance = 1e-6
	)

	wideOut := renderNoteWithReverb(t, mix, wide, blocks, note, velocity)
	narrowOut := renderNoteWithReverb(t, mix, narrow, blocks*wide/narrow, note, velocity)

	if len(wideOut) != len(narrowOut) {
		t.Fatalf("frame counts differ: wide %d, narrow %d", len(wideOut), len(narrowOut))
	}

	for i := range wideOut {
		if diff := math.Abs(float64(wideOut[i] - narrowOut[i])); diff > tolerance {
			t.Fatalf("sample %d of %d differs by %v: wide %v, narrow %v",
				i, len(wideOut), diff, wideOut[i], narrowOut[i])
		}
	}
}

// TestSilenceEndsTheTail covers the preset swap. Silence abandons every voice,
// and it has to abandon the room they were played in too -- an engine set aside
// mid-tail and brought back from the cache later would otherwise resume it.
func TestSilenceEndsTheTail(t *testing.T) {
	const (
		frames   = 128
		note     = 72
		velocity = 100
	)

	engine := newTestEngine(t)
	engine.SetReverbMix(0.8)
	engine.NoteOn(note, velocity)

	for range 64 {
		engine.ProcessBlock(frames)
	}

	engine.Silence()

	for i := range 8 {
		if r := rms(engine.ProcessBlock(frames)); r != 0 {
			t.Fatalf("block %d after Silence has RMS %v, want silence", i, r)
		}
	}
}

// TestClosingTheReverbGlidesRatherThanSteps pins the ramp.
//
// The control is a dial, and the loudest change it can make is the whole range
// at once. Applying that instantly would put a step into the output the size of
// the tail, which is a click. The assertion is on the difference between
// neighbouring samples: with the ramp, closing the control cannot move the
// output by more than the block it was already producing did.
func TestClosingTheReverbGlidesRatherThanSteps(t *testing.T) {
	const (
		frames   = 128
		note     = 72
		velocity = 100
	)

	engine := newTestEngine(t)
	engine.SetReverbMix(1)
	engine.NoteOn(note, velocity)

	// Let the tail establish itself, then let the note itself decay away so
	// what remains is almost entirely reverb and a step in it is not hidden
	// under a ringing bar.
	for range 400 {
		engine.ProcessBlock(frames)
	}

	before := engine.ProcessBlock(frames)
	settled := maxStep(before)
	last := before[len(before)-1]

	engine.SetReverbMix(0)

	after := engine.ProcessBlock(frames)

	// The seam between the two blocks counts too: a step applied at the block
	// boundary would hide from a scan that only looked inside one.
	if jump := math.Abs(float64(after[0] - last)); jump > 4*settled {
		t.Errorf("closing the reverb stepped the output by %v across the block boundary, against %v inside the previous block", jump, settled)
	}

	if step := maxStep(after); step > 4*settled {
		t.Errorf("closing the reverb stepped the output by %v, against %v in the previous block", step, settled)
	}
}

// maxStep reports the largest jump between neighbouring samples of one channel.
func maxStep(buf []float32) float64 {
	var largest float64

	for i := 2; i < len(buf); i++ {
		if diff := math.Abs(float64(buf[i] - buf[i-2])); diff > largest {
			largest = diff
		}
	}

	return largest
}

// TestReverbDecorrelatesTheChannels is why there are two networks rather than
// one fed the sum.
//
// A note in the middle of the keyboard is panned to the middle, so its two
// channels are identical. One shared network would leave them identical, and
// the reverb would arrive as a mono tail pinned to the centre of an image the
// keyboard pan exists to spread out.
func TestReverbDecorrelatesTheChannels(t *testing.T) {
	const (
		frames = 128
		blocks = 400

		// The centre of KeyboardFirstNote..KeyboardLastNote, so gainsForNote
		// gives the two channels the same coefficient.
		note     = (KeyboardFirstNote + KeyboardLastNote) / 2
		velocity = 100
	)

	engine := newTestEngine(t)
	engine.SetReverbMix(0.9)
	engine.NoteOn(note, velocity)

	var differing int

	for range blocks {
		block := engine.ProcessBlock(frames)

		for i := 0; i < len(block); i += 2 {
			if block[i] != block[i+1] {
				differing++
			}
		}
	}

	if differing == 0 {
		t.Error("the two channels are identical: the tail is mono")
	}
}

// TestReverbDoesNotAllocatePerBlock is the audio-thread guarantee. The engine
// renders on the worker that also feeds the transport, and a garbage collection
// there is a dropout.
func TestReverbDoesNotAllocatePerBlock(t *testing.T) {
	const (
		frames   = defaultRealtimeBlockFrames
		note     = 72
		velocity = 100
	)

	engine := newTestEngine(t)
	engine.SetReverbMix(0.5)
	engine.NoteOn(note, velocity)

	// The first block sizes the deinterleave scratch, which is the one
	// allocation the reverb is allowed and the reason this is not measured from
	// a cold engine.
	engine.ProcessBlock(frames)

	if allocs := testing.AllocsPerRun(64, func() {
		engine.ProcessBlock(frames)
	}); allocs != 0 {
		t.Errorf("ProcessBlock with reverb allocates %v times per block, want 0", allocs)
	}

	if allocs := testing.AllocsPerRun(64, func() {
		engine.SetReverbMix(0.25)
	}); allocs != 0 {
		t.Errorf("SetReverbMix allocates %v times, want 0", allocs)
	}
}

// TestReverbMixIsClamped covers the setter's edges. The value comes from a
// browser and crosses a js.Value, so it is not worth trusting.
func TestReverbMixIsClamped(t *testing.T) {
	engine := newTestEngine(t)

	for _, tc := range []struct {
		name string
		set  float32
		want float32
	}{
		{name: "below zero", set: -1, want: 0},
		{name: "zero", set: 0, want: 0},
		{name: "middle", set: 0.5, want: 0.5},
		{name: "one", set: 1, want: 1},
		{name: "above one", set: 3, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine.SetReverbMix(tc.set)

			if got := engine.ReverbMix(); math.Abs(float64(got-tc.want)) > 1e-6 {
				t.Errorf("SetReverbMix(%v) then ReverbMix() = %v, want %v", tc.set, got, tc.want)
			}
		})
	}
}
