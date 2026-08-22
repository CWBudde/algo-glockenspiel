package optimizer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/optimizer"
)

func TestDecodeParamBounds(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
		check   func(t *testing.T, bounds optimizer.ParamBounds)
	}{
		{
			name:    "narrows only the given dimensions",
			content: `{"base_frequency": [430.0, 450.0], "decay_ms": [50.0, 400.0]}`,
			check: func(t *testing.T, bounds optimizer.ParamBounds) {
				t.Helper()

				if bounds.BaseFrequency != (optimizer.Range{Min: 430, Max: 450}) {
					t.Fatalf("unexpected base frequency bound: %#v", bounds.BaseFrequency)
				}

				if bounds.DecayMs != (optimizer.Range{Min: 50, Max: 400}) {
					t.Fatalf("unexpected decay bound: %#v", bounds.DecayMs)
				}

				if bounds.InputMix != optimizer.DefaultParamBounds.InputMix {
					t.Fatalf("expected omitted key to keep its default: %#v", bounds.InputMix)
				}
			},
		},
		{
			name: "accepts every key",
			content: `{"input_mix":[0,1],"filter_freq":[100,9000],"base_frequency":[400,500],` +
				`"amplitude":[-1,1],"frequency_mult":[0.9,4],"decay_ms":[10,100],"harmonic_gain":[0,1]}`,
			check: func(t *testing.T, bounds optimizer.ParamBounds) {
				t.Helper()

				if bounds.HarmonicGain != (optimizer.Range{Min: 0, Max: 1}) {
					t.Fatalf("unexpected harmonic gain bound: %#v", bounds.HarmonicGain)
				}
			},
		},
		{
			name:    "rejects inverted range",
			content: `{"decay_ms": [400.0, 50.0]}`,
			wantErr: "must be below max",
		},
		{
			name:    "rejects empty range",
			content: `{"decay_ms": [50.0, 50.0]}`,
			wantErr: "must be below max",
		},
		{
			name:    "rejects a non-finite bound",
			content: `{"decay_ms": [50.0, 1e999]}`,
			wantErr: "decode bounds",
		},
		{
			name:    "rejects unknown key",
			content: `{"decay_millis": [50.0, 400.0]}`,
			wantErr: "decode bounds",
		},
		{
			name:    "rejects malformed json",
			content: `{"decay_ms": [50.0`,
			wantErr: "decode bounds",
		},
		{
			name:    "rejects a second document after the first",
			content: `{"decay_ms": [50.0, 400.0]}{"decay_ms": [1.0, 2.0]}`,
			wantErr: "unexpected content after the bounds object",
		},
		{
			name:    "rejects junk after the object",
			content: `{"decay_ms": [50.0, 400.0]} nonsense`,
			wantErr: "unexpected content after the bounds object",
		},
		{
			name:    "rejects a range wholly outside the model domain",
			content: `{"input_mix": [3.0, 4.0]}`,
			wantErr: "leaves the model range",
		},
		{
			name:    "rejects a range overhanging the model domain",
			content: `{"filter_freq": [10.0, 8000.0]}`,
			wantErr: "leaves the model range",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bounds, err := optimizer.DecodeParamBounds([]byte(tc.content), "the test document")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("DecodeParamBounds failed: %v", err)
			}

			tc.check(t, bounds)
		})
	}
}

func TestLoadParamBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bounds.json")
	if err := os.WriteFile(path, []byte(`{"decay_ms": [50.0, 400.0]}`), 0o600); err != nil {
		t.Fatalf("write bounds fixture: %v", err)
	}

	bounds, err := optimizer.LoadParamBounds(path)
	if err != nil {
		t.Fatalf("LoadParamBounds failed: %v", err)
	}

	if bounds.DecayMs != (optimizer.Range{Min: 50, Max: 400}) {
		t.Fatalf("unexpected decay bound: %#v", bounds.DecayMs)
	}
}

func TestLoadParamBoundsMissingFile(t *testing.T) {
	if _, err := optimizer.LoadParamBounds(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("expected missing bounds file to fail")
	}
}

func TestBoundsKeysMatchTheDocument(t *testing.T) {
	// Every documented key must decode, or the --bounds help text and the API
	// error messages would advertise a key the parser rejects. The ranges are
	// per key because they now have to sit inside the model's domain, and no
	// single pair fits both input_mix and filter_freq.
	ranges := map[string]string{
		"input_mix":      "[0.5, 1.5]",
		"filter_freq":    "[500.0, 8000.0]",
		"base_frequency": "[400.0, 500.0]",
		"amplitude":      "[-1.0, 1.0]",
		"frequency_mult": "[0.5, 10.0]",
		"decay_ms":       "[50.0, 400.0]",
		"harmonic_gain":  "[0.0, 1.0]",
	}

	for _, key := range optimizer.BoundsKeys {
		value, ok := ranges[key]
		if !ok {
			t.Fatalf("documented key %q has no test range", key)
		}

		if _, err := optimizer.DecodeParamBounds([]byte(`{"`+key+`": `+value+`}`), "the test document"); err != nil {
			t.Fatalf("documented key %q does not decode: %v", key, err)
		}
	}
}
