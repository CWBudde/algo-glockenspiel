package synth

import (
	"math"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/oscbank"
	"github.com/cwbudde/glockenspiel/internal/preset"
)

// The engine renders every sounding voice through one voice-major oscillator
// bank per oscbank.LaneWidth lanes instead of walking the voices serially
// through their own rotor-major banks. The two tests below are the correctness
// half of that change, and they pin two different claims.
//
// The first is exact: the voice-major kernel has no cross-lane arithmetic at
// all -- lane l's rotors are advanced from lane l's excitation and summed into
// lane l's accumulator -- so a note must render the same samples whether it is
// alone in a bank or sharing one with seven others, and whichever lane it lands
// in. There is no tolerance to pick here and none is used: any bleed between
// lanes, and any lane mix-up when voice stealing rotates the slots, changes the
// samples and equality catches it.
//
// The second is bounded, and deliberately so. The rotor-major Bank folds a
// voice's rotors across the eight lanes of a block and finishes the reduction
// with a pairwise tree; the voice-major bank sums the same rotors in ascending
// pairs down the lane. Same terms, different association, so float32 rounds
// differently. Nothing else in the chain moves -- the lowpass, the shaper and
// the dry mix are per voice either way -- so the difference is exactly the
// reassociation of a sixteen-term sum and nothing more, which is why the bound
// is asserted relative to the block's own peak rather than as a raw epsilon.

const voiceMajorBlocks = 48

// captureVoiceBuffer returns the mono signal the engine rendered for a note in
// the block that has just finished, which is the voice's own output before the
// pan and the master gain reach it.
func captureVoiceBuffer(e *RealtimeEngine, note, frames int) []float32 {
	for i := range e.voices {
		if e.voices[i].note == note {
			return append([]float32(nil), e.voices[i].buffer[:frames]...)
		}
	}

	return nil
}

// TestPolyphonicRenderIsBitIdenticalPerVoice strikes a chord across several
// lanes and requires every voice in it to render exactly what the same note
// renders alone in lane 0 of its own engine.
//
// The onsets are staggered, which makes it the test for the other half of the
// lane story as well: a note-on has to configure and clear its own lane and
// leave the lanes around it ringing. A SetVoice that reshaped the bank, or a
// ResetVoice that cleared the wrong lane, would silence or corrupt the notes
// that were already sounding, and the earlier voices' samples would stop
// matching their solo renders at exactly the block the next note arrives in.
func TestPolyphonicRenderIsBitIdenticalPerVoice(t *testing.T) {
	const (
		frames   = defaultRealtimeBlockFrames
		velocity = 100
	)

	// Ten notes, so the chord spills past LaneWidth into a second bank and the
	// lane-to-bank mapping is exercised rather than assumed.
	notes := []int{60, 64, 67, 72, 76, 79, 84, 55, 48, 91}
	onsets := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}

	chord := newTestEngine(t)

	solo := make([]*RealtimeEngine, len(notes))
	for i := range notes {
		solo[i] = newTestEngine(t)
	}

	for block := 0; block < voiceMajorBlocks; block++ {
		for i, note := range notes {
			if onsets[i] == block {
				chord.NoteOn(note, velocity)
				solo[i].NoteOn(note, velocity)
			}
		}

		chord.ProcessBlock(frames)

		for i, note := range notes {
			if block < onsets[i] {
				continue
			}

			solo[i].ProcessBlock(frames)

			want := captureVoiceBuffer(solo[i], note, frames)
			got := captureVoiceBuffer(chord, note, frames)

			if want == nil || got == nil {
				// Both engines run the same auto-stop rule on the same samples,
				// so a note retires in the same block in both or the test has
				// already failed on an earlier block.
				if want != nil || got != nil {
					t.Fatalf("note %d retired in only one engine at block %d", note, block)
				}

				continue
			}

			for j := range got {
				if got[j] != want[j] {
					t.Fatalf("note %d differs from its solo render at block %d sample %d: %v vs %v",
						note, block, j, got[j], want[j])
				}
			}
		}
	}

	if chord.ActiveVoices() == 0 {
		t.Fatal("every voice retired, so the comparison covered nothing")
	}
}

// TestPolyphonicRenderMatchesTheSerialPath renders a chord through the engine
// and compares it against the path the engine used to take: every voice through
// its own rotor-major model.Bar, mixed with the same pan, trim, master gain and
// hard clip.
//
// It is a bound and not an equality, because the two layouts sum a voice's
// rotors in different orders: Bank folds them across the eight lanes of a block
// and finishes with a pairwise tree, the voice-major bank sums ascending rotor
// pairs down a single lane. Reassociating a float32 sum changes it.
//
// The shipped preset does not actually notice. It has four modes and no
// harmonics, so a voice is four rotors, and four rotors happen to associate
// identically either way -- the block fold has nothing to fold, and both paths
// end at (r0+r1)+(r2+r3). The subtest that gives every mode four harmonics is
// there so the bound is measuring something: sixteen rotors do reassociate, and
// that is the case a preset a fit produces tomorrow could land in.
//
// The reference is written out here rather than kept behind a flag in the
// engine, because it is not a second render mode anybody should be able to
// select -- it is the previous implementation, and this test is the only thing
// that still needs it.
func TestPolyphonicRenderMatchesTheSerialPath(t *testing.T) {
	const (
		frames   = defaultRealtimeBlockFrames
		velocity = 100

		// One part in 100000 of the block's own peak: far above the measured
		// deviation of a sixteen-term reassociated float32 sum, four orders of
		// magnitude below anything audible, and tight enough that an actual
		// lane mix-up could not hide under it.
		relativeBound = 1e-5
	)

	notes := []int{60, 64, 67, 72}

	for _, tc := range []struct {
		name      string
		harmonics int
	}{
		{name: "shipped preset", harmonics: 0},
		{name: "four harmonics per mode", harmonics: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := loadTestPreset(t)

			if tc.harmonics > 0 {
				// Harmonics are a v2 field, and the shipped preset is written
				// in v2 already; the bump is here so the test does not depend
				// on that staying true.
				p.Version = preset.VersionV2

				gains := make([]float64, tc.harmonics)
				for i := range gains {
					gains[i] = 1 / float64(i+1)
				}

				for i := range p.Parameters.Modes {
					p.Parameters.Modes[i].Harmonics = append([]float64(nil), gains...)
				}
			}

			s, err := NewSynthesizer(p, 48000)
			if err != nil {
				t.Fatalf("NewSynthesizer failed: %v", err)
			}

			engine := NewRealtimeEngine(s)
			reference := newSerialReference(t, engine, notes, velocity)

			for _, note := range notes {
				engine.NoteOn(note, velocity)
			}

			peak := 0.0
			worst := 0.0

			for block := 0; block < voiceMajorBlocks; block++ {
				got := engine.ProcessBlock(frames)
				want := reference.processBlock(frames)

				for i := range got {
					if diff := math.Abs(float64(got[i] - want[i])); diff > worst {
						worst = diff
					}

					if level := math.Abs(float64(want[i])); level > peak {
						peak = level
					}
				}
			}

			if peak == 0 {
				t.Fatal("the reference rendered silence, so the comparison covered nothing")
			}

			if worst > relativeBound*peak {
				t.Fatalf("voice-major render differs from the serial path by %g, which is %g of the %g peak, want at most %g",
					worst, worst/peak, peak, relativeBound)
			}

			t.Logf("worst deviation %g at peak %g (%g relative)", worst, peak, worst/peak)
		})
	}
}

// serialReference is the engine's mix loop as it stood before the voice-major
// banks: one voice per note, each rendering through its own model.Bar.
type serialReference struct {
	voices  []*Voice
	left    []float32
	right   []float32
	buf     []float32
	scratch []float32
}

func newSerialReference(t *testing.T, engine *RealtimeEngine, notes []int, velocity int) *serialReference {
	t.Helper()

	ref := &serialReference{
		buf:     make([]float32, defaultRealtimeBlockFrames*2),
		scratch: make([]float32, defaultRealtimeBlockFrames),
	}

	for _, note := range notes {
		voice, err := engine.synth.NewVoice(note, velocity, engine.noteDuration, engine.renderOptions)
		if err != nil {
			t.Fatalf("NewVoice(%d) failed: %v", note, err)
		}

		trim := engine.trimForNote(note)
		left, right := gainsForNote(note)

		ref.voices = append(ref.voices, voice)
		ref.left = append(ref.left, left*trim*engine.masterGain)
		ref.right = append(ref.right, right*trim*engine.masterGain)
	}

	return ref
}

func (r *serialReference) processBlock(frames int) []float32 {
	buf := r.buf[:frames*2]
	clear(buf)

	for i, voice := range r.voices {
		n := voice.RenderInto(r.scratch[:frames])

		for j := 0; j < n; j++ {
			sample := r.scratch[j]
			buf[j*2] += sample * r.left[i]
			buf[j*2+1] += sample * r.right[i]
		}
	}

	for i := range buf {
		buf[i] = hardClip(buf[i])
	}

	return buf
}

// TestLanesStayDistinctAcrossStealingAndRetirement is the lane counterpart of
// the buffer-identity tests: two sounding voices sharing a lane would mix one
// note's rotor state into another's, and the slots are permuted by both voice
// stealing and retirement, so the invariant has to survive both.
func TestLanesStayDistinctAcrossStealingAndRetirement(t *testing.T) {
	engine := newTestEngine(t)
	engine.maxVoices = 5

	assertDistinctLanes := func(what string) {
		t.Helper()

		seen := map[int]int{}

		for i := range engine.voices {
			lane := engine.voices[i].lane
			if lane == noLane {
				t.Fatalf("%s: sounding voice %d holds no lane", what, i)
			}

			if other, ok := seen[lane]; ok {
				t.Fatalf("%s: voices %d and %d share lane %d", what, other, i, lane)
			}

			seen[lane] = i
		}
	}

	for note := 60; note < 65; note++ {
		engine.NoteOn(note, 100)
	}

	assertDistinctLanes("after filling the engine")

	for note := 70; note < 75; note++ {
		engine.NoteOn(note, 100)
		assertDistinctLanes("after a steal")
	}

	// A short note retires inside ProcessBlock and has to give its lane back,
	// or the engine leaks lanes until acquireLane has to grow them.
	engine.noteDuration = 0.005
	engine.NoteOn(80, 100)
	engine.noteDuration = defaultVoiceDuration

	for block := 0; block < 40; block++ {
		engine.ProcessBlock(defaultRealtimeBlockFrames)
		assertDistinctLanes("after a block")
	}

	used := 0

	for _, taken := range engine.laneUsed {
		if taken {
			used++
		}
	}

	if used != engine.ActiveVoices() {
		t.Fatalf("%d lanes held by %d sounding voices", used, engine.ActiveVoices())
	}
}

// TestSoundingVoicesStayPackedIntoTheLowestLanes is the performance invariant,
// and it is a correctness test only in the sense that breaking it is silent: a
// block walks every bank up to the highest lane a voice holds, so lanes that
// drifted upwards as notes were stolen and retired would multiply the rotor
// work without changing a single sample.
func TestSoundingVoicesStayPackedIntoTheLowestLanes(t *testing.T) {
	engine := newTestEngine(t)
	engine.noteDuration = 0.01

	for note := 60; note < 68; note++ {
		engine.NoteOn(note, 100)
	}

	for block := 0; engine.ActiveVoices() > 0 && block < 200; block++ {
		engine.ProcessBlock(defaultRealtimeBlockFrames)
	}

	if engine.ActiveVoices() != 0 {
		t.Fatalf("expected every voice to retire, %d still sounding", engine.ActiveVoices())
	}

	engine.noteDuration = defaultVoiceDuration

	for note := 72; note < 75; note++ {
		engine.NoteOn(note, 100)
	}

	for i := range engine.voices {
		if lane := engine.voices[i].lane; lane >= oscbank.LaneWidth {
			t.Fatalf("three sounding voices reached lane %d, so the render walks %d banks",
				lane, lane/oscbank.LaneWidth+1)
		}
	}
}

// TestARetiredLaneIsRefilledAndItsBankSkipped pins the two properties that make
// not compacting lanes tolerable, and they are the reasons the engine gets away
// with releasing a lane where its note happened to retire.
//
// The first is that a hole heals. acquireLane takes the lowest free lane, so a
// lane freed in a low bank is the one the next note-on gets, and fragmentation
// does not accumulate while anything is being played.
//
// The second is that a bank nobody is sounding in costs nothing: renderBanks
// skips it rather than advancing eight silent lanes. Without that, a session
// that once reached full polyphony would pay for every bank it had ever touched
// for as long as it ran.
func TestARetiredLaneIsRefilledAndItsBankSkipped(t *testing.T) {
	engine := newTestEngine(t)

	// Eight short notes take bank 0, eight long ones take bank 1.
	engine.noteDuration = 0.01

	for note := 60; note < 68; note++ {
		engine.NoteOn(note, 100)
	}

	engine.noteDuration = defaultVoiceDuration

	for note := 68; note < 76; note++ {
		engine.NoteOn(note, 100)
	}

	for i := range engine.voices {
		if lane := engine.voices[i].lane; lane < 0 || lane >= 2*oscbank.LaneWidth {
			t.Fatalf("sixteen notes should occupy exactly two banks, got lane %d", lane)
		}
	}

	// Run until the short notes retire. The long ones outlive this by three
	// orders of magnitude, so bank 1 is still sounding when bank 0 empties.
	for block := 0; engine.ActiveVoices() > oscbank.LaneWidth && block < 400; block++ {
		engine.ProcessBlock(defaultRealtimeBlockFrames)
	}

	if got := engine.ActiveVoices(); got != oscbank.LaneWidth {
		t.Fatalf("expected the eight short notes to retire, %d voices sounding", got)
	}

	for lane := range oscbank.LaneWidth {
		if engine.laneUsed[lane] {
			t.Fatalf("lane %d is still held after its note retired", lane)
		}
	}

	// Bank 0 is empty and bank 1 is not, which is the fragmented state. It has
	// to render, and it has to render the same as it did the block before: a
	// skipped bank must be skipped, not zeroed into the mix.
	before := append([]float32(nil), engine.ProcessBlock(defaultRealtimeBlockFrames)...)
	if !hasSignal(before) {
		t.Fatal("the surviving bank produced silence")
	}

	// The hole heals: the next note-on takes the lowest free lane, which is in
	// the empty bank rather than beyond the sounding one.
	engine.NoteOn(80, 100)

	lane := noLane

	for i := range engine.voices {
		if engine.voices[i].note == 80 {
			lane = engine.voices[i].lane
		}
	}

	if lane < 0 || lane >= oscbank.LaneWidth {
		t.Fatalf("a note-on after a retirement took lane %d, want a lane in the emptied bank 0", lane)
	}
}

func hasSignal(samples []float32) bool {
	for _, s := range samples {
		if s != 0 {
			return true
		}
	}

	return false
}
