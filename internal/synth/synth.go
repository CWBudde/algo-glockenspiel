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
	if duration <= 0 {
		return nil
	}

	totalSamples := int(math.Round(duration * float64(s.sampleRate)))
	if totalSamples <= 0 {
		return nil
	}

	params := s.scaledParamsForNote(note)
	if err := s.bar.UpdateParams(&params); err != nil {
		return nil
	}

	s.bar.Reset()

	threshold := math.Pow(10, options.DecayDBFS/20)
	if options.DecayDBFS == 0 {
		threshold = math.Pow(10, defaultDecayThreshold/20)
	}

	out := make([]float32, 0, totalSamples)
	remaining := totalSamples
	strikeVelocity := velocity
	consecutiveQuietBlocks := 0

	for remaining > 0 {
		n := s.blockSize
		if remaining < n {
			n = remaining
		}

		block := s.bar.Synthesize(strikeVelocity, n)
		strikeVelocity = 0

		if options.AutoStop && shouldStop(block, threshold) {
			consecutiveQuietBlocks++
			if consecutiveQuietBlocks >= autoStopBlockCount {
				break
			}
		} else {
			consecutiveQuietBlocks = 0
		}

		out = append(out, block...)
		remaining -= n
	}

	return out
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
