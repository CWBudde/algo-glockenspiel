package fitschema_test

import (
	"testing"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/fitschema"
)

func TestParseDurationRoundTripsGoDurationsAndBareSeconds(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"30", 30 * time.Second},
		{"30s", 30 * time.Second},
		{"2m", 2 * time.Minute},
		{"1h30m", time.Hour + 30*time.Minute},
	}

	for _, test := range cases {
		got, err := fitschema.ParseDuration(test.raw)
		if err != nil {
			t.Fatalf("ParseDuration(%q) returned %v, want %s", test.raw, err, test.want)
		}

		if got != test.want {
			t.Fatalf("ParseDuration(%q) = %s, want %s", test.raw, got, test.want)
		}
	}
}

func TestParseDurationRejectsNegativeSecondsAndGarbage(t *testing.T) {
	for _, raw := range []string{"-1s", "abc"} {
		if _, err := fitschema.ParseDuration(raw); err == nil {
			t.Fatalf("ParseDuration(%q) succeeded, want an error", raw)
		}
	}
}

func TestParseDurationRejectsNonFiniteBareSeconds(t *testing.T) {
	for _, raw := range []string{"NaN", "Inf", "-Inf"} {
		if _, err := fitschema.ParseDuration(raw); err == nil {
			t.Fatalf("ParseDuration(%q) succeeded, want an error", raw)
		}
	}
}
