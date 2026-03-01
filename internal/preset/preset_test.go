package preset

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/model"
)

func TestLoadDefaultPreset(t *testing.T) {
	p, err := Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		t.Fatalf("expected default preset to load, got error: %v", err)
	}

	if p.Name == "" {
		t.Fatal("expected non-empty preset name")
	}
}

func TestLoadMinimalPreset(t *testing.T) {
	p, err := Load(filepath.FromSlash("../../testdata/presets/minimal.json"))
	if err != nil {
		t.Fatalf("expected minimal preset to load, got error: %v", err)
	}

	if p.Note != 69 {
		t.Fatalf("expected note 69, got %d", p.Note)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preset.json")

	want := validPreset()
	if err := Save(want, path); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if got.Name != want.Name || got.Note != want.Note || got.Version != want.Version {
		t.Fatalf("unexpected metadata round-trip: got %#v want %#v", got, want)
	}

	if len(got.Parameters.Chebyshev.HarmonicGains) != len(want.Parameters.Chebyshev.HarmonicGains) {
		t.Fatalf("unexpected chebyshev gain length: got %d want %d",
			len(got.Parameters.Chebyshev.HarmonicGains), len(want.Parameters.Chebyshev.HarmonicGains))
	}
}

func TestValidateRejectsBadPreset(t *testing.T) {
	p := validPreset()
	p.Note = 200

	err := Validate(p)
	if err == nil {
		t.Fatal("expected validation error")
	}

	if !strings.Contains(err.Error(), "note") {
		t.Fatalf("expected note validation error, got: %v", err)
	}
}

func TestJSONMarshalling(t *testing.T) {
	p := validPreset()

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded Preset
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if err := Validate(&decoded); err != nil {
		t.Fatalf("decoded preset should validate: %v", err)
	}
}

func validPreset() *Preset {
	return &Preset{
		Version: "1.0",
		Name:    "Unit Test Preset",
		Note:    69,
		Parameters: model.BarParams{
			InputMix:        0.472433640370972,
			FilterFrequency: 522.935295651445,
			BaseFrequency:   440.0,
			Modes: [model.NumModes]model.ModeParams{
				{Amplitude: 0.885860562324524, Frequency: 1756.64123535156, DecayMs: 188.223281860352},
				{Amplitude: 1.99459731578827, Frequency: 4768.10693359375, DecayMs: 1.60327112674713},
				{Amplitude: -0.464719623327255, Frequency: 38.241283416748, DecayMs: 5.55945539474487},
				{Amplitude: 0.363913357257843, Frequency: 32.6347961425781, DecayMs: 8.6815824508667},
			},
			Chebyshev: model.ChebyshevParams{
				Enabled:       true,
				HarmonicGains: []float64{1.0, 0.5, 0.3, 0.2},
			},
		},
	}
}
