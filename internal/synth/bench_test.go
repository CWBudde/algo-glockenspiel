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
