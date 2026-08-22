package synth

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/preset"
)

func BenchmarkRenderNoteDefaultA4(b *testing.B) {
	p, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		b.Fatalf("load default preset: %v", err)
	}

	engine, err := NewSynthesizer(p, 44100)
	if err != nil {
		b.Fatalf("NewSynthesizer failed: %v", err)
	}

	const duration = 2.0

	sampleCount := int(duration * 44100)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = engine.RenderNote(69, 100, duration)
	}

	elapsed := b.Elapsed().Seconds()
	if elapsed > 0 {
		b.ReportMetric(float64(b.N)/elapsed, "render/s")
		b.ReportMetric(float64(sampleCount*b.N)/elapsed, "samples/s")
	}
}

func BenchmarkRealtimeEngineRetriggeredC5(b *testing.B) {
	p, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		b.Fatalf("load default preset: %v", err)
	}

	synthesizer, err := NewSynthesizer(p, 48000)
	if err != nil {
		b.Fatalf("NewSynthesizer failed: %v", err)
	}

	const (
		blockFrames = 128
		totalFrames = 48000
		note        = 72
		velocity    = 100
	)

	triggerFrames := map[int]struct{}{
		0:     {},
		12000: {},
		24000: {},
		36000: {},
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		engine := NewRealtimeEngine(synthesizer)

		for frame := 0; frame < totalFrames; frame += blockFrames {
			if _, ok := triggerFrames[frame]; ok {
				engine.NoteOn(note, velocity)
			}

			_ = engine.ProcessBlock(blockFrames)
		}
	}

	elapsed := b.Elapsed().Seconds()
	if elapsed > 0 {
		b.ReportMetric(float64(b.N)/elapsed, "runs/s")
		b.ReportMetric(float64(totalFrames*b.N)/elapsed, "frames/s")
	}
}

func BenchmarkRealtimeEnginePolyphonicPattern(b *testing.B) {
	p, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		b.Fatalf("load default preset: %v", err)
	}

	synthesizer, err := NewSynthesizer(p, 48000)
	if err != nil {
		b.Fatalf("NewSynthesizer failed: %v", err)
	}

	const (
		blockFrames = 128
		totalFrames = 48000
		velocity    = 100
	)

	pattern := map[int]int{
		0:     72,
		6000:  76,
		12000: 79,
		18000: 84,
		24000: 79,
		30000: 76,
		36000: 72,
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		engine := NewRealtimeEngine(synthesizer)

		for frame := 0; frame < totalFrames; frame += blockFrames {
			if note, ok := pattern[frame]; ok {
				engine.NoteOn(note, velocity)
			}

			_ = engine.ProcessBlock(blockFrames)
		}
	}

	elapsed := b.Elapsed().Seconds()
	if elapsed > 0 {
		b.ReportMetric(float64(b.N)/elapsed, "runs/s")
		b.ReportMetric(float64(totalFrames*b.N)/elapsed, "frames/s")
	}
}

// BenchmarkRealtimeEngineVoiceCount sweeps the number of simultaneously
// sounding voices and measures ProcessBlock alone. The shape of that curve is
// what the voice-major banks are for: one bank advances oscbank.LaneWidth lanes
// whether one of them is sounding or all eight, so the *rotor* part of the cost
// steps at every multiple of the lane width instead of climbing with every
// voice.
//
// It sweeps the rotor count too, and that dimension is the honest half of the
// measurement. The shipped preset is four modes with no harmonics, so a voice
// is four rotors and the bank is a small part of what a voice costs -- the
// per-voice excitation lowpass is a larger one, and it does not pack. The
// 4x4 case gives every mode four harmonics, which is sixteen rotors, and is
// where the bank is the dominant term and the step is visible.
//
// The notes are held rather than struck and left to decay: auto-stop is off and
// the duration is long, so the voice count under measurement is the voice count
// for the whole run. That is not the shipping configuration and is not meant to
// be -- it is the only way to put a fixed number on the x axis.
func BenchmarkRealtimeEngineVoiceCount(b *testing.B) {
	const (
		blockFrames = 128
		velocity    = 100
	)

	for _, shape := range []struct {
		name      string
		harmonics int
	}{
		{name: "modes=4x1", harmonics: 0},
		{name: "modes=4x4", harmonics: 4},
	} {
		synthesizer := benchSynthesizer(b, shape.harmonics)

		for _, voices := range []int{1, 2, 4, 8, 16} {
			b.Run(fmt.Sprintf("%s/voices=%d", shape.name, voices), func(b *testing.B) {
				engine := NewRealtimeEngine(synthesizer)
				engine.renderOptions.AutoStop = false
				engine.noteDuration = 3600

				for i := 0; i < voices; i++ {
					engine.NoteOn(KeyboardFirstNote+3*i, velocity)
				}

				if engine.ActiveVoices() != voices {
					b.Fatalf("expected %d sounding voices, got %d", voices, engine.ActiveVoices())
				}

				b.ReportAllocs()
				b.ResetTimer()

				for i := 0; i < b.N; i++ {
					_ = engine.ProcessBlock(blockFrames)
				}

				elapsed := b.Elapsed().Seconds()
				if elapsed > 0 {
					b.ReportMetric(float64(blockFrames*b.N)/elapsed, "frames/s")
					b.ReportMetric(float64(blockFrames*b.N*voices)/elapsed, "voice-frames/s")
				}
			})
		}
	}
}

// benchSynthesizer loads the shipped preset and optionally gives every mode the
// same harmonic series, which is how the sweep reaches a rotor count where the
// oscillator bank rather than the excitation filter is the dominant cost.
func benchSynthesizer(b *testing.B, harmonics int) *Synthesizer {
	b.Helper()

	p, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		b.Fatalf("load default preset: %v", err)
	}

	if harmonics > 0 {
		p.Version = preset.VersionV2

		gains := make([]float64, harmonics)
		for i := range gains {
			gains[i] = 1 / float64(i+1)
		}

		for i := range p.Parameters.Modes {
			p.Parameters.Modes[i].Harmonics = append([]float64(nil), gains...)
		}
	}

	synthesizer, err := NewSynthesizer(p, 48000)
	if err != nil {
		b.Fatalf("NewSynthesizer failed: %v", err)
	}

	return synthesizer
}
