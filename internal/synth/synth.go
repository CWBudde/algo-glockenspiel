package synth

import (
	"fmt"
	"math"

	"github.com/cwbudde/glockenspiel/internal/preset"
	"github.com/cwbudde/glockenspiel/model"
)

const (
	defaultBlockSize      = 128
	defaultDecayThreshold = -90.0
	autoStopBlockCount    = 8
)

// RenderOptions controls note rendering behavior.
type RenderOptions struct {
	AutoStop  bool
	DecayDBFS float64
}

// Voice streams a single struck note incrementally.
//
// scratch is the voice's own transposition workspace. A voice that is reset for
// a new note has to land the transposed parameters somewhere before handing
// them to the bar, and on the audio thread that somewhere must not be freshly
// allocated -- so every voice carries one, sized on its first use and reused
// for the rest of its life. It holds no state between notes; it is a buffer,
// not a field of the voice's identity.
type Voice struct {
	bar                    *model.Bar
	scratch                model.BarParams
	remainingSamples       int
	strikeVelocity         int
	autoStop               bool
	threshold              float64
	consecutiveQuietBlocks int
	blockSize              int
	done                   bool
}

// Synthesizer orchestrates note rendering from a preset.
type Synthesizer struct {
	bar        *model.Bar
	preset     *preset.Preset
	sampleRate int
	blockSize  int
}

// NewSynthesizer initializes synthesis from a preset.
func NewSynthesizer(presetConfig *preset.Preset, sampleRate int) (*Synthesizer, error) {
	if sampleRate <= 0 {
		return nil, fmt.Errorf("sample rate must be positive: %d", sampleRate)
	}

	if err := preset.Validate(presetConfig); err != nil {
		return nil, err
	}

	bar, err := model.NewBar(&presetConfig.Parameters, sampleRate)
	if err != nil {
		return nil, err
	}

	return &Synthesizer{
		bar:        bar,
		preset:     presetConfig,
		sampleRate: sampleRate,
		blockSize:  defaultBlockSize,
	}, nil
}

// RenderNote renders a note for the requested duration.
func (s *Synthesizer) RenderNote(note, velocity int, duration float64) []float32 {
	return s.RenderNoteWithOptions(note, velocity, duration, RenderOptions{})
}

// RenderNoteWithOptions renders a note with additional stop controls.
func (s *Synthesizer) RenderNoteWithOptions(note, velocity int, duration float64, options RenderOptions) []float32 {
	voice, err := s.NewVoice(note, velocity, duration, options)
	if err != nil {
		return nil
	}

	out := make([]float32, 0, voice.remainingSamples)

	buf := make([]float32, s.blockSize)
	for voice.Active() {
		n := voice.RenderInto(buf)
		if n == 0 {
			break
		}

		out = append(out, buf[:n]...)
	}

	return out
}

// NewVoice prepares a streaming note voice.
//
// It is the allocating form, for callers that are not on the audio thread: it
// builds the bar and then hands it to ResetVoice, so there is exactly one
// definition of what a voice reset means rather than one here and one there.
// A caller that already owns a voice -- the realtime engine, whose slots are
// pooled -- calls ResetVoice directly and allocates nothing.
func (s *Synthesizer) NewVoice(note, velocity int, duration float64, options RenderOptions) (*Voice, error) {
	// Checked before the bar is built so that a bad duration is reported as a
	// bad duration, exactly as it was when the two checks stood at the top of
	// this function.
	if _, err := s.durationSamples(duration); err != nil {
		return nil, err
	}

	voice, err := s.newIdleVoice()
	if err != nil {
		return nil, err
	}

	if err := s.ResetVoice(voice, note, velocity, duration, options); err != nil {
		return nil, err
	}

	return voice, nil
}

// newIdleVoice builds a voice whose bar is configured for the preset's own note
// and which has struck nothing yet. Every voice starts life this way, including
// the pooled slots the realtime engine builds at construction; ResetVoice is
// what turns one into a particular note.
//
// The bar is built from the voice's own scratch parameters rather than from the
// preset directly, so the scratch slices exist at the right shape from the
// start and the first ResetVoice already reuses them.
func (s *Synthesizer) newIdleVoice() (*Voice, error) {
	voice := &Voice{blockSize: s.blockSize}

	s.preset.Parameters.CopyInto(&voice.scratch)

	bar, err := model.NewBar(&voice.scratch, s.sampleRate)
	if err != nil {
		return nil, err
	}

	voice.bar = bar

	return voice, nil
}

// ResetVoice restrikes an existing voice as another note, without allocating.
//
// This is the audio-thread form of NewVoice: the bar is retuned in place
// through model.Bar.UpdateParams, which reuses the bar's own slices when the
// mode and harmonic counts are unchanged -- and they are, since every note of a
// preset has the same shape and only its frequencies and decays move.
//
// The Reset is not optional and is the whole correctness argument for pooling.
// A bar carries oscillator phase and the excitation lowpass's delay line, and
// model.Bar.setLowpass deliberately retunes the filter without clearing that
// delay line, because a parameter change mid-note is not a discontinuity in the
// signal. A new note is: without Reset, the tail of the previous note would
// leak through the filter's memory into the new strike, and a pooled slot would
// render a note differently from a freshly built voice. TestPooledVoiceMatches*
// in realtime_pooling_test.go pins that it does not.
//
// On failure nothing but scratch has been touched: the duration is checked
// first, and UpdateParams validates before it writes. A caller may therefore
// treat a refused reset as having left the voice exactly as it found it, which
// is what lets the engine refuse a note without disturbing the one that slot is
// already playing.
func (s *Synthesizer) ResetVoice(v *Voice, note, velocity int, duration float64, options RenderOptions) error {
	totalSamples, err := s.durationSamples(duration)
	if err != nil {
		return err
	}

	s.scaledParamsForNoteInto(&v.scratch, note)

	if err := v.bar.UpdateParams(&v.scratch); err != nil {
		return err
	}

	v.bar.Reset()

	threshold := math.Pow(10, options.DecayDBFS/20)
	if options.DecayDBFS == 0 {
		threshold = math.Pow(10, defaultDecayThreshold/20)
	}

	v.remainingSamples = totalSamples
	v.strikeVelocity = velocity
	v.autoStop = options.AutoStop
	v.threshold = threshold
	v.consecutiveQuietBlocks = 0
	v.done = false
	v.blockSize = s.blockSize

	return nil
}

// warm renders one silent block so that model.Bar.ensureBuffers allocates its
// five working buffers here rather than during the first block of the first
// real note. Pooling the voices is pointless if the audio thread still pays for
// their buffers the first time each slot sounds.
//
// The block is a strike of velocity 0, which is silence in, silence out, and
// the Reset afterwards leaves the bar in the same state a fresh one is in.
func (v *Voice) warm() {
	v.bar.Synthesize(0, v.blockSize)
	v.bar.Reset()
}

// durationSamples converts a note duration to a sample count, rejecting the two
// durations that cannot produce a note.
func (s *Synthesizer) durationSamples(duration float64) (int, error) {
	if duration <= 0 {
		return 0, fmt.Errorf("duration must be positive: %g", duration)
	}

	totalSamples := int(math.Round(duration * float64(s.sampleRate)))
	if totalSamples <= 0 {
		return 0, fmt.Errorf("duration produced no samples: %g", duration)
	}

	return totalSamples, nil
}

// scaledParamsForNote transposes the preset to another note. The clone is not
// optional: BarParams.Modes is a slice, so a plain struct copy would scale the
// preset's own modes on every note.
//
// The transposition itself lives in model.TransposeToNote, shared with the
// plugin and with preset validation. Validation decides whether a preset is
// playable by transposing it to the ends of the keyboard, so it has to compute
// the same ratio this does, down to the last bit -- otherwise a preset could be
// cleared for a note that then fails to build.
//
// What it means downstream is that the decays handed to model.NewBar are
// systematically larger than the ones the preset file holds -- the shipped
// preset's 188.2 ms first mode becomes 1266 ms at MIDI note 36 -- which is why
// model.ValidateBarParams measures against DecayMsValidationMax rather than the
// authoring bound DecayMsSearchMax. While those two were one constant at 500 ms,
// notes 36..52 could not be built at all.
func (s *Synthesizer) scaledParamsForNote(note int) model.BarParams {
	scaled := s.preset.Parameters.Clone()
	model.TransposeToNote(&scaled, s.preset.Note, note)

	return scaled
}

// scaledParamsForNoteInto is the same transposition into a destination the
// caller already owns. It is what the audio path uses: BarParams.CopyInto
// reuses dst's slices whenever their capacity suffices, so a voice that has
// been struck once transposes every later note without touching the allocator.
//
// The value-returning form above stays for the offline render path, where the
// caller has no destination to offer and a Clone is the clearer thing to write.
func (s *Synthesizer) scaledParamsForNoteInto(dst *model.BarParams, note int) {
	s.preset.Parameters.CopyInto(dst)
	model.TransposeToNote(dst, s.preset.Note, note)
}

// peakForNote renders one strike of the given note and returns its peak level
// in linear units, or 0 if the note cannot be rendered at all.
//
// It exists to calibrate the realtime engine's per-note level trim, and it is
// deliberately a measurement rather than a formula. See calibrateNoteTrims in
// realtime.go for why the level cannot be predicted from the note alone.
//
// The render window is the longest decay the preset has *after* transposition,
// which is where the naive choice goes wrong: the peak of a multi-mode bar is
// not the attack transient. The modes beat against each other, and the sample
// where they first line up can be far into the note -- 167.5 ms for the shipped
// preset at MIDI 36, where a fixed 50 ms window would have measured a peak
// 4.7 dB below the real one and left the trim that much too loud. Measured
// across both presets in the repo and every note of the keyboard, the true peak
// always lands within half of that window, so one whole decay carries a factor
// of two in hand.
//
// The floor keeps a very short decay from producing a window of a few samples.
// The ceiling can never bind for a preset that passed validation, since
// DecayMsValidationMax is the largest transposed decay that exists; it is there
// so that a future ceiling change degrades into a slightly low measurement
// rather than into a multi-second engine construction.
func (s *Synthesizer) peakForNote(note, velocity int) float64 {
	const (
		minWindowSeconds = 0.02
		maxWindowSeconds = model.DecayMsValidationMax / 1000
	)

	scaled := s.scaledParamsForNote(note)

	window := 0.0

	for i := range scaled.Modes {
		if decay := scaled.Modes[i].DecayMs / 1000; decay > window {
			window = decay
		}
	}

	window = math.Min(math.Max(window, minWindowSeconds), maxWindowSeconds)

	peak := 0.0

	for _, sample := range s.RenderNote(note, velocity, window) {
		if abs := math.Abs(float64(sample)); abs > peak {
			peak = abs
		}
	}

	return peak
}

func shouldStop(block []float32, threshold float64) bool {
	if len(block) == 0 {
		return true
	}

	sum := 0.0

	for _, x := range block {
		v := float64(x)
		sum += v * v
	}

	rms := math.Sqrt(sum / float64(len(block)))

	return rms < threshold
}

// Active reports whether the voice can still render audio.
func (v *Voice) Active() bool {
	return v != nil && !v.done && v.remainingSamples > 0
}

// RenderInto writes the next chunk into dst and returns the sample count written.
//
// This is the whole-chain form, where the voice's own bar runs its own
// rotor-major bank. The realtime engine does not use it: it drives startBlock
// and finishBlock instead so that one voice-major bank can serve every sounding
// voice at once. Both forms share blockLength and advance, so there is one
// definition of how long a block is and one of what finishing it means.
func (v *Voice) RenderInto(dst []float32) int {
	n := v.blockLength(len(dst))
	if n == 0 {
		return 0
	}

	block := v.bar.Synthesize(v.strikeVelocity, n)
	v.strikeVelocity = 0

	copy(dst[:n], block)

	v.advance(block)

	return n
}

// startBlock runs the pre-bank half of the chain for a block of at most n
// samples and returns the excitation an oscillator bank should be fed, or nil
// if the voice has nothing left to render.
//
// The returned slice aliases the bar's working buffers and stays valid until
// the next call on this voice, which is what lets the engine gather it straight
// into its interleaved input.
func (v *Voice) startBlock(n int) []float32 {
	n = v.blockLength(n)
	if n == 0 {
		return nil
	}

	in := v.bar.StartBankInput(v.strikeVelocity, n)
	v.strikeVelocity = 0

	return in
}

// finishBlock runs the post-bank half of the chain over one voice's share of a
// bank's output, writes it into dst and does the block bookkeeping. bankOut
// must be the deinterleaved output of the block startBlock last prepared.
func (v *Voice) finishBlock(bankOut, dst []float32) {
	v.advance(v.bar.FinishBankOutput(bankOut, dst))
}

// blockLength returns how many samples the voice will contribute to a request
// of n samples: the render block size and the note's remaining length both cap
// it, and a voice that is done contributes nothing.
func (v *Voice) blockLength(n int) int {
	if !v.Active() || n <= 0 {
		return 0
	}

	if n > v.blockSize {
		n = v.blockSize
	}

	if n > v.remainingSamples {
		n = v.remainingSamples
	}

	return n
}

// advance applies the auto-stop rule and the remaining-sample count to a block
// the voice has just rendered.
func (v *Voice) advance(block []float32) {
	if v.autoStop && shouldStop(block, v.threshold) {
		v.consecutiveQuietBlocks++
		if v.consecutiveQuietBlocks >= autoStopBlockCount {
			v.done = true
		}
	} else {
		v.consecutiveQuietBlocks = 0
	}

	v.remainingSamples -= len(block)
	if v.remainingSamples <= 0 {
		v.done = true
	}
}
