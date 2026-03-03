package synth

import (
	"fmt"
	"math"

	"github.com/cwbudde/glockenspiel/internal/model"
	"github.com/cwbudde/glockenspiel/internal/preset"
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

func (s *Synthesizer) scaledParamsForNote(note int) model.BarParams {
	scaled := s.preset.Parameters
	ratio := math.Pow(2, float64(note-s.preset.Note)/12)

	scaled.BaseFrequency *= ratio
	for i := 0; i < model.NumModes; i++ {
		scaled.Modes[i].Frequency *= ratio
		if ratio > 0 {
			scaled.Modes[i].DecayMs /= ratio
		}
	}

	return scaled
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
