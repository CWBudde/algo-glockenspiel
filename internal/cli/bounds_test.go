package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/optimizer"
)

func TestLoadParamBounds(t *testing.T) {
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
			name:    "rejects unknown key",
			content: `{"decay_millis": [50.0, 400.0]}`,
			wantErr: "decode bounds",
		},
		{
			name:    "rejects malformed json",
			content: `{"decay_ms": [50.0`,
			wantErr: "decode bounds",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bounds.json")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("write bounds fixture: %v", err)
			}

			bounds, err := loadParamBounds(path)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("loadParamBounds failed: %v", err)
			}

			tc.check(t, bounds)
		})
	}
}

func TestLoadParamBoundsMissingFile(t *testing.T) {
	if _, err := loadParamBounds(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("expected missing bounds file to fail")
	}
}
