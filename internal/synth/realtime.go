package synth

import (
	"math"

	"github.com/cwbudde/glockenspiel/internal/oscbank"
)

const (
	defaultRealtimeBlockFrames = 128
	defaultVoiceDuration       = 4.0
	defaultVoiceDecayDBFS      = -72.0
	defaultRealtimeMaxVoices   = 64
	minRealtimeGain            = 0.1
)

type realtimeVoice struct {
	note   int
	stream *Voice
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
}

// NewRealtimeEngine creates a block-rendering engine for interactive playback.
func NewRealtimeEngine(s *Synthesizer) *RealtimeEngine {
	return &RealtimeEngine{
		synth:        s,
		voices:       make([]realtimeVoice, 0, defaultRealtimeMaxVoices),
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
func (e *RealtimeEngine) NoteOn(note, velocity int) {
	stream, err := e.synth.NewVoice(note, velocity, e.noteDuration, e.renderOptions)
	if err != nil {
		return
	}

	left, right := gainsForNote(note, e.masterGain)

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
// which already carries a block buffer unless this is the first note to use it.
//
// The list is allocated at maxVoices capacity and only ever reslices within it,
// and ProcessBlock retires voices by swapping them past the end rather than
// overwriting them, so a retired voice leaves its buffer in the slot the next
// note-on picks up. The make below therefore runs at most maxVoices times over
// the engine's life, and never once the keyboard has been played through.
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
	defer oscbank.FlushDenormals().Restore()

	required := frames * 2
	if len(e.mixBuffer) < required {
		e.mixBuffer = make([]float32, required)
	}

	buf := e.mixBuffer[:required]
	clear(buf)

	writeIndex := 0

	// Indexing rather than ranging by value is load-bearing: over a copy, the
	// buffer growth below is written to the copy and discarded, so a block
	// larger than the buffer reallocates on every block instead of once.
	for i := range e.voices {
		v := &e.voices[i]

		if cap(v.buffer) < frames {
			v.buffer = make([]float32, frames)
		}

		n := v.stream.RenderInto(v.buffer[:frames])
		for j := 0; j < n; j++ {
			sample := v.buffer[j]
			buf[j*2] += sample * v.left
			buf[j*2+1] += sample * v.right
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

// ActiveVoices reports how many voices are currently alive.
func (e *RealtimeEngine) ActiveVoices() int {
	return len(e.voices)
}

func gainsForNote(note int, gain float32) (float32, float32) {
	const (
		firstNote = 72
		semitones = 24
	)

	relative := float32(note-firstNote) / float32(semitones-1)
	pan := relative*1.2 - 0.6

	left := gain * (1 - pan) * 0.5
	right := gain * (1 + pan) * 0.5

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
