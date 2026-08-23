package synth

import (
	"math"
	"sync/atomic"

	"github.com/cwbudde/algo-glockenspiel/internal/oscbank"
	"github.com/cwbudde/algo-glockenspiel/model"
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
// web/src/lib/layout.ts and are the Go-side source of truth for anything that
// has to reason
// about the span of the instrument.
//
// The values themselves live in model, because preset validation needs them:
// whether a preset is well-formed depends on whether it survives transposition
// to both ends of this range. These are the engine's names for them.
//
// The narrower FIRST_NOTE/LAST_NOTE pair in web/src/lib/layout.ts (60..84)
// describes the
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

	// lane is the index this voice occupies in the engine's voice-major
	// oscillator banks, or -1 while the slot is not sounding. It is the one
	// thing about a slot that is *not* fixed to the slot: the rotor state for a
	// voice is a stride through a bank's arrays and cannot be moved cheaply, so
	// the lane has to travel with the note rather than with the slot, and the
	// slots are permuted by voice stealing and by retirement. A voice keeps the
	// lane it was given from note-on until it retires; a retiring voice hands
	// its lane back, and the next note-on takes the lowest free one so the
	// sounding voices stay packed into as few banks as possible.
	lane int
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

	// banks are the voice-major oscillator banks the whole engine renders
	// through, one per oscbank.LaneWidth lanes. Lane l lives in
	// banks[l/LaneWidth] at index l%LaneWidth.
	banks []*oscbank.VoiceBank

	// laneUsed marks the lanes a sounding voice holds. acquireLane takes the
	// lowest free one, which is what keeps the sounding voices packed low and
	// the number of banks a block has to walk proportional to the banks they
	// occupy rather than to the slot count.
	laneUsed []bool

	// laneVoice maps a lane to the index of the voice holding it, or -1. It is
	// rebuilt once per block, because the voice list is permuted between blocks
	// and the render has to walk lanes in bank order rather than list order.
	laneVoice []int32

	// interleavedIn and interleavedOut are one bank's [samples][LaneWidth]
	// excitation and output. One pair serves every bank because the banks are
	// processed one after another. Grow-only, like mixBuffer.
	interleavedIn  []float32
	interleavedOut []float32

	// noteTrims is the per-note level trim, indexed by note-trimsFirst, and
	// read once per note-on. Built by calibrateNoteTrims at construction, never
	// written afterwards, so the audio thread only ever loads from it.
	noteTrims  []float32
	trimsFirst int

	// reverb is the output bus effect, and the one thing in the engine that
	// keeps producing sound after every voice has retired. It is nil only if
	// the sample rate was rejected, which NewRealtimeEngine cannot report, so
	// every use of it is guarded.
	reverb *stereoReverb

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
		slots[i].lane = noLane

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
func NewRealtimeEngine(synthesizer *Synthesizer) *RealtimeEngine {
	engine := &RealtimeEngine{
		synth:        synthesizer,
		voices:       newVoiceSlots(synthesizer, defaultRealtimeMaxVoices, defaultRealtimeBlockFrames),
		mixBuffer:    make([]float32, defaultRealtimeBlockFrames*2),
		masterGain:   0.7,
		noteDuration: defaultVoiceDuration,
		renderOptions: RenderOptions{
			AutoStop:  true,
			DecayDBFS: defaultVoiceDecayDBFS,
		},
		maxVoices:      defaultRealtimeMaxVoices,
		laneUsed:       make([]bool, defaultRealtimeMaxVoices),
		laneVoice:      make([]int32, defaultRealtimeMaxVoices),
		interleavedIn:  make([]float32, defaultRealtimeBlockFrames*oscbank.LaneWidth),
		interleavedOut: make([]float32, defaultRealtimeBlockFrames*oscbank.LaneWidth),
		noteTrims:      calibrateNoteTrims(synthesizer),
		trimsFirst:     KeyboardFirstNote,
		reverb:         newEngineReverb(synthesizer.sampleRate),
	}

	engine.banks = newVoiceBanks(synthesizer, len(engine.laneUsed))

	return engine
}

// newEngineReverb builds the output bus reverb, or nil.
//
// A rate the reverb refuses leaves the engine dry rather than unbuilt. The
// constructor has nowhere to return an error to -- the same reason a voice that
// cannot be built leaves its slot empty -- and an instrument that plays without
// its room is a far better answer than one that does not play at all. The rate
// has already survived NewSynthesizer by the time it gets here, so this is a
// guard rather than a path.
func newEngineReverb(sampleRate int) *stereoReverb {
	r, err := newStereoReverb(sampleRate, DefaultReverbParams())
	if err != nil {
		return nil
	}

	return r
}

// noLane is the lane index of a slot that is not sounding.
const noLane = -1

// newVoiceBanks builds one voice-major bank per LaneWidth lanes and pins every
// lane of every bank to the preset's own shape.
//
// The pinning is not cosmetic. oscbank.VoiceBank infers its shape from the
// widest lane it holds, and a shape change discards every lane's rotor state --
// which on the audio path would be every sounding note going silent at once.
// Configuring all lanes here, with the preset's own oscillators, fixes the
// shape before the first note-on, and transposition never changes it: it scales
// frequencies and decays and leaves the mode and harmonic counts alone. Every
// later SetVoice therefore takes the unchanged-shape path and touches only the
// lane it names. TestNoteOnLeavesRingingVoicesUndisturbed is what pins that.
func newVoiceBanks(s *Synthesizer, lanes int) []*oscbank.VoiceBank {
	banks := make([]*oscbank.VoiceBank, (lanes+oscbank.LaneWidth-1)/oscbank.LaneWidth)

	for i := range banks {
		banks[i] = newVoiceBank(s)
	}

	return banks
}

func newVoiceBank(s *Synthesizer) *oscbank.VoiceBank {
	bank := oscbank.NewVoiceBank(float64(s.sampleRate))

	for lane := 0; lane < oscbank.LaneWidth; lane++ {
		// The error cannot fire: these are the oscillators a validated preset
		// built a bar from. Ignoring it keeps the constructor total, and a lane
		// that somehow failed to configure is silent rather than wrong.
		_ = bank.SetVoice(lane, s.bar.BankOscillators())
	}

	bank.Reset()

	return bank
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

// Silence retires every sounding voice at once, leaving the engine ready to
// play but with nothing ringing.
//
// It is not a note-off and there is no release: the voices are abandoned where
// they are. That is the point. It exists for the one thing that legitimately
// cuts a note off mid-decay -- swapping the sound out from under the player --
// and for the engine on the other side of that swap, which is the subtler half.
// An engine that stops being processed stops retiring voices, because
// retirement happens in ProcessBlock, so an engine kept for reuse comes back
// holding whatever was ringing when it was set aside and resumes it as if no
// time had passed. Silencing on the way in is what makes such an engine safe to
// keep.
//
// It runs where a note-on runs -- the caller's audio thread -- and does the
// same work retirement already does per block, so it allocates nothing and
// takes no lock.
func (e *RealtimeEngine) Silence() {
	for i := range e.voices {
		e.releaseLane(&e.voices[i])
	}

	// Truncating rather than clearing is what keeps the slots reusable: every
	// slot past the length still carries the buffer and the warmed voice it was
	// built with, and claimSlot re-slices into them rather than building more.
	e.voices = e.voices[:0]

	// The room goes with the notes. It is the same argument the paragraph above
	// makes for the voices: an engine set aside mid-tail and brought back later
	// would otherwise resume a room the player left minutes ago, and a tail
	// outliving the sound that caused it is the audible half of that.
	if e.reverb != nil {
		e.reverb.reset()
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

// SetReverbMix updates how much of the output goes through the room, from 0
// for a dry instrument to 1 for the most the engine offers.
//
// It is a live setter rather than a preset field on purpose. Changing a preset
// rebuilds the engine, which costs a per-note calibration sweep -- 165 to 190
// ms measured in the browser, against an audio queue about 11.6 ms deep -- and
// that is a price a dial someone drags cannot pay once per step. Nothing here
// reconfigures a delay line; it moves the target of a per-sample ramp.
func (e *RealtimeEngine) SetReverbMix(mix float32) {
	if e.reverb == nil {
		return
	}

	e.reverb.setMix(float64(mix))
}

// ReverbMix reports the mix the engine is set to, 0 if it has no reverb.
func (e *RealtimeEngine) ReverbMix() float32 {
	if e.reverb == nil {
		return 0
	}

	return float32(e.reverb.mix())
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

	// A retrigger or a steal reuses the lane the slot is already sounding on;
	// only a slot that is not sounding needs a new one. Taking the lowest free
	// lane is what packs the sounding voices into the low banks: a block pays
	// for one pass per bank that holds a sounding voice, not one per slot that
	// has ever been used.
	//
	// "One pass per occupied bank" is the honest form of that claim, and it is
	// weaker than ceil(polyphony/LaneWidth): a lane is released where its note
	// retired, so survivors can straddle more banks than their count needs, and
	// nothing moves a survivor down. Rotor state lives in its bank's arrays, so
	// compacting one means migrating that state, which VoiceBank has no API
	// for. Measured at 128 frames with eight sounding voices: 4423 ns in one
	// bank, 4748 across two, 5952 across four, 6604 across eight. The common
	// case is cheap and it self-heals, because a hole is the lowest free lane
	// and so the next note-on fills it; reaching four banks with eight voices
	// takes a prior 25-note chord, and eight takes 57. See "Known limits" in
	// docs/oscillator-bank.md.
	if v.lane == noLane {
		v.lane = e.acquireLane()
	}

	// Retuning the lane and clearing its rotors is the voice-major half of what
	// Bar.Reset does on the rotor-major path: the bar's own bank is no longer
	// what renders this note, so retuning the bar alone would leave the note
	// sounding at the previous note's pitch with the previous note's tail.
	bank, lane := e.bankFor(v.lane)

	// Unreachable for the same reason the constructor's SetVoice is: these are
	// the oscillators UpdateParams has just validated. Handled anyway, because
	// a silently misconfigured lane on the audio thread is worse than a counted
	// refusal.
	if err := bank.SetVoice(lane, v.stream.bar.BankOscillators()); err != nil {
		e.releaseLane(v)
		e.dropNoteOn(note)

		return false
	}

	bank.ResetVoice(lane)

	return true
}

// bankFor resolves a lane index to the bank that holds it and its index within
// that bank.
func (e *RealtimeEngine) bankFor(lane int) (*oscbank.VoiceBank, int) {
	return e.banks[lane/oscbank.LaneWidth], lane % oscbank.LaneWidth
}

// acquireLane takes the lowest free lane. There is one lane per slot, and a
// slot holds at most one lane, so it cannot fail for a slot the engine built;
// the fallback grows the bookkeeping the same way claimSlot grows the slot
// list, which only a caller that raised maxVoices past the engine's capacity
// can reach.
func (e *RealtimeEngine) acquireLane() int {
	for lane := range e.laneUsed {
		if !e.laneUsed[lane] {
			e.laneUsed[lane] = true

			return lane
		}
	}

	return e.growLanes()
}

// growLanes appends one lane, and a bank for it when the last one is full.
// Reachable only past the engine's built capacity, and it allocates, which is
// why every path that can run on the audio thread stays inside the capacity.
func (e *RealtimeEngine) growLanes() int {
	lane := len(e.laneUsed)

	e.laneUsed = append(e.laneUsed, true)
	e.laneVoice = append(e.laneVoice, noLane)

	if len(e.banks)*oscbank.LaneWidth <= lane {
		e.banks = append(e.banks, newVoiceBank(e.synth))
	}

	return lane
}

// releaseLane hands a retiring voice's lane back and clears its rotor state, so
// the next note that takes the lane starts from silence and a retired note
// stops costing rotor work.
func (e *RealtimeEngine) releaseLane(v *realtimeVoice) {
	if v.lane == noLane {
		return
	}

	bank, lane := e.bankFor(v.lane)
	bank.ResetVoice(lane)

	e.laneUsed[v.lane] = false
	v.lane = noLane
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
		e.voices = append(e.voices, realtimeVoice{lane: noLane})
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
//
// The whole engine renders through the voice-major banks: every sounding
// voice's excitation is gathered into one interleaved buffer, one
// VoiceBank.ProcessBlock advances every lane of a bank at once, and the result
// is deinterleaved straight into the per-voice gain and mix. What that buys is
// a cost curve that steps rather than climbs -- a bank costs the same whether
// one lane of it is sounding or all eight -- and what it costs is the idle
// lanes: below LaneWidth sounding voices this is slower than rendering each
// voice through its own rotor-major bank. See "The realtime render path" in
// docs/oscillator-bank.md.
//
// Only the rotor bank is shared. The excitation lowpass, the Chebyshev shaper
// at either stage and the dry mix all stay per voice inside model.Bar, which is
// why the loop below is three passes: start every lane's chain up to the bank,
// run the bank, then finish every lane's chain after it.
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

	// A voice renders at most its own block size per pass, so a callback wider
	// than that is covered by several passes rather than by one that leaves the
	// tail silent. Getting this wrong is not subtle: at the demo's 512-frame
	// callback and a 128-sample block, three quarters of every buffer stayed at
	// zero and the output was a 94 Hz gate rather than a note.
	for offset := 0; offset < frames; {
		segment := min(frames-offset, e.synth.blockSize)

		e.renderBanks(segment, buf[offset*2:(offset+segment)*2])

		offset += segment
	}

	e.retireVoices()

	// The reverb runs after the voices and before the clip: it is a bus effect,
	// so it sees the finished mix, and the clip stays the last thing that
	// touches the buffer so it still bounds everything that leaves the engine.
	//
	// It is also why a block can be non-silent with no voices in it at all.
	// Retirement above is about the oscillators; the tail outlives them by
	// design, and anything downstream that infers "nothing is playing" from
	// ActiveVoices would cut it off.
	if e.reverb != nil {
		e.reverb.process(buf)
	}

	for i := range buf {
		buf[i] = hardClip(buf[i])
	}

	return buf
}

// renderBanks runs one pass of every bank that holds a sounding voice and mixes
// segment frames of the result into buf.
//
// segment must not exceed the synthesizer's block size, and buf must hold
// segment interleaved stereo frames. ProcessBlock is the only caller and
// derives both from the same min, so the bound lives there rather than being
// re-applied here: a second cap would silently render less than buf was sized
// for, which is the failure this whole change set exists to remove. Exceeding
// it would desynchronise the bank from the voices feeding it -- startBlock caps
// itself at the voice's block size, so the rotors would advance further than
// the excitation gathered for them.
func (e *RealtimeEngine) renderBanks(segment int, buf []float32) {
	highest := e.mapLanes(segment)
	if highest == noLane {
		return
	}

	e.ensureInterleaved(segment)

	// Read the master gain once per block. Folding it into the per-voice pan
	// coefficients here rather than at note-on is what makes a gain change
	// audible on notes that are already sounding, and hoisting it out of the
	// inner loop keeps the mix at the same two multiplies per sample it cost
	// when the gain was baked into the slot.
	gain := e.masterGain

	width := oscbank.LaneWidth
	excitation := e.interleavedIn[:segment*width]
	output := e.interleavedOut[:segment*width]

	for bank := 0; bank <= highest/width; bank++ {
		base := bank * width

		var lengths [oscbank.LaneWidth]int

		sounding := false

		clear(excitation)

		for lane := range width {
			slot := e.laneVoice[base+lane]
			if slot < 0 {
				continue
			}

			src := e.voices[slot].stream.startBlock(segment)
			lengths[lane] = len(src)

			if len(src) == 0 {
				// The lane is held by a voice that has finished but has not
				// been released yet, because retirement runs once per callback
				// rather than once per pass. It contributes nothing, so it must
				// not be what keeps the bank pass alive.
				continue
			}

			for i, x := range src {
				excitation[i*width+lane] = x
			}

			sounding = true
		}

		if !sounding {
			continue
		}

		e.banks[bank].ProcessBlock(excitation, output)

		for lane := range width {
			n := lengths[lane]
			if n == 0 {
				continue
			}

			v := &e.voices[e.laneVoice[base+lane]]

			// Lifted straight into the slot's own block buffer, and finished
			// in place there: the post-bank chain is elementwise, so a
			// separate deinterleave buffer would only buy an extra copy of
			// every sample.
			lifted := v.buffer[:n]
			for i := range lifted {
				lifted[i] = output[i*width+lane]
			}

			// A voice can retire partway through a pass, and what it returns
			// is how much of the block is actually its own. Mixing the whole
			// block instead would carry the tail past the point the voice
			// stopped at, which is the difference between the two ways a note
			// can end rather than a rounding.
			sounded := v.stream.finishBlock(lifted, lifted)

			left := v.left * gain
			right := v.right * gain

			for i := 0; i < sounded; i++ {
				sample := v.buffer[i]
				buf[i*2] += sample * left
				buf[i*2+1] += sample * right
			}
		}
	}
}

// mapLanes rebuilds the lane-to-voice index and returns the highest lane a
// sounding voice holds, or noLane if none does. It also grows any voice buffer
// the block has outgrown, which is the one place a wider callback block is
// allowed to allocate.
//
// Indexing rather than ranging by value is load-bearing: over a copy, the
// buffer growth is written to the copy and discarded, so a block larger than
// the buffer would reallocate on every block instead of once.
func (e *RealtimeEngine) mapLanes(frames int) int {
	for i := range e.laneVoice {
		e.laneVoice[i] = noLane
	}

	highest := noLane

	for i := range e.voices {
		v := &e.voices[i]

		if cap(v.buffer) < frames {
			v.buffer = make([]float32, frames)
		}

		if v.lane == noLane {
			continue
		}

		e.laneVoice[v.lane] = int32(i)

		if v.lane > highest {
			highest = v.lane
		}
	}

	return highest
}

// ensureInterleaved grows the gather and scatter buffers, which are grow-only
// for the same reason mixBuffer is: a host that raises its block size should
// cost one allocation, not one per block.
func (e *RealtimeEngine) ensureInterleaved(segment int) {
	if len(e.interleavedIn) >= segment*oscbank.LaneWidth {
		return
	}

	e.interleavedIn = make([]float32, segment*oscbank.LaneWidth)
	e.interleavedOut = make([]float32, segment*oscbank.LaneWidth)
}

// retireVoices compacts the voice list, dropping the voices that finished in
// this block and handing their lanes back.
//
// Swap rather than assign: an overwrite would drop the retired voice's buffer
// and leave two slots pointing at one buffer. Swapping permutes the slots, so
// every buffer stays owned by exactly one slot and the retired ones wait past
// the end for claimSlot to hand them out again.
func (e *RealtimeEngine) retireVoices() {
	writeIndex := 0

	for i := range e.voices {
		if !e.voices[i].stream.Active() {
			e.releaseLane(&e.voices[i])

			continue
		}

		if writeIndex != i {
			e.voices[writeIndex], e.voices[i] = e.voices[i], e.voices[writeIndex]
		}

		writeIndex++
	}

	e.voices = e.voices[:writeIndex]
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
