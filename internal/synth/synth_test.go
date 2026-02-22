package synth

import (
	"path/filepath"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/preset"
)

func TestRenderNoteLength(t *testing.T) {
	p := loadTestPreset(t)

	s, err := NewSynthesizer(p, 48000)
	if err != nil {
		t.Fatalf("new synthesizer failed: %v", err)
	}

	got := s.RenderNote(69, 100, 1.0)
	if len(got) != 48000 {
		t.Fatalf("unexpected sample count: got %d want %d", len(got), 48000)
	}
}

func TestRenderNoteAutoStop(t *testing.T) {
	p := loadTestPreset(t)
	for i := range p.Parameters.Modes {
		p.Parameters.Modes[i].DecayMs = 0.1
	}

	s, err := NewSynthesizer(p, 48000)
	if err != nil {
		t.Fatalf("new synthesizer failed: %v", err)
	}

	full := s.RenderNote(69, 100, 2.0)
	short := s.RenderNoteWithOptions(69, 100, 2.0, RenderOptions{
		AutoStop:  true,
		DecayDBFS: 20,
	})

	if len(short) >= len(full) {
		t.Fatalf("expected auto-stop render to be shorter: auto=%d full=%d", len(short), len(full))
	}
}

func TestRenderDifferentDurations(t *testing.T) {
	p := loadTestPreset(t)
	s, err := NewSynthesizer(p, 44100)
	if err != nil {
		t.Fatalf("new synthesizer failed: %v", err)
	}

	a := s.RenderNote(69, 100, 0.25)
	b := s.RenderNote(69, 100, 0.5)

	if len(a) >= len(b) {
		t.Fatalf("expected longer duration to produce more samples: a=%d b=%d", len(a), len(b))
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
