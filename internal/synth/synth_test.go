package synth

import (
	"path/filepath"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/preset"
)

func TestRenderNoteLength(t *testing.T) {
	p := loadTestPreset(t)

	synthesizer, err := NewSynthesizer(p, 48000)
	if err != nil {
		t.Fatalf("new synthesizer failed: %v", err)
	}

	got := synthesizer.RenderNote(69, 100, 1.0)
	if len(got) != 48000 {
		t.Fatalf("unexpected sample count: got %d want %d", len(got), 48000)
	}
}

func TestRenderNoteAutoStop(t *testing.T) {
	p := loadTestPreset(t)
	for i := range p.Parameters.Modes {
		p.Parameters.Modes[i].DecayMs = 0.1
	}

	synthesizer, err := NewSynthesizer(p, 48000)
	if err != nil {
		t.Fatalf("new synthesizer failed: %v", err)
	}

	full := synthesizer.RenderNote(69, 100, 2.0)
	short := synthesizer.RenderNoteWithOptions(69, 100, 2.0, RenderOptions{
		AutoStop:  true,
		DecayDBFS: 20,
	})

	if len(short) >= len(full) {
		t.Fatalf("expected auto-stop render to be shorter: auto=%d full=%d", len(short), len(full))
	}
}

func TestRenderDifferentDurations(t *testing.T) {
	p := loadTestPreset(t)

	synthesizer, err := NewSynthesizer(p, 44100)
	if err != nil {
		t.Fatalf("new synthesizer failed: %v", err)
	}

	a := synthesizer.RenderNote(69, 100, 0.25)
	b := synthesizer.RenderNote(69, 100, 0.5)

	if len(a) >= len(b) {
		t.Fatalf("expected longer duration to produce more samples: a=%d b=%d", len(a), len(b))
	}
}

func TestVoiceRenderMatchesRenderNote(t *testing.T) {
	p := loadTestPreset(t)

	synthesizer, err := NewSynthesizer(p, 44100)
	if err != nil {
		t.Fatalf("new synthesizer failed: %v", err)
	}

	full := synthesizer.RenderNoteWithOptions(69, 100, 0.5, RenderOptions{
		AutoStop:  true,
		DecayDBFS: -72,
	})

	voice, err := synthesizer.NewVoice(69, 100, 0.5, RenderOptions{
		AutoStop:  true,
		DecayDBFS: -72,
	})
	if err != nil {
		t.Fatalf("NewVoice failed: %v", err)
	}

	chunk := make([]float32, 37)
	streamed := make([]float32, 0, len(full))
	for voice.Active() {
		n := voice.RenderInto(chunk)
		if n == 0 {
			break
		}
		streamed = append(streamed, chunk[:n]...)
	}

	if len(streamed) != len(full) {
		t.Fatalf("streamed length mismatch: got %d want %d", len(streamed), len(full))
	}

	for i := range full {
		if streamed[i] != full[i] {
			t.Fatalf("sample %d mismatch: got %g want %g", i, streamed[i], full[i])
		}
	}
}

func loadTestPreset(t *testing.T) *preset.Preset {
	t.Helper()

	path := filepath.FromSlash("../../assets/presets/default.json")

	p, err := preset.Load(path)
	if err != nil {
		t.Fatalf("failed to load test preset: %v", err)
	}

	return p
}
