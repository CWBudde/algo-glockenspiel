package synth

import (
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
