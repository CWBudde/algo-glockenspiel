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

// TestShippedPresetsMatchTheirDeclaredSchema holds every tracked preset to the
// rules of the version it claims, rather than to one version's rules.
//
// It used to assert that every shipped preset was v1, which was true for as
// long as there was one of them. A v2 document is not a relaxation of v1: the
// point of the two versions is that a v1 file which quietly grew a variable
// mode count or an output-stage shaper is a bug, and that check has to survive
// the arrival of files that legitimately use both.
//
// The v4 arm is the sharpest of them, because v4's claim is checkable from one
// field: the version must be v4 exactly when the document carries a decay
// keytrack, in both directions. A build that stamped v4 onto a keytrack-free
// preset has already shipped once, and that file locks out a v3 reader -- the
// external VST3 module among them -- for a key it does not contain.
func TestShippedPresetsMatchTheirDeclaredSchema(t *testing.T) {
	for _, path := range shippedPresets(t) {
		loaded, err := preset.Load(path)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}

		switch loaded.Version {
		case preset.VersionV1:
			if len(loaded.Parameters.Modes) != v1ModeCount {
				t.Fatalf("%s: %d modes, want %d", path, len(loaded.Parameters.Modes), v1ModeCount)
			}

			if loaded.Parameters.Chebyshev.ResolvedStage() != model.ChebyshevStageExcitation {
				t.Fatalf("%s: stage = %q, want the v1 excitation placement",
					path, loaded.Parameters.Chebyshev.ResolvedStage())
			}
		case preset.VersionV2, preset.VersionV3:
			if len(loaded.Parameters.Modes) == 0 {
				t.Fatalf("%s: no modes", path)
			}

			// v4 is the first version whose claim is checkable from a single
			// field, so check it rather than only allowing it: a document
			// stamped v4 without a keytrack locks out every v3 reader for a
			// key it does not carry, which is a bug a build has shipped once
			// already.
			if loaded.Parameters.DecayKeytrack != nil {
				t.Fatalf("%s: version %q carries a decay keytrack, which is v4",
					path, loaded.Version)
			}
		case preset.VersionV4:
			if len(loaded.Parameters.Modes) == 0 {
				t.Fatalf("%s: no modes", path)
			}

			if loaded.Parameters.DecayKeytrack == nil {
				t.Fatalf("%s: declares v4 but carries no decay keytrack", path)
			}
		default:
			t.Fatalf("%s: version = %q, want %q, %q, %q or %q",
				path, loaded.Version,
				preset.VersionV1, preset.VersionV2, preset.VersionV3, preset.VersionV4)
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

		want := preset.MinimumVersion(&original.Parameters)
		if preset.OlderThan(want, original.Version) {
			want = original.Version
		}

		if upgraded.Version != want {
			t.Fatalf("%s: upgraded version = %q, want %q", path, upgraded.Version, want)
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
		"output gain": func(p *preset.Preset) {
			p.Parameters.OutputGainDB = 6
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

			// Stamped the way a writer stamps: for the fields the document
			// ended up holding. The upgrade alone cannot know -- the mutation
			// happens after it -- and that is the whole point of deriving the
			// version from the parameters rather than from a constant.
			upgraded.Version = preset.MinimumVersion(&upgraded.Parameters)

			if err := preset.Validate(upgraded); err != nil {
				t.Fatalf("a version carrying this field should accept it: %v", err)
			}
		})
	}
}

// TestV2RejectsTheOutputGain pins the reason output_gain_db started a version
// rather than extending v2: a v2 reader that met the field would ignore it and
// render at unity, so a document carrying it must not call itself v2.
func TestV2RejectsTheOutputGain(t *testing.T) {
	base, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		t.Fatalf("load default: %v", err)
	}

	upgraded, err := preset.Upgrade(base)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	candidate := upgraded.Clone()
	candidate.Version = preset.VersionV2
	candidate.Parameters.OutputGainDB = 6

	if err := preset.Validate(candidate); err == nil {
		t.Fatal("expected a v2 schema error for output_gain_db")
	} else if !strings.Contains(err.Error(), preset.VersionV3) {
		t.Fatalf("the error should name version %s: %v", preset.VersionV3, err)
	}

	// An explicit zero is indistinguishable from an omitted field once decoded,
	// so the presence check has to read the raw JSON. Decode is the only place
	// that can catch it.
	data := []byte(`{"version":"2.0","name":"x","note":69,"parameters":{` +
		`"input_mix":0.1,"filter_frequency":1000,"base_frequency":440,` +
		`"output_gain_db":0,` +
		`"modes":[{"amplitude":0.5,"frequency":440,"decay_ms":50}]}}`)

	if _, err := preset.Decode(data, "test"); err == nil {
		t.Fatal("expected an explicit zero output_gain_db to be rejected in a v2 document")
	} else if !strings.Contains(err.Error(), "output_gain_db") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUnsupportedVersionIsRejected(t *testing.T) {
	data := []byte(`{"version":"9.0","name":"x","note":69,"parameters":{}}`)

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
		"zero output gain": func() {
			params["output_gain_db"] = 0
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

// TestV3RejectsTheKeytrack mirrors TestV2RejectsTheOutputGain, with one part
// that test does not have.
//
// The trap value here is 1.0 rather than zero. One is the exponent a v3 reader
// applies implicitly, so it is precisely the value an author will believe is
// harmless to write into an older document -- and it is the one that must still
// be refused, because a reader that accepts it has accepted a document it
// cannot be trusted to have understood.
func TestV3RejectsTheKeytrack(t *testing.T) {
	base, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		t.Fatalf("load default: %v", err)
	}

	upgraded, err := preset.Upgrade(base)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	keytrack := 0.5

	candidate := upgraded.Clone()
	candidate.Version = preset.VersionV3
	candidate.Parameters.DecayKeytrack = &keytrack

	if err := preset.Validate(candidate); err == nil {
		t.Fatal("expected a v3 schema error for decay_keytrack")
	} else if !strings.Contains(err.Error(), preset.VersionV4) {
		t.Fatalf("the error should name version %s: %v", preset.VersionV4, err)
	}

	for _, written := range []string{"1.0", "null"} {
		data := []byte(`{"version":"3.0","name":"x","note":69,"parameters":{` +
			`"input_mix":0.1,"filter_frequency":1000,"base_frequency":440,` +
			`"decay_keytrack":` + written + `,` +
			`"modes":[{"amplitude":0.5,"frequency":440,"decay_ms":50}]}}`)

		if _, err := preset.Decode(data, "test"); err == nil {
			t.Errorf("a v3 document carrying decay_keytrack %s was accepted", written)
		} else if !strings.Contains(err.Error(), "decay_keytrack") {
			t.Errorf("decay_keytrack %s: unexpected error: %v", written, err)
		}
	}
}

// TestV4StillAcceptsTheOutputGain is the regression the version ladder needed
// and did not have.
//
// The output_gain_db gate read "version is not exactly v3", which was right
// while v3 was the newest version and became wrong the moment v4 existed --
// every calibrated preset a fit writes carries an output gain and is written in
// the current version, so all of them would have been refused for holding a
// field an older version introduced.
func TestV4StillAcceptsTheOutputGain(t *testing.T) {
	data := []byte(`{"version":"4.0","name":"x","note":69,"parameters":{` +
		`"input_mix":0.1,"filter_frequency":1000,"base_frequency":440,` +
		`"output_gain_db":-3.5,"decay_keytrack":0.75,` +
		`"modes":[{"amplitude":0.5,"frequency":440,"decay_ms":50}]}}`)

	loaded, err := preset.Decode(data, "test")
	if err != nil {
		t.Fatalf("a v4 document carrying both new fields was refused: %v", err)
	}

	if loaded.Parameters.OutputGainDB != -3.5 {
		t.Errorf("output_gain_db read back as %v", loaded.Parameters.OutputGainDB)
	}

	if loaded.Parameters.ResolvedDecayKeytrack() != 0.75 {
		t.Errorf("decay_keytrack read back as %v", loaded.Parameters.ResolvedDecayKeytrack())
	}
}

// TestUpgradeToV4LeavesTheSoundAlone is why v4 can exist without touching a
// single shipped file: none of them carries a keytrack, so none of them is a v4
// document, and upgrading one neither restamps it nor changes how it renders.
//
// The version a file claims says what a reader must understand to render it
// correctly. A preset with no keytrack renders identically under a v3 reader,
// so claiming v4 would lock that reader out and buy nothing.
func TestUpgradeToV4LeavesTheSoundAlone(t *testing.T) {
	for _, path := range []string{
		"../../assets/presets/default.json",
		"../../assets/presets/recorded-bar.json",
	} {
		base, err := preset.Load(filepath.FromSlash(path))
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}

		upgraded, err := preset.Upgrade(base)
		if err != nil {
			t.Fatalf("upgrade %s: %v", path, err)
		}

		if upgraded.Version == preset.VersionV4 {
			t.Errorf("%s upgraded to %q; with no keytrack it is not a v4 document",
				path, upgraded.Version)
		}

		if want := preset.MinimumVersion(&upgraded.Parameters); upgraded.Version != want {
			t.Errorf("%s upgraded to %q, want the version its fields need, %q",
				path, upgraded.Version, want)
		}

		if upgraded.Parameters.DecayKeytrack != nil {
			t.Errorf("%s gained a keytrack of %v on upgrade; it should stay absent",
				path, *upgraded.Parameters.DecayKeytrack)
		}

		if got := upgraded.Parameters.ResolvedDecayKeytrack(); got != 1 {
			t.Errorf("%s resolves its absent keytrack to %v, want 1", path, got)
		}
	}
}

// TestTheVersionIsEarnedByTheFieldsTheDocumentUses pins the rule the ladder was
// written for, from the writing side rather than the reading side.
//
// Every other version test asks what a reader must reject. This one asks what a
// writer may claim, which is the half that went wrong in practice: a fit that
// stamps CurrentVersion on everything it writes hands a v3-only reader a
// document it refuses, over a field the document does not contain and would not
// have used. The keytrack is the second field to reach this ladder, and the
// first one the repo shipped before deciding whether it had earned its version.
func TestTheVersionIsEarnedByTheFieldsTheDocumentUses(t *testing.T) {
	keytrack := 1.0

	for _, testCase := range []struct {
		name  string
		apply func(*model.BarParams)
		want  string
	}{
		{name: "bare parameters", apply: func(*model.BarParams) {}, want: preset.VersionV2},
		{
			name:  "an output gain",
			apply: func(params *model.BarParams) { params.OutputGainDB = -3 },
			want:  preset.VersionV3,
		},
		{
			// 1.0 is the neutral value, and it still earns v4: the document
			// carries the key, so a v3 reader would refuse it, and a writer that
			// pretended otherwise would produce a file no reader agrees about.
			name:  "a neutral keytrack",
			apply: func(params *model.BarParams) { params.DecayKeytrack = &keytrack },
			want:  preset.VersionV4,
		},
		{
			name: "both",
			apply: func(params *model.BarParams) {
				params.OutputGainDB = -3
				params.DecayKeytrack = &keytrack
			},
			want: preset.VersionV4,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			params := model.BarParams{
				InputMix:        0.1,
				FilterFrequency: 1000,
				BaseFrequency:   440,
				Modes: []model.ModeParams{
					{Amplitude: 0.5, Frequency: 440, DecayMs: 50},
				},
			}
			testCase.apply(&params)

			if got := preset.MinimumVersion(&params); got != testCase.want {
				t.Errorf("MinimumVersion = %q, want %q", got, testCase.want)
			}

			// Whatever the minimum is, a document already claiming a newer
			// version keeps it: downgrading a file is not an upgrade's business,
			// and a reader that accepted it yesterday must accept it today.
			candidate := &preset.Preset{
				Version:    preset.VersionV4,
				Name:       "ladder",
				Note:       69,
				Parameters: params,
			}

			upgraded, err := preset.Upgrade(candidate)
			if err != nil {
				t.Fatalf("upgrade: %v", err)
			}

			if upgraded.Version != preset.VersionV4 {
				t.Errorf("upgrading a v4 document produced %q", upgraded.Version)
			}
		})
	}
}

// TestProvenanceCloneCopiesTheReferenceSlice. Clone's contract is a deep copy,
// and `clone := *p` gives a slice its header rather than its contents -- so a
// joint fit's per-note provenance was shared between a preset and its clone,
// silently, in exactly the callers that clone for isolation.
func TestProvenanceCloneCopiesTheReferenceSlice(t *testing.T) {
	original := &preset.Provenance{
		References: []preset.NoteReferenceProvenance{
			{Note: 84, Score: 0.5},
			{Note: 85, Score: 0.6},
		},
	}

	clone := original.Clone()
	clone.References[0].Score = 99

	if original.References[0].Score != 0.5 {
		t.Fatalf("editing the clone moved the original to %v, so the slice is shared",
			original.References[0].Score)
	}
}
