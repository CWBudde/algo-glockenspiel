package synth

import (
	"math"
	"sync/atomic"

	"github.com/cwbudde/glockenspiel/internal/oscbank"
	"github.com/cwbudde/glockenspiel/model"
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
// The values themselves live in model, because preset validation needs them:
// whether a preset is well-formed depends on whether it survives transposition
// to both ends of this range. These are the engine's names for them.
//
// The narrower FIRST_NOTE/LAST_NOTE pair in web/ui.js (60..84) describes the
// drawn bar row, not the playable range: the on-screen keyboard below it sends
// note-ons across the full 36..96 span, and so do incoming MIDI and the
// computer-keyboard bindings. Panning has to cover every note that can reach
// NoteOn, so it spans the keyboard range rather than the bar row -- spanning
// 60..84 instead would push everything outside it past the intended stereo
// width, which is the class of bug this constant pair exists to prevent.
const (
	KeyboardFirstNote = model.KeyboardFirstNote
	KeyboardLastNote  = model.KeyboardLastNote
)

// maxKeyboardPan is the half-width of the stereo spread, so the lowest note
// sits at -maxKeyboardPan and the highest at +maxKeyboardPan. Well short of a
// hard-panned 1.0: the bars should read as laid out in front of the listener,
// not as two separate mono instruments.
const maxKeyboardPan = 0.6

type realtimeVoice struct {
	note int
	// stream is the slot's voice, built once at engine construction and reused
	// for every note the slot ever plays. It is never replaced on the audio
	// thread: NoteOn restrikes it through Synthesizer.ResetVoice instead, which
	// is what keeps a note-on free of allocation.
	stream *Voice
	// left and right are unit-gain pan coefficients, master gain excluded.
	// Baking the master gain in here instead is what used to make SetMasterGain
	// silently miss every note that was already ringing; ProcessBlock applies
	// the engine's current gain on top of these per block.
	left   float32
	right  float32
	buffer []float32
}

// start records what a slot is now playing, after its stream has already been
// restruck. It deliberately touches neither buffer nor stream: every path that
// starts a note -- retrigger, voice stealing, a freshly claimed slot -- runs on
// the audio thread, and the block buffer and the voice the slot already owns
// are exactly the ones the new note needs.
func (v *realtimeVoice) start(note int, left, right float32) {
	v.note = note
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

	// noteTrims is the per-note level trim, indexed by note-trimsFirst, and
	// read once per note-on. Built by calibrateNoteTrims at construction, never
	// written afterwards, so the audio thread only ever loads from it.
	noteTrims  []float32
	trimsFirst int

	// droppedNoteOns and lastDroppedNote are the diagnostic for a note-on the
	// engine could not turn into a voice. See NoteOn for why the engine counts
	// rather than reports, and why these are atomics.
	droppedNoteOns  atomic.Uint64
	lastDroppedNote atomic.Int32
}

// newVoiceSlots returns an empty voice list of maxVoices capacity whose slots
// all carry a block buffer and a ready-to-strike voice already.
//
// The buffers come out of one backing array, so the whole voice bank is a
// single allocation and neighbouring voices sit next to each other in cache.
// The full slice expression keeps a slot from ever reaching into the next one:
// ProcessBlock is allowed to replace a buffer with a wider one, and appending
// into a neighbour instead would be silent cross-talk.
//
// The voices are built here, off the audio thread, and warmed so their bars
// have their working buffers too. That is the second half of the note-on
// allocation story: the slot buffers stopped a note-on from allocating a block
// buffer, and pooling the voices stops it from building a bar. Everything a
// note-on can possibly need exists before the first one arrives.
//
// A voice that cannot be built leaves its slot's stream nil, and NoteOn refuses
// such a slot rather than crashing on it. It cannot happen for a preset that
// built a Synthesizer -- the parameters are the same ones -- but the engine
// constructor has nowhere to return an error to, and a silent nil dereference
// on the audio thread is a worse answer than a counted refusal.
func newVoiceSlots(s *Synthesizer, maxVoices, frames int) []realtimeVoice {
	slots := make([]realtimeVoice, maxVoices)
	pool := make([]float32, maxVoices*frames)

	for i := range slots {
		start := i * frames
		slots[i].buffer = pool[start : start+frames : start+frames]

		voice, err := s.newIdleVoice()
		if err != nil {
			continue
		}

		voice.warm()

		slots[i].stream = voice
	}

	return slots[:0]
}

// NewRealtimeEngine creates a block-rendering engine for interactive playback.
//
// Construction measures the preset once per playable note to build the level
// trim table, which costs 44 ms (best of seven) for the shipped preset at 48 kHz. That is
// paid once, off the audio thread, by the one caller that builds an engine
// (cmd/glockenspiel-wasm at startup). It is deliberately not paid by
// NewSynthesizer: internal/optimizer/objective.go builds a synthesizer per
// candidate evaluation, and 61 extra renders per evaluation would make fitting
// unusable.
//
// Construction also builds and warms one voice per slot, which is what a
// note-on no longer has to do. Measured against the same preset it is 1472
// further allocations and 654 KB, and it does not move construction time out of
// the noise of the calibration it sits next to (38.7..41.3 ms before,
// 39.1..42.8 ms after, three runs each). Both are paid once at startup.
func NewRealtimeEngine(s *Synthesizer) *RealtimeEngine {
	return &RealtimeEngine{
		synth:        s,
		voices:       newVoiceSlots(s, defaultRealtimeMaxVoices, defaultRealtimeBlockFrames),
		mixBuffer:    make([]float32, defaultRealtimeBlockFrames*2),
		masterGain:   0.7,
		noteDuration: defaultVoiceDuration,
		renderOptions: RenderOptions{
			AutoStop:  true,
			DecayDBFS: defaultVoiceDecayDBFS,
		},
		maxVoices:  defaultRealtimeMaxVoices,
		noteTrims:  calibrateNoteTrims(s),
		trimsFirst: KeyboardFirstNote,
	}
}

// calibrateNoteTrims measures the preset at every playable note and returns the
// gain each note needs to sit at the level of the preset's own note.
//
// == Why the law is measured rather than written down ==
//
// The obvious approach is a formula: the peak level of the shipped preset falls
// almost linearly with pitch, 27.78 dB across the keyboard at about -0.46 dB
// per semitone, and a straight line through that fits to within 0.93 dB. It is
// wrong anyway, and the second preset in the repo is what proves it.
//
// The slope is not a property of transposition. It is a property of *this*
// preset having four modes: the peak of a multi-mode bar is where the modes
// beat into phase, and how much of that beat pattern fits inside the decay
// envelope grows as the decay does, i.e. as 1/ratio. testdata/presets/minimal
// has a single mode and therefore nothing to beat against, and its level is
// flat to within 0.3 dB from note 36 to note 78 -- measured, not assumed.
// Applying the shipped preset's -0.46 dB/semitone line to it would *introduce*
// some 28 dB of tilt where there is currently none. Even the physically
// motivated law, multiplying by the transposition ratio (-0.5017 dB/semitone,
// the exactly-undo-the-decay-inflation law), leaves 2.32 dB of residual on the
// shipped preset and turns minimal's 11.30 dB spread into 20.39 dB.
//
// No fixed curve can serve both, because the level-versus-pitch relationship is
// a consequence of the preset's mode structure. Measuring it costs 61 renders
// at engine construction and is exact for whatever preset is actually loaded,
// including one a fit produces tomorrow.
//
// == The reference and the clamp ==
//
// Every note is normalised to the preset's own note, so a preset keeps the
// level it was authored and fitted at and only the *other* notes move. That
// also means the shipped preset's -3 dBFS stays -3 dBFS.
//
// Velocity is not a variable here: measured across both presets and every note,
// the peak scales with velocity to within 0.01 dB of a constant factor, so a
// trim measured at one velocity levels the keyboard at all of them. The
// calibration uses the maximum, 127, so the headroom the trims buy is headroom
// at the loudest strike a MIDI keyboard can send.
//
// The clamp is a guard against pathology, not a design parameter: a note that
// renders almost nothing -- a preset whose modes cancel at some pitch, a fit
// that went somewhere strange -- would otherwise ask for an unbounded boost and
// amplify numerical noise to full scale. +/-36 dB is far outside anything the
// two real presets need (they span -16.1 dB to +11.7 dB).
func calibrateNoteTrims(synthesizer *Synthesizer) []float32 {
	const (
		calibrationVelocity = 127
		minTrim             = 1.0 / 64
		maxTrim             = 64.0
	)

	trims := make([]float32, KeyboardLastNote-KeyboardFirstNote+1)
	for i := range trims {
		trims[i] = 1
	}

	reference := synthesizer.peakForNote(synthesizer.preset.Note, calibrationVelocity)
	if reference <= 0 {
		// The preset is silent at its own note, so there is nothing to
		// normalise against. Leaving every trim at unity keeps the engine
		// behaving exactly as it did before calibration existed.
		return trims
	}

	for note := KeyboardFirstNote; note <= KeyboardLastNote; note++ {
		peak := synthesizer.peakForNote(note, calibrationVelocity)
		if peak <= 0 {
			continue
		}

		trims[note-KeyboardFirstNote] = float32(math.Min(math.Max(reference/peak, minTrim), maxTrim))
	}

	return trims
}

// trimForNote returns the level trim for a note, or 1 for a note outside the
// keyboard. An out-of-range note-on is already an oddity -- gainsForNote clamps
// its pan rather than extrapolating -- and extrapolating a level for it would
// be inventing a measurement instead of declining to make one.
func (e *RealtimeEngine) trimForNote(note int) float32 {
	index := note - e.trimsFirst
	if index < 0 || index >= len(e.noteTrims) {
		return 1
	}

	return e.noteTrims[index]
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
// A note the engine cannot strike -- one whose transposed parameters do not
// validate -- is counted rather than reported. NoteOn
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
	// The pan law and the level law are applied here rather than in
	// ProcessBlock, and stay two separate functions rather than one combined
	// one. gainsForNote redistributes a note's energy between the channels and
	// its two gains sum to 1 by construction, which is the invariant that keeps
	// panning from becoming a volume change; the trim is the note's volume.
	// Folding them together would destroy that invariant and with it the test
	// that guards it. Multiplying afterwards keeps the mix at the same two
	// multiplies per sample it has always cost, with the master gain still
	// applied per block on top so it reaches a ringing note.
	trim := e.trimForNote(note)

	left, right := gainsForNote(note)
	left *= trim
	right *= trim

	// Every arm below restrikes the slot's pooled voice before it commits to
	// the slot. That order is what keeps a refused note harmless: restrikeSlot
	// leaves the voice untouched when it fails, so the ringing note a retrigger
	// would have replaced keeps ringing, the voice a steal would have taken
	// keeps playing, and a freshly claimed slot is given straight back.
	for i := range e.voices {
		if e.voices[i].note == note {
			if !e.restrikeSlot(i, note, velocity) {
				return
			}

			e.voices[i].start(note, left, right)

			return
		}
	}

	if len(e.voices) >= e.maxVoices {
		// Steal the oldest voice. Rotating the slot to the back rather than
		// dropping it and appending a new one keeps its buffer, which is
		// already the right size, and its voice, which is already built.
		if !e.restrikeSlot(0, note, velocity) {
			return
		}

		last := len(e.voices) - 1
		stolen := e.voices[0]

		copy(e.voices, e.voices[1:])

		e.voices[last] = stolen
		e.voices[last].start(note, left, right)

		return
	}

	slot := e.claimSlot()
	if !e.restrikeSlot(slot, note, velocity) {
		e.voices = e.voices[:slot]

		return
	}

	e.voices[slot].start(note, left, right)
}

// restrikeSlot restrikes a slot's pooled voice as the given note and reports
// whether it succeeded, counting the refusal if it did not.
//
// The slot is addressed through the full-capacity view rather than the live
// list, because claimSlot's caller restrikes the slot before the slot is
// committed to, and because a stolen slot is restruck before it is rotated.
func (e *RealtimeEngine) restrikeSlot(slot, note, velocity int) bool {
	v := &e.voices[:cap(e.voices)][slot]

	if v.stream == nil {
		e.dropNoteOn(note)

		return false
	}

	if err := e.synth.ResetVoice(v.stream, note, velocity, e.noteDuration, e.renderOptions); err != nil {
		e.dropNoteOn(note)

		return false
	}

	return true
}

// dropNoteOn records a note-on the engine could not turn into sound.
func (e *RealtimeEngine) dropNoteOn(note int) {
	e.lastDroppedNote.Store(int32(note))
	e.droppedNoteOns.Add(1)
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

	// Same story as the buffer: every slot the engine was built with already
	// carries a voice, so this only ever runs for a slot appended past that
	// capacity. Building one here degrades that case to an allocation rather
	// than to a slot NoteOn has to refuse.
	if e.voices[slot].stream == nil {
		if voice, err := e.synth.newIdleVoice(); err == nil {
			voice.warm()

			e.voices[slot].stream = voice
		}
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
