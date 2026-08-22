package synth

import (
	"math"
	"sync/atomic"

	"github.com/cwbudde/glockenspiel/internal/oscbank"
)

const (
	defaultRealtimeBlockFrames = 128
	defaultVoiceDuration       = 4.0
	defaultVoiceDecayDBFS      = -72.0
	defaultRealtimeMaxVoices   = 64
	minRealtimeGain            = 0.1
)

// KeyboardFirstNote and KeyboardLastNote are the MIDI range the player can
// actually strike. They mirror KEYBOARD_FIRST_NOTE and KEYBOARD_LAST_NOTE in
// web/ui.js and are the Go-side source of truth for anything that has to reason
// about the span of the instrument.
//
// The narrower FIRST_NOTE/LAST_NOTE pair in web/ui.js (60..84) describes the
// drawn bar row, not the playable range: the on-screen keyboard below it sends
// note-ons across the full 36..96 span, and so do incoming MIDI and the
// computer-keyboard bindings. Panning has to cover every note that can reach
// NoteOn, so it spans the keyboard range rather than the bar row -- spanning
// 60..84 instead would push everything outside it past the intended stereo
// width, which is the class of bug this constant pair exists to prevent.
const (
	KeyboardFirstNote = 36
	KeyboardLastNote  = 96
)

// maxKeyboardPan is the half-width of the stereo spread, so the lowest note
// sits at -maxKeyboardPan and the highest at +maxKeyboardPan. Well short of a
// hard-panned 1.0: the bars should read as laid out in front of the listener,
// not as two separate mono instruments.
const maxKeyboardPan = 0.6

type realtimeVoice struct {
	note   int
	stream *Voice
	// left and right are unit-gain pan coefficients, master gain excluded.
	// Baking the master gain in here instead is what used to make SetMasterGain
	// silently miss every note that was already ringing; ProcessBlock applies
	// the engine's current gain on top of these per block.
	left   float32
	right  float32
	buffer []float32
}

// start points a voice slot at a new stream. It deliberately leaves buffer
// alone: every path that hands a slot a new stream -- retrigger, voice
// stealing, a freshly claimed slot -- runs on the audio thread, and the block
// buffer the slot already owns is exactly the buffer the new stream needs.
func (v *realtimeVoice) start(note int, stream *Voice, left, right float32) {
	v.note = note
	v.stream = stream
	v.left = left
	v.right = right
}

// RealtimeEngine streams and mixes active glockenspiel voices.
type RealtimeEngine struct {
	synth         *Synthesizer
	voices        []realtimeVoice
	mixBuffer     []float32
	masterGain    float32
	noteDuration  float64
	renderOptions RenderOptions
	maxVoices     int

	// droppedNoteOns and lastDroppedNote are the diagnostic for a note-on the
	// engine could not turn into a voice. See NoteOn for why the engine counts
	// rather than reports, and why these are atomics.
	droppedNoteOns  atomic.Uint64
	lastDroppedNote atomic.Int32
}

// newVoiceSlots returns an empty voice list of maxVoices capacity whose slots
// all carry a block buffer already.
//
// The buffers come out of one backing array, so the whole voice bank is a
// single allocation and neighbouring voices sit next to each other in cache.
// The full slice expression keeps a slot from ever reaching into the next one:
// ProcessBlock is allowed to replace a buffer with a wider one, and appending
// into a neighbour instead would be silent cross-talk.
func newVoiceSlots(maxVoices, frames int) []realtimeVoice {
	slots := make([]realtimeVoice, maxVoices)
	pool := make([]float32, maxVoices*frames)

	for i := range slots {
		start := i * frames
		slots[i].buffer = pool[start : start+frames : start+frames]
	}

	return slots[:0]
}

// NewRealtimeEngine creates a block-rendering engine for interactive playback.
func NewRealtimeEngine(s *Synthesizer) *RealtimeEngine {
	return &RealtimeEngine{
		synth:        s,
		voices:       newVoiceSlots(defaultRealtimeMaxVoices, defaultRealtimeBlockFrames),
		mixBuffer:    make([]float32, defaultRealtimeBlockFrames*2),
		masterGain:   0.7,
		noteDuration: defaultVoiceDuration,
		renderOptions: RenderOptions{
			AutoStop:  true,
			DecayDBFS: defaultVoiceDecayDBFS,
		},
		maxVoices: defaultRealtimeMaxVoices,
	}
}

// SetMasterGain updates engine output gain.
func (e *RealtimeEngine) SetMasterGain(gain float32) {
	if gain < minRealtimeGain {
		gain = minRealtimeGain
	}

	if gain > 1 {
		gain = 1
	}

	e.masterGain = gain
}

// NoteOn retriggers the requested bar.
//
// A note whose voice cannot be built is counted rather than reported. NoteOn
// runs on the audio thread -- the browser's audio callback, the plugin's
// process call -- where the usual ways of not losing an error are all
// unavailable: returning one would push the handling into a caller that has
// nowhere to put it either, logging takes a lock and formats a string, and
// wrapping it into a channel or a slice allocates. Two atomic stores cost a few
// nanoseconds, never block, never allocate, and leave the failure visible to
// anything that can read a counter: a test, a debug overlay, a health check.
//
// The alternative that was here before -- dropping the error on the floor --
// is what made the dead low register invisible. Notes 36..52 produced no sound
// and no trace of having been asked for, so the bug read as "the low keys are
// quiet" rather than as "the engine is refusing them".
func (e *RealtimeEngine) NoteOn(note, velocity int) {
	stream, err := e.synth.NewVoice(note, velocity, e.noteDuration, e.renderOptions)
	if err != nil {
		e.lastDroppedNote.Store(int32(note))
		e.droppedNoteOns.Add(1)

		return
	}

	left, right := gainsForNote(note)

	for i := range e.voices {
		if e.voices[i].note == note {
			e.voices[i].start(note, stream, left, right)
			return
		}
	}

	if len(e.voices) >= e.maxVoices {
		// Steal the oldest voice. Rotating the slot to the back rather than
		// dropping it and appending a new one keeps its buffer, which is
		// already the right size.
		last := len(e.voices) - 1
		stolen := e.voices[0]

		copy(e.voices, e.voices[1:])

		e.voices[last] = stolen
		e.voices[last].start(note, stream, left, right)

		return
	}

	slot := e.claimSlot()
	e.voices[slot].start(note, stream, left, right)
}

// claimSlot extends the voice list by one and returns the index of the slot,
// which already carries a block buffer.
//
// The list is built at maxVoices capacity with every slot's buffer allocated up
// front, and it only ever reslices within that capacity: ProcessBlock retires
// voices by swapping them past the end rather than overwriting them, so a
// retired voice leaves its buffer in the slot the next note-on picks up. That
// is what keeps this path free of any allocation, including for the first note
// a slot ever sees.
//
// The append is unreachable unless a caller raises maxVoices past the capacity
// the engine was built with. It is left in so that doing so degrades to an
// allocation rather than to a panic.
func (e *RealtimeEngine) claimSlot() int {
	slot := len(e.voices)

	if slot < cap(e.voices) {
		e.voices = e.voices[:slot+1]
	} else {
		e.voices = append(e.voices, realtimeVoice{})
	}

	if cap(e.voices[slot].buffer) < defaultRealtimeBlockFrames {
		e.voices[slot].buffer = make([]float32, defaultRealtimeBlockFrames)
	}

	return slot
}

// ProcessBlock renders stereo interleaved output for the next block.
func (e *RealtimeEngine) ProcessBlock(frames int) []float32 {
	if frames <= 0 {
		return nil
	}

	// One mode change per callback covers the whole block, mixing included.
	// The per-block scopes inside the bank see the bits already set and cost a
	// register read each.
	scope := oscbank.FlushDenormals()
	defer scope.Restore()

	required := frames * 2
	if len(e.mixBuffer) < required {
		e.mixBuffer = make([]float32, required)
	}

	buf := e.mixBuffer[:required]
	clear(buf)

	writeIndex := 0

	// Read the master gain once per block. Folding it into the per-voice pan
	// coefficients here rather than at note-on is what makes a gain change
	// audible on notes that are already sounding, and hoisting it out of the
	// inner loop keeps the mix at the same two multiplies per sample it cost
	// when the gain was baked into the slot.
	gain := e.masterGain

	// Indexing rather than ranging by value is load-bearing: over a copy, the
	// buffer growth below is written to the copy and discarded, so a block
	// larger than the buffer reallocates on every block instead of once.
	for i := range e.voices {
		v := &e.voices[i]

		if cap(v.buffer) < frames {
			v.buffer = make([]float32, frames)
		}

		left := v.left * gain
		right := v.right * gain

		n := v.stream.RenderInto(v.buffer[:frames])
		for j := 0; j < n; j++ {
			sample := v.buffer[j]
			buf[j*2] += sample * left
			buf[j*2+1] += sample * right
		}

		if !v.stream.Active() {
			continue
		}

		// Swap rather than assign: an overwrite would drop the retired voice's
		// buffer and leave two slots pointing at one buffer. Swapping permutes
		// the slots, so every buffer stays owned by exactly one slot and the
		// retired ones wait past the end for claimSlot to hand them out again.
		if writeIndex != i {
			e.voices[writeIndex], e.voices[i] = e.voices[i], e.voices[writeIndex]
		}

		writeIndex++
	}

	e.voices = e.voices[:writeIndex]

	for i := range buf {
		buf[i] = hardClip(buf[i])
	}

	return buf
}

// DroppedNoteOns reports how many note-ons the engine could not turn into a
// voice over its lifetime. It is safe to call from any thread, and a non-zero
// value always means a key was struck and produced nothing.
//
// The counter is monotonic and deliberately never reset: a caller that wants a
// rate takes two readings and subtracts, which is a thing a reader can do on
// its own thread, whereas a reset is a write the audio thread would have to
// coordinate with.
func (e *RealtimeEngine) DroppedNoteOns() uint64 {
	return e.droppedNoteOns.Load()
}

// LastDroppedNote reports the MIDI note of the most recent dropped note-on, or
// -1 if none has been dropped.
//
// It is the smallest thing that makes the counter actionable: a bare count says
// something is wrong, the note says where to look. It is not synchronised with
// the counter -- reading both is two loads, not one snapshot -- because making
// it atomic as a pair would need a lock on the audio thread to serve a purely
// diagnostic read.
func (e *RealtimeEngine) LastDroppedNote() int {
	if e.droppedNoteOns.Load() == 0 {
		return -1
	}

	return int(e.lastDroppedNote.Load())
}

// ActiveVoices reports how many voices are currently alive.
func (e *RealtimeEngine) ActiveVoices() int {
	return len(e.voices)
}

// gainsForNote maps a note to its unit-gain stereo pair: the bar's position on
// the keyboard becomes its position in the stereo field, low notes to the left.
// The returned pair carries no master gain -- ProcessBlock applies that per
// block so a gain change reaches notes that are already ringing.
//
// The position is clamped before it becomes a pan, which is what keeps the
// result a pan rather than a gain change. The previous mapping spanned 24
// semitones from note 72 and clamped nothing, so a note-on below 72 -- ordinary
// on a keyboard that starts at 36 -- ran off the end of the scale: note 36 came
// out at pan -2.48, which is a left channel boosted to 1.74x and a right
// channel multiplied by -0.74, i.e. over unity and phase-inverted against the
// left. Clamping keeps both gains inside [0, 1] for every MIDI value, including
// the 0..35 and 97..127 a stray note-on can carry.
func gainsForNote(note int) (float32, float32) {
	const span = KeyboardLastNote - KeyboardFirstNote

	relative := float32(note-KeyboardFirstNote) / float32(span)

	switch {
	case relative < 0:
		relative = 0
	case relative > 1:
		relative = 1
	}

	pan := (relative*2 - 1) * maxKeyboardPan

	left := (1 - pan) * 0.5
	right := (1 + pan) * 0.5

	return left, right
}

func hardClip(v float32) float32 {
	if v > 1 {
		return 1
	}

	if v < -1 {
		return -1
	}

	if math.Abs(float64(v)) < 1e-30 {
		return 0
	}

	return v
}
