package synth

import "math"

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
		voices:       make([]realtimeVoice, 0, 16),
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
	next := realtimeVoice{
		note:   note,
		stream: stream,
		left:   left,
		right:  right,
		buffer: make([]float32, defaultRealtimeBlockFrames),
	}

	for i := range e.voices {
		if e.voices[i].note == note {
			e.voices[i] = next
			return
		}
	}

	if len(e.voices) >= e.maxVoices {
		copy(e.voices[0:], e.voices[1:])
		e.voices = e.voices[:len(e.voices)-1]
	}

	e.voices = append(e.voices, next)
}

// ProcessBlock renders stereo interleaved output for the next block.
func (e *RealtimeEngine) ProcessBlock(frames int) []float32 {
	if frames <= 0 {
		return nil
	}

	required := frames * 2
	if len(e.mixBuffer) < required {
		e.mixBuffer = make([]float32, required)
	}

	buf := e.mixBuffer[:required]
	clear(buf)

	writeIndex := 0
	for _, v := range e.voices {
		if len(v.buffer) < frames {
			v.buffer = make([]float32, frames)
		}

		n := v.stream.RenderInto(v.buffer[:frames])
		for i := 0; i < n; i++ {
			sample := v.buffer[i]
			buf[i*2] += sample * v.left
			buf[i*2+1] += sample * v.right
		}

		if v.stream.Active() {
			e.voices[writeIndex] = v
			writeIndex++
		}
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
