package synth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/preset"
	"github.com/cwbudde/glockenspiel/model"
)

func shippedPresetPaths(t *testing.T) []string {
	t.Helper()

	roots := []string{
		filepath.FromSlash("../../assets/presets"),
		filepath.FromSlash("../../testdata/presets"),
	}

	var paths []string

	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatalf("read %s: %v", root, err)
		}

		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".json") {
				paths = append(paths, filepath.Join(root, entry.Name()))
			}
		}
	}

	return paths
}

func renderPreset(t *testing.T, p *preset.Preset) []float32 {
	t.Helper()

	synthesizer, err := NewSynthesizer(p, 44100)
	if err != nil {
		t.Fatalf("NewSynthesizer: %v", err)
	}

	return synthesizer.RenderNote(p.Note, 100, 1.0)
}

func assertSamplesIdentical(t *testing.T, label string, want, got []float32) {
	t.Helper()

	if len(want) != len(got) {
		t.Fatalf("%s: rendered %d samples, want %d", label, len(got), len(want))
	}

	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("%s: sample %d = %v, want %v", label, i, got[i], want[i])
		}
	}
}

// TestShippedPresetsRenderIdenticallyAfterRoundTrip is the Phase 1 compatibility
// gate: a v1 preset must sound the same whether it is rendered as loaded, after
// a save/load cycle, or after being upgraded to the v2 schema.
func TestShippedPresetsRenderIdenticallyAfterRoundTrip(t *testing.T) {
	paths := shippedPresetPaths(t)
	if len(paths) == 0 {
		t.Fatal("no preset fixtures found")
	}

	for _, path := range paths {
		original, err := preset.Load(path)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}

		reference := renderPreset(t, original)
		if len(reference) == 0 {
			t.Fatalf("%s rendered nothing", path)
		}

		saved := filepath.Join(t.TempDir(), "round-trip.json")
		if err := preset.Save(original, saved); err != nil {
			t.Fatalf("save %s: %v", path, err)
		}

		reloaded, err := preset.Load(saved)
		if err != nil {
			t.Fatalf("reload %s: %v", path, err)
		}

		assertSamplesIdentical(t, path+" after save/load", reference, renderPreset(t, reloaded))

		upgraded, err := preset.Upgrade(original)
		if err != nil {
			t.Fatalf("upgrade %s: %v", path, err)
		}

		assertSamplesIdentical(t, path+" after upgrade to v2", reference, renderPreset(t, upgraded))
	}
}

// TestRenderingIsIndependentOfPresetState guards the slice-aliasing trap that
// arrived with a variable mode count: transposing a note must not scale the
// preset the synthesizer was built from.
func TestRenderingIsIndependentOfPresetState(t *testing.T) {
	loaded, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		t.Fatalf("load default: %v", err)
	}

	synthesizer, err := NewSynthesizer(loaded, 44100)
	if err != nil {
		t.Fatalf("NewSynthesizer: %v", err)
	}

	first := synthesizer.RenderNote(loaded.Note, 100, 0.25)

	for _, note := range []int{48, 60, 84, 96} {
		_ = synthesizer.RenderNote(note, 100, 0.25)
	}

	assertSamplesIdentical(t, "after transposed renders", first, synthesizer.RenderNote(loaded.Note, 100, 0.25))
}

// TestSynthesizerHandlesVariableModeCounts exercises the runtime-configurable
// path end to end, including per-mode harmonic partials.
func TestSynthesizerHandlesVariableModeCounts(t *testing.T) {
	for _, modeCount := range []int{1, 2, 7, 16, 64} {
		modes := make([]model.ModeParams, modeCount)
		for i := range modes {
			modes[i] = model.ModeParams{
				Amplitude: 0.4 / float64(i+1),
				Frequency: 440 * float64(i+1),
				DecayMs:   120 / float64(i+1),
				Harmonics: []float64{1, 0.4, 0.2},
			}
		}

		candidate := &preset.Preset{
			Version: preset.VersionV2,
			Name:    "variable",
			Note:    69,
			Parameters: model.BarParams{
				InputMix:        0,
				FilterFrequency: 4000,
				BaseFrequency:   440,
				Modes:           modes,
			},
		}

		rendered := renderPreset(t, candidate)
		if len(rendered) == 0 {
			t.Fatalf("%d modes rendered nothing", modeCount)
		}

		peak := float32(0)
		for _, v := range rendered {
			if v > peak {
				peak = v
			}
		}

		if peak <= 0 {
			t.Fatalf("%d modes produced silence", modeCount)
		}
	}
}
