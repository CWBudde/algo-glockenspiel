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
type Voice struct {
	bar                    *model.Bar
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
func (s *Synthesizer) NewVoice(note, velocity int, duration float64, options RenderOptions) (*Voice, error) {
	if duration <= 0 {
		return nil, fmt.Errorf("duration must be positive: %g", duration)
	}

	totalSamples := int(math.Round(duration * float64(s.sampleRate)))
	if totalSamples <= 0 {
		return nil, fmt.Errorf("duration produced no samples: %g", duration)
	}

	params := s.scaledParamsForNote(note)

	bar, err := model.NewBar(&params, s.sampleRate)
	if err != nil {
		return nil, err
	}

	bar.Reset()

	threshold := math.Pow(10, options.DecayDBFS/20)
	if options.DecayDBFS == 0 {
		threshold = math.Pow(10, defaultDecayThreshold/20)
	}

	return &Voice{
		bar:              bar,
		remainingSamples: totalSamples,
		strikeVelocity:   velocity,
		autoStop:         options.AutoStop,
		threshold:        threshold,
		blockSize:        s.blockSize,
	}, nil
}

// scaledParamsForNote transposes the preset to another note. The clone is not
// optional: BarParams.Modes is a slice, so a plain struct copy would scale the
// preset's own modes on every note.
//
// Dividing DecayMs by the ratio is the physically right thing and stays: a bar
// an octave lower rings roughly twice as long, so transposing down has to
// lengthen the decay. What it means downstream is that the decays handed to
// model.NewBar are systematically larger than the ones the preset file holds --
// the shipped preset's 188.2 ms first mode becomes 1266 ms at MIDI note 36 --
// which is why model.ValidateBarParams measures against DecayMsValidationMax
// rather than the authoring bound DecayMsSearchMax. While those two were one
// constant at 500 ms, notes 36..52 could not be built at all.
func (s *Synthesizer) scaledParamsForNote(note int) model.BarParams {
	scaled := s.preset.Parameters.Clone()
	ratio := math.Pow(2, float64(note-s.preset.Note)/12)

	scaled.BaseFrequency *= ratio

	for i := range scaled.Modes {
		scaled.Modes[i].Frequency *= ratio
		if ratio > 0 {
			scaled.Modes[i].DecayMs /= ratio
		}
	}

	return scaled
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
func (v *Voice) RenderInto(dst []float32) int {
	if !v.Active() || len(dst) == 0 {
		return 0
	}

	n := len(dst)
	if n > v.blockSize {
		n = v.blockSize
	}

	if n > v.remainingSamples {
		n = v.remainingSamples
	}

	block := v.bar.Synthesize(v.strikeVelocity, n)
	v.strikeVelocity = 0

	copy(dst[:n], block)

	if v.autoStop && shouldStop(block, v.threshold) {
		v.consecutiveQuietBlocks++
		if v.consecutiveQuietBlocks >= autoStopBlockCount {
			v.done = true
		}
	} else {
		v.consecutiveQuietBlocks = 0
	}

	v.remainingSamples -= n
	if v.remainingSamples <= 0 {
		v.done = true
	}

	return n
}
