package preset_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/model"
)

// v1ModeCount mirrors the fixed mode count of the v1 schema.
const v1ModeCount = 4

// shippedPresets returns every preset file tracked in the repository.
func shippedPresets(t *testing.T) []string {
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

	if len(paths) == 0 {
		t.Fatal("no preset fixtures found")
	}

	return paths
}

func TestShippedPresetsLoadAsV1(t *testing.T) {
	for _, path := range shippedPresets(t) {
		loaded, err := preset.Load(path)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}

		if loaded.Version != preset.VersionV1 {
			t.Fatalf("%s: version = %q, want %q", path, loaded.Version, preset.VersionV1)
		}

		if len(loaded.Parameters.Modes) != v1ModeCount {
			t.Fatalf("%s: %d modes, want %d", path, len(loaded.Parameters.Modes), v1ModeCount)
		}

		if loaded.Parameters.Chebyshev.ResolvedStage() != model.ChebyshevStageExcitation {
			t.Fatalf("%s: stage = %q, want the v1 excitation placement",
				path, loaded.Parameters.Chebyshev.ResolvedStage())
		}
	}
}

func TestShippedPresetsRoundTripThroughSaveAndLoad(t *testing.T) {
	for _, path := range shippedPresets(t) {
		original, err := preset.Load(path)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}

		out := filepath.Join(t.TempDir(), "round-trip.json")
		if err := preset.Save(original, out); err != nil {
			t.Fatalf("save %s: %v", path, err)
		}

		reloaded, err := preset.Load(out)
		if err != nil {
			t.Fatalf("reload %s: %v", path, err)
		}

		assertPresetsEqual(t, path, original, reloaded)
	}
}

func TestUpgradePreservesParameters(t *testing.T) {
	for _, path := range shippedPresets(t) {
		original, err := preset.Load(path)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}

		upgraded, err := preset.Upgrade(original)
		if err != nil {
			t.Fatalf("upgrade %s: %v", path, err)
		}

		if upgraded.Version != preset.VersionV2 {
			t.Fatalf("%s: upgraded version = %q, want %q", path, upgraded.Version, preset.VersionV2)
		}

		// The upgrade fills in the stage the v1 loader implies; everything else
		// must be untouched.
		upgraded.Version = original.Version
		upgraded.Parameters.Chebyshev.Stage = original.Parameters.Chebyshev.Stage

		assertPresetsEqual(t, path, original, upgraded)

		// Upgrading must not alias the original's slices.
		roundTrip, err := preset.Upgrade(original)
		if err != nil {
			t.Fatalf("upgrade %s: %v", path, err)
		}

		roundTrip.Parameters.Modes[0].Frequency = -1
		if original.Parameters.Modes[0].Frequency == -1 {
			t.Fatalf("%s: upgrade aliased the original's modes", path)
		}
	}
}

func TestV1RejectsV2OnlyFields(t *testing.T) {
	base, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		t.Fatalf("load default: %v", err)
	}

	cases := map[string]func(p *preset.Preset){
		"per-mode harmonics": func(p *preset.Preset) {
			p.Parameters.Modes[0].Harmonics = []float64{1, 0.5}
		},
		"explicit chebyshev stage": func(p *preset.Preset) {
			p.Parameters.Chebyshev.Stage = model.ChebyshevStageOutput
		},
		"extra mode": func(p *preset.Preset) {
			p.Parameters.Modes = append(p.Parameters.Modes, p.Parameters.Modes[0])
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := base.Clone()
			mutate(candidate)

			if err := preset.Validate(candidate); err == nil {
				t.Fatal("expected a v1 schema error")
			}

			upgraded, err := preset.Upgrade(base)
			if err != nil {
				t.Fatalf("upgrade: %v", err)
			}

			mutate(upgraded)

			if err := preset.Validate(upgraded); err != nil {
				t.Fatalf("v2 should accept this: %v", err)
			}
		})
	}
}

func TestUnsupportedVersionIsRejected(t *testing.T) {
	data := []byte(`{"version":"3.0","name":"x","note":69,"parameters":{}}`)

	if _, err := preset.Decode(data, "test"); err == nil {
		t.Fatal("expected an unsupported-version error")
	} else if !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestV2AcceptsVariableModeCounts(t *testing.T) {
	for _, modeCount := range []int{1, 3, 4, 9, 64} {
		modes := make([]model.ModeParams, modeCount)
		for i := range modes {
			modes[i] = model.ModeParams{
				Amplitude: 0.5,
				Frequency: 440 * float64(i+1),
				DecayMs:   50,
				Harmonics: []float64{1, 0.5, 0.25},
			}
		}

		candidate := &preset.Preset{
			Version: preset.VersionV2,
			Name:    "variable",
			Note:    69,
			Parameters: model.BarParams{
				InputMix:        0.1,
				FilterFrequency: 1000,
				BaseFrequency:   440,
				Modes:           modes,
				Chebyshev: model.ChebyshevParams{
					Enabled:       true,
					Stage:         model.ChebyshevStageOutput,
					HarmonicGains: []float64{1, 0.5},
				},
			},
		}

		out := filepath.Join(t.TempDir(), "v2.json")
		if err := preset.Save(candidate, out); err != nil {
			t.Fatalf("%d modes: save: %v", modeCount, err)
		}

		reloaded, err := preset.Load(out)
		if err != nil {
			t.Fatalf("%d modes: load: %v", modeCount, err)
		}

		assertPresetsEqual(t, "variable", candidate, reloaded)
	}
}

func assertPresetsEqual(t *testing.T, label string, want, got *preset.Preset) {
	t.Helper()

	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("%s: marshal want: %v", label, err)
	}

	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("%s: marshal got: %v", label, err)
	}

	if string(wantJSON) != string(gotJSON) {
		t.Fatalf("%s mismatch:\n got %s\nwant %s", label, gotJSON, wantJSON)
	}
}

// TestV1RejectsV2FieldsPresentButEmpty covers what a value check cannot see: an
// explicit "stage": "" or "harmonics": [] decodes to the same zero value as an
// omitted field, so presence has to be read off the raw JSON.
func TestV1RejectsV2FieldsPresentButEmpty(t *testing.T) {
	base, err := os.ReadFile(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		t.Fatalf("read default: %v", err)
	}

	var document map[string]any
	if err := json.Unmarshal(base, &document); err != nil {
		t.Fatalf("decode default: %v", err)
	}

	params, ok := document["parameters"].(map[string]any)
	if !ok {
		t.Fatal("default preset has no parameters object")
	}

	cases := map[string]func(){
		"empty stage": func() {
			chebyshev, _ := params["chebyshev"].(map[string]any)
			chebyshev["stage"] = ""
		},
		"empty harmonics": func() {
			modes, _ := params["modes"].([]any)
			mode, _ := modes[0].(map[string]any)
			mode["harmonics"] = []any{}
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var candidate map[string]any
			if err := json.Unmarshal(base, &candidate); err != nil {
				t.Fatalf("decode default: %v", err)
			}

			params, _ = candidate["parameters"].(map[string]any)

			mutate()

			data, err := json.Marshal(candidate)
			if err != nil {
				t.Fatalf("encode candidate: %v", err)
			}

			if _, err := preset.Decode(data, "test"); err == nil {
				t.Fatal("expected a v1 schema error")
			}
		})
	}
}
