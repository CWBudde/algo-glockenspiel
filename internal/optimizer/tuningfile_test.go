package optimizer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/mayfly"
)

func TestDecodeMayflyTuning(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
		check   func(t *testing.T, tuning *optimizer.MayflyTuning)
	}{
		{
			name:    "an empty document sets nothing",
			content: `{}`,
			check: func(t *testing.T, tuning *optimizer.MayflyTuning) {
				t.Helper()

				if tuning.NPop != nil || tuning.Convergence != nil || tuning.Schedule != nil {
					t.Fatalf("expected an empty document to leave every field nil: %#v", tuning)
				}
			},
		},
		{
			name:    "sets only the given keys",
			content: `{"npop": 40, "selection": "tournament"}`,
			check: func(t *testing.T, tuning *optimizer.MayflyTuning) {
				t.Helper()

				if tuning.NPop == nil || *tuning.NPop != 40 {
					t.Fatalf("unexpected npop: %#v", tuning.NPop)
				}

				if tuning.Selection == nil || *tuning.Selection != "tournament" {
					t.Fatalf("unexpected selection: %#v", tuning.Selection)
				}

				if tuning.NPopF != nil {
					t.Fatalf("expected an omitted key to stay nil: %#v", tuning.NPopF)
				}
			},
		},
		{
			name:    "accepts the self-contained document fields",
			content: `{"variant": "desma", "preset": "multimodal"}`,
			check: func(t *testing.T, tuning *optimizer.MayflyTuning) {
				t.Helper()

				if tuning.Variant == nil || *tuning.Variant != "desma" {
					t.Fatalf("unexpected variant: %#v", tuning.Variant)
				}

				if tuning.Preset == nil || *tuning.Preset != "multimodal" {
					t.Fatalf("unexpected preset: %#v", tuning.Preset)
				}
			},
		},
		{
			name:    "rejects unknown key",
			content: `{"nopp": 40}`,
			wantErr: "decode tuning",
		},
		{
			name:    "rejects a second document after the first",
			content: `{"npop": 40}{"npop": 50}`,
			wantErr: "unexpected content after the tuning object",
		},
		{
			name:    "rejects junk after the object",
			content: `{"npop": 40} nonsense`,
			wantErr: "unexpected content after the tuning object",
		},
		{
			name:    "rejects malformed json",
			content: `{"npop": 40`,
			wantErr: "decode tuning",
		},

		{name: "npop below two", content: `{"npop": 1}`, wantErr: "npop must be at least 2"},
		{name: "npopf below two", content: `{"npopf": 1}`, wantErr: "npopf must be at least 2"},
		{name: "g above one", content: `{"g": 1.5}`, wantErr: "g must be in [0, 1]"},
		{name: "g below zero", content: `{"g": -0.5}`, wantErr: "g must be in [0, 1]"},
		{name: "g_damp at zero", content: `{"g_damp": 0}`, wantErr: "g_damp must be greater than 0"},
		{name: "a1 negative", content: `{"a1": -1}`, wantErr: "a1 must be at least 0"},
		{name: "a2 negative", content: `{"a2": -1}`, wantErr: "a2 must be at least 0"},
		{name: "a3 negative", content: `{"a3": -1}`, wantErr: "a3 must be at least 0"},
		{name: "beta at zero", content: `{"beta": 0}`, wantErr: "beta must be greater than 0"},
		{name: "mu above one", content: `{"mu": 2}`, wantErr: "mu must be in [0, 1]"},
		{name: "nc_ratio negative", content: `{"nc_ratio": -0.5}`, wantErr: "nc_ratio must be at least 0"},
		{name: "nm negative", content: `{"nm": -1}`, wantErr: "nm must be at least 0"},
		{
			name: "tournament_size negative", content: `{"tournament_size": -1}`,
			wantErr: "tournament_size must be at least 0",
		},
		{
			name: "selection not a strategy", content: `{"selection": "roulette"}`,
			wantErr: `selection must be one of "tournament", "rank"`,
		},
		{name: "search_range negative", content: `{"search_range": -1}`, wantErr: "search_range must be at least 0"},
		{name: "elite_count negative", content: `{"elite_count": -1}`, wantErr: "elite_count must be at least 0"},
		{
			name: "enlarge_factor at zero", content: `{"enlarge_factor": 0}`,
			wantErr: "enlarge_factor must be greater than 0",
		},
		{
			name: "reduction_factor at zero", content: `{"reduction_factor": 0}`,
			wantErr: "reduction_factor must be greater than 0",
		},
		{
			name: "orthogonal_factor above one", content: `{"orthogonal_factor": 1.5}`,
			wantErr: "orthogonal_factor must be in [0, 1]",
		},
		{name: "chaos_factor above one", content: `{"chaos_factor": 2}`, wantErr: "chaos_factor must be in [0, 1]"},
		{name: "levy_alpha at zero", content: `{"levy_alpha": 0}`, wantErr: "levy_alpha must be in (0, 2]"},
		{name: "levy_alpha above two", content: `{"levy_alpha": 2.5}`, wantErr: "levy_alpha must be in (0, 2]"},
		{name: "levy_beta at zero", content: `{"levy_beta": 0}`, wantErr: "levy_beta must be greater than 0"},
		{
			name: "opposition_rate above one", content: `{"opposition_rate": 1.2}`,
			wantErr: "opposition_rate must be in [0, 1]",
		},
		{
			name: "elite_opposition_count negative", content: `{"elite_opposition_count": -1}`,
			wantErr: "elite_opposition_count must be at least 0",
		},
		{
			name: "initial_temperature at zero", content: `{"initial_temperature": 0}`,
			wantErr: "initial_temperature must be greater than 0",
		},
		{name: "cooling_rate at one", content: `{"cooling_rate": 1}`, wantErr: "cooling_rate must be in (0, 1)"},
		{name: "cooling_rate at zero", content: `{"cooling_rate": 0}`, wantErr: "cooling_rate must be in (0, 1)"},
		{
			name: "cauchy_mutation_rate above one", content: `{"cauchy_mutation_rate": 1.1}`,
			wantErr: "cauchy_mutation_rate must be in [0, 1]",
		},
		{
			name: "cooling_schedule not a schedule", content: `{"cooling_schedule": "geometric"}`,
			wantErr: `cooling_schedule must be one of "exponential", "linear", "logarithmic"`,
		},
		{
			name: "median_weight above one", content: `{"median_weight": 1.5}`,
			wantErr: "median_weight must be in [0, 1]",
		},
		{
			name: "gravity_type not a schedule", content: `{"gravity_type": "quadratic"}`,
			wantErr: `gravity_type must be one of "linear", "exponential", "sigmoid"`,
		},
		{
			name: "aquila_weight above one", content: `{"aquila_weight": 1.5}`,
			wantErr: "aquila_weight must be in [0, 1]",
		},
		{
			name: "opposition_probability negative", content: `{"opposition_probability": -0.1}`,
			wantErr: "opposition_probability must be in [0, 1]",
		},
		{name: "archive_size negative", content: `{"archive_size": -1}`, wantErr: "archive_size must be at least 0"},
		{
			name: "strategy_switch negative", content: `{"strategy_switch": -1}`,
			wantErr: "strategy_switch must be at least 0",
		},
		{
			name: "min_improvement negative", content: `{"convergence": {"min_improvement": -1}}`,
			wantErr: "min_improvement must be at least 0",
		},
		{
			name: "stagnation_iterations negative", content: `{"convergence": {"stagnation_iterations": -1}}`,
			wantErr: "stagnation_iterations must be at least 0",
		},
		{
			name: "min_iterations negative", content: `{"convergence": {"min_iterations": -1}}`,
			wantErr: "min_iterations must be at least 0",
		},
		{name: "epochs below one", content: `{"schedule": {"epochs": 0}}`, wantErr: "epochs must be at least 1"},
		{name: "restarts negative", content: `{"schedule": {"restarts": -1}}`, wantErr: "restarts must be at least 0"},
		{
			name: "classify_evals negative", content: `{"schedule": {"classify_evals": -1}}`,
			wantErr: "classify_evals must be at least 0",
		},

		{name: "rejects an infinite value", content: `{"dance": 1e999}`, wantErr: "decode tuning"},
		{
			name: "rejects an unbounded but infinite target cost", content: `{"convergence": {"target_cost": 1e999}}`,
			wantErr: "decode tuning",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tuning, err := optimizer.DecodeMayflyTuning([]byte(tc.content), "the test document")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("DecodeMayflyTuning failed: %v", err)
			}

			tc.check(t, tuning)
		})
	}
}

// TestDecodeMayflyTuningNonFinite covers the values encoding/json will hand us
// as a float64 rather than rejecting outright. A literal NaN is not valid JSON,
// so the only way a caller produces one is through a float64 that already is
// one, which is what the marshalled fixtures below stand in for.
func TestDecodeMayflyTuningNonFinite(t *testing.T) {
	for _, content := range []string{`{"dance": 1e999}`, `{"dance": -1e999}`, `{"golden_factor": 1e999}`} {
		t.Run(content, func(t *testing.T) {
			if _, err := optimizer.DecodeMayflyTuning([]byte(content), "the test document"); err == nil {
				t.Fatalf("expected %s to be rejected", content)
			}
		})
	}
}

func TestDecodeMayflyTuningNCSemantics(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantNil bool
		want    int
	}{
		{name: "absent leaves the factory value alone", content: `{}`, wantNil: true},
		{name: "minus one selects NCAuto", content: `{"nc": -1}`, want: mayfly.NCAuto},
		{name: "zero disables crossover", content: `{"nc": 0}`, want: 0},
		{name: "a positive count is literal", content: `{"nc": 12}`, want: 12},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tuning, err := optimizer.DecodeMayflyTuning([]byte(tc.content), "the test document")
			if err != nil {
				t.Fatalf("DecodeMayflyTuning failed: %v", err)
			}

			if tc.wantNil {
				if tuning.NC != nil {
					t.Fatalf("expected an absent nc to stay nil, got %d", *tuning.NC)
				}

				return
			}

			if tuning.NC == nil {
				t.Fatal("expected nc to be set")
			}

			if *tuning.NC != tc.want {
				t.Fatalf("expected nc %d, got %d", tc.want, *tuning.NC)
			}
		})
	}

	_, err := optimizer.DecodeMayflyTuning([]byte(`{"nc": -2}`), "the test document")
	if err == nil || !strings.Contains(err.Error(), "NCAuto") {
		t.Fatalf("expected nc -2 to be rejected with a message naming NCAuto, got %v", err)
	}
}

func TestMayflyTuningKeysMatchTheDocument(t *testing.T) {
	// The table is what the decoder validates against and what the CLI help and
	// the generated TypeScript table are built from. If it drifts from the
	// struct, a knob decodes without ever being range-checked, or the help text
	// advertises a key the parser rejects.
	documented := make(map[string]bool, len(optimizer.MayflyTuningKeys))
	for _, key := range optimizer.MayflyTuningKeys {
		if documented[key] {
			t.Fatalf("duplicate key %q in MayflyTuningFields", key)
		}

		documented[key] = true
	}

	if len(documented) != len(optimizer.MayflyTuningFields()) {
		t.Fatal("MayflyTuningKeys and MayflyTuningFields disagree on length")
	}

	// variant and preset are struct fields but not tunable knobs: they name the
	// dialect and the preset a document was written for, so a self-contained
	// document needs no matching command line. They set nothing on the config,
	// carry no range, and so are deliberately absent from the table.
	skipped := map[string]bool{"variant": true, "preset": true}

	found := map[string]bool{}
	collectMayflyTuningKeys(t, reflect.TypeOf(optimizer.MayflyTuning{}), skipped, found)

	for key := range found {
		if !documented[key] {
			t.Errorf("struct field %q has no entry in MayflyTuningFields", key)
		}
	}

	for key := range documented {
		if !found[key] {
			t.Errorf("MayflyTuningFields documents %q, which no struct field spells", key)
		}
	}
}

// collectMayflyTuningKeys gathers the json tag of every knob, descending into
// the nested blocks rather than counting their container keys: "convergence"
// and "schedule" are grouping, not knobs.
func collectMayflyTuningKeys(t *testing.T, typ reflect.Type, skipped, found map[string]bool) {
	t.Helper()

	for index := range typ.NumField() {
		field := typ.Field(index)

		key, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if key == "" || key == "-" || skipped[key] {
			continue
		}

		nested := field.Type
		if nested.Kind() == reflect.Pointer {
			nested = nested.Elem()
		}

		if nested.Kind() == reflect.Struct {
			collectMayflyTuningKeys(t, nested, skipped, found)

			continue
		}

		found[key] = true
	}
}

func TestMayflyTuningRoundTrip(t *testing.T) {
	original := fullyPopulatedTuning()

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal tuning: %v", err)
	}

	decoded, err := optimizer.DecodeMayflyTuning(data, "the test document")
	if err != nil {
		t.Fatalf("DecodeMayflyTuning failed: %v", err)
	}

	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("round trip changed the document:\nbefore %#v\nafter  %#v", original, decoded)
	}
}

func TestMayflyTuningApply(t *testing.T) {
	t.Run("a nil receiver is a no-op", func(t *testing.T) {
		var tuning *optimizer.MayflyTuning

		cfg := mayfly.NewDefaultConfig()
		before := *cfg

		if err := tuning.Apply(cfg, "ma"); err != nil {
			t.Fatalf("Apply on a nil receiver failed: %v", err)
		}

		if !reflect.DeepEqual(before, *cfg) {
			t.Fatal("expected a nil tuning to leave the config untouched")
		}
	})

	t.Run("an empty document is a no-op", func(t *testing.T) {
		cfg := mayfly.NewDefaultConfig()
		before := *cfg

		if err := (&optimizer.MayflyTuning{}).Apply(cfg, "ma"); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if !reflect.DeepEqual(before, *cfg) {
			t.Fatal("expected an empty tuning to leave the config untouched")
		}
	})

	t.Run("every shared knob lands on the config", func(t *testing.T) {
		tuning := sharedTuning()

		cfg := mayfly.NewDefaultConfig()
		if err := tuning.Apply(cfg, "ma"); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		checks := []struct {
			key  string
			got  any
			want any
		}{
			{"npop", cfg.NPop, 40},
			{"npopf", cfg.NPopF, 42},
			{"g", cfg.G, 0.5},
			{"g_damp", cfg.GDamp, 0.9},
			{"a1", cfg.A1, 1.1},
			{"a2", cfg.A2, 1.2},
			{"a3", cfg.A3, 1.3},
			{"beta", cfg.Beta, 2.5},
			{"dance", cfg.Dance, 4.0},
			{"dance_damp", cfg.DanceDamp, 0.7},
			{"fl", cfg.FL, 1.4},
			{"fl_damp", cfg.FLDamp, 0.98},
			{"nc", cfg.NC, 12},
			{"nc_ratio", cfg.NCRatio, 0.8},
			{"nm", cfg.NM, 3},
			{"mu", cfg.Mu, 0.02},
			{"crossover_gamma", cfg.CrossoverGamma, 0.6},
			{"selection", cfg.Selection, mayfly.SelectionTournament},
			{"tournament_size", cfg.TournamentSize, 5},
			{"vel_max", cfg.VelMax, 3.0},
			{"vel_min", cfg.VelMin, -3.0},
		}

		for _, check := range checks {
			if check.got != check.want {
				t.Errorf("%s: expected %v, got %v", check.key, check.want, check.got)
			}
		}
	})

	t.Run("every variant knob lands on the config", func(t *testing.T) {
		tests := []struct {
			variant string
			content string
			check   func(t *testing.T, cfg *mayfly.Config)
		}{
			{
				variant: "desma",
				content: `{"elite_count": 7, "search_range": 2.5, "enlarge_factor": 1.2, "reduction_factor": 0.8}`,
				check: func(t *testing.T, cfg *mayfly.Config) {
					t.Helper()

					if cfg.EliteCount != 7 || cfg.SearchRange != 2.5 ||
						cfg.EnlargeFactor != 1.2 || cfg.ReductionFactor != 0.8 {
						t.Fatalf("unexpected desma config: %+v", cfg)
					}
				},
			},
			{
				variant: "olce",
				content: `{"orthogonal_factor": 0.4, "chaos_factor": 0.2}`,
				check: func(t *testing.T, cfg *mayfly.Config) {
					t.Helper()

					if cfg.OrthogonalFactor != 0.4 || cfg.ChaosFactor != 0.2 {
						t.Fatalf("unexpected olce config: %+v", cfg)
					}
				},
			},
			{
				variant: "eobbma",
				content: `{"levy_alpha": 1.4, "levy_beta": 1.1, "opposition_rate": 0.25, "elite_opposition_count": 4}`,
				check: func(t *testing.T, cfg *mayfly.Config) {
					t.Helper()

					if cfg.LevyAlpha != 1.4 || cfg.LevyBeta != 1.1 ||
						cfg.OppositionRate != 0.25 || cfg.EliteOppositionCount != 4 {
						t.Fatalf("unexpected eobbma config: %+v", cfg)
					}
				},
			},
			{
				variant: "gsasma",
				content: `{"initial_temperature": 50, "cooling_rate": 0.9, "cooling_schedule": "linear",` +
					`"cauchy_mutation_rate": 0.4, "golden_factor": 1.5, "apply_obl_to_global_best": true}`,
				check: func(t *testing.T, cfg *mayfly.Config) {
					t.Helper()

					if cfg.InitialTemperature != 50 || cfg.CoolingRate != 0.9 ||
						cfg.CoolingSchedule != mayfly.CoolingLinear || cfg.CauchyMutationRate != 0.4 ||
						cfg.GoldenFactor != 1.5 || !cfg.ApplyOBLToGlobalBest {
						t.Fatalf("unexpected gsasma config: %+v", cfg)
					}
				},
			},
			{
				variant: "mpma",
				content: `{"median_weight": 0.6, "gravity_type": "sigmoid", "use_weighted_median": true}`,
				check: func(t *testing.T, cfg *mayfly.Config) {
					t.Helper()

					if cfg.MedianWeight != 0.6 || cfg.GravityType != mayfly.GravitySigmoid ||
						!cfg.UseWeightedMedian {
						t.Fatalf("unexpected mpma config: %+v", cfg)
					}
				},
			},
			{
				variant: "aoblmoa",
				content: `{"aquila_weight": 0.9, "opposition_probability": 0.35, "archive_size": 64,` +
					`"strategy_switch": 500}`,
				check: func(t *testing.T, cfg *mayfly.Config) {
					t.Helper()

					//nolint:staticcheck // SA1019: the knob is deprecated upstream and still carried; see applyTuning.
					if cfg.AquilaWeight != 0.9 || cfg.OppositionProbability != 0.35 ||
						cfg.ArchiveSize != 64 || cfg.StrategySwitch != 500 {
						t.Fatalf("unexpected aoblmoa config: %+v", cfg)
					}
				},
			},
		}

		for _, tc := range tests {
			t.Run(tc.variant, func(t *testing.T) {
				tuning, err := optimizer.DecodeMayflyTuning([]byte(tc.content), "the test document")
				if err != nil {
					t.Fatalf("DecodeMayflyTuning failed: %v", err)
				}

				cfg := mayfly.NewDefaultConfig()
				if err := tuning.Apply(cfg, tc.variant); err != nil {
					t.Fatalf("Apply failed: %v", err)
				}

				tc.check(t, cfg)
			})
		}
	})

	t.Run("a full convergence block lands on the config", func(t *testing.T) {
		content := `{"convergence": {"target_cost": 0.0, "min_improvement": 1e-6,` +
			`"stagnation_iterations": 50, "min_iterations": 10}}`

		tuning, err := optimizer.DecodeMayflyTuning([]byte(content), "the test document")
		if err != nil {
			t.Fatalf("DecodeMayflyTuning failed: %v", err)
		}

		cfg := mayfly.NewDefaultConfig()
		if err := tuning.Apply(cfg, "ma"); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if cfg.Convergence == nil {
			t.Fatal("expected a convergence block to be created")
		}

		if cfg.Convergence.TargetCost == nil || *cfg.Convergence.TargetCost != 0 {
			t.Fatalf("expected a target cost of zero, got %#v", cfg.Convergence.TargetCost)
		}

		if cfg.Convergence.MinImprovement != 1e-6 || cfg.Convergence.StagnationIterations != 50 ||
			cfg.Convergence.MinIterations != 10 {
			t.Fatalf("unexpected convergence config: %+v", cfg.Convergence)
		}
	})

	t.Run("a convergence block without a target leaves the target nil", func(t *testing.T) {
		tuning, err := optimizer.DecodeMayflyTuning(
			[]byte(`{"convergence": {"stagnation_iterations": 25}}`), "the test document",
		)
		if err != nil {
			t.Fatalf("DecodeMayflyTuning failed: %v", err)
		}

		cfg := mayfly.NewDefaultConfig()
		if err := tuning.Apply(cfg, "ma"); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if cfg.Convergence == nil {
			t.Fatal("expected a convergence block to be created")
		}

		if cfg.Convergence.TargetCost != nil {
			t.Fatalf("expected an omitted target cost to stay nil, got %v", *cfg.Convergence.TargetCost)
		}

		if cfg.Convergence.StagnationIterations != 25 {
			t.Fatalf("unexpected stagnation window: %d", cfg.Convergence.StagnationIterations)
		}
	})

	t.Run("an absent convergence block creates nothing", func(t *testing.T) {
		cfg := mayfly.NewDefaultConfig()
		if err := (&optimizer.MayflyTuning{}).Apply(cfg, "ma"); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if cfg.Convergence != nil {
			t.Fatalf("expected no convergence block, got %+v", cfg.Convergence)
		}
	})

	t.Run("the schedule is wrapper-owned and stays off the config", func(t *testing.T) {
		tuning, err := optimizer.DecodeMayflyTuning(
			[]byte(`{"schedule": {"epochs": 3, "restarts": 2, "classify_evals": 500}}`), "the test document",
		)
		if err != nil {
			t.Fatalf("DecodeMayflyTuning failed: %v", err)
		}

		cfg := mayfly.NewDefaultConfig()
		before := *cfg

		if err := tuning.Apply(cfg, "ma"); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if !reflect.DeepEqual(before, *cfg) {
			t.Fatal("expected the schedule block to leave the mayfly config untouched")
		}

		if tuning.Schedule == nil || *tuning.Schedule.Epochs != 3 ||
			*tuning.Schedule.Restarts != 2 || *tuning.Schedule.ClassifyEvals != 500 {
			t.Fatalf("expected the caller to read the schedule off the struct: %#v", tuning.Schedule)
		}
	})
}

func TestMayflyTuningRejectsForeignVariantKnobs(t *testing.T) {
	tuning, err := optimizer.DecodeMayflyTuning([]byte(`{"elite_count": 3}`), "the test document")
	if err != nil {
		t.Fatalf("DecodeMayflyTuning failed: %v", err)
	}

	err = tuning.Apply(mayfly.NewDefaultConfig(), "olce")
	if err == nil {
		t.Fatal("expected a desma knob under olce to be rejected")
	}

	for _, want := range []string{"elite_count", "desma", "olce"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to name %q, got %v", want, err)
		}
	}
}

func TestLoadMayflyTuning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tuning.json")
	if err := os.WriteFile(path, []byte(`{"npop": 40}`), 0o600); err != nil {
		t.Fatalf("write tuning fixture: %v", err)
	}

	tuning, err := optimizer.LoadMayflyTuning(path)
	if err != nil {
		t.Fatalf("LoadMayflyTuning failed: %v", err)
	}

	if tuning.NPop == nil || *tuning.NPop != 40 {
		t.Fatalf("unexpected npop: %#v", tuning.NPop)
	}
}

func TestLoadMayflyTuningMissingFile(t *testing.T) {
	if _, err := optimizer.LoadMayflyTuning(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("expected missing tuning file to fail")
	}
}

// sharedTuning sets every shared knob to a value distinct from the factory
// default, so a knob that silently fails to be written shows up as the default.
func sharedTuning() *optimizer.MayflyTuning {
	return &optimizer.MayflyTuning{
		NPop:           tuningInt(40),
		NPopF:          tuningInt(42),
		G:              tuningFloat(0.5),
		GDamp:          tuningFloat(0.9),
		A1:             tuningFloat(1.1),
		A2:             tuningFloat(1.2),
		A3:             tuningFloat(1.3),
		Beta:           tuningFloat(2.5),
		Dance:          tuningFloat(4.0),
		DanceDamp:      tuningFloat(0.7),
		FL:             tuningFloat(1.4),
		FLDamp:         tuningFloat(0.98),
		NC:             tuningInt(12),
		NCRatio:        tuningFloat(0.8),
		NM:             tuningInt(3),
		Mu:             tuningFloat(0.02),
		CrossoverGamma: tuningFloat(0.6),
		Selection:      tuningString("tournament"),
		TournamentSize: tuningInt(5),
		VelMax:         tuningFloat(3.0),
		VelMin:         tuningFloat(-3.0),
	}
}

// fullyPopulatedTuning sets every field, including the ones no single variant
// owns, so the round trip has nothing left to lose.
func fullyPopulatedTuning() *optimizer.MayflyTuning {
	tuning := sharedTuning()

	tuning.Variant = tuningString("desma")
	tuning.Preset = tuningString("multimodal")

	tuning.EliteCount = tuningInt(7)
	tuning.SearchRange = tuningFloat(2.5)
	tuning.EnlargeFactor = tuningFloat(1.2)
	tuning.ReductionFactor = tuningFloat(0.8)

	tuning.OrthogonalFactor = tuningFloat(0.4)
	tuning.ChaosFactor = tuningFloat(0.2)

	tuning.LevyAlpha = tuningFloat(1.4)
	tuning.LevyBeta = tuningFloat(1.1)
	tuning.OppositionRate = tuningFloat(0.25)
	tuning.EliteOppositionCount = tuningInt(4)

	tuning.InitialTemperature = tuningFloat(50)
	tuning.CoolingRate = tuningFloat(0.9)
	tuning.CoolingSchedule = tuningString("linear")
	tuning.CauchyMutationRate = tuningFloat(0.4)
	tuning.GoldenFactor = tuningFloat(1.5)
	tuning.ApplyOBLToGlobalBest = tuningBoolValue(true)

	tuning.MedianWeight = tuningFloat(0.6)
	tuning.GravityType = tuningString("sigmoid")
	tuning.UseWeightedMedian = tuningBoolValue(true)

	tuning.AquilaWeight = tuningFloat(0.9)
	tuning.OppositionProbability = tuningFloat(0.35)
	tuning.ArchiveSize = tuningInt(64)
	tuning.StrategySwitch = tuningInt(500)

	tuning.Convergence = &optimizer.MayflyConvergence{
		TargetCost:           tuningFloat(0),
		MinImprovement:       tuningFloat(1e-6),
		StagnationIterations: tuningInt(50),
		MinIterations:        tuningInt(10),
	}

	tuning.Schedule = &optimizer.MayflySchedule{
		Epochs:        tuningInt(3),
		Restarts:      tuningInt(2),
		ClassifyEvals: tuningInt(500),
	}

	return tuning
}

func tuningInt(value int) *int { return &value }

func tuningFloat(value float64) *float64 { return &value }

func tuningString(value string) *string { return &value }

func tuningBoolValue(value bool) *bool { return &value }
