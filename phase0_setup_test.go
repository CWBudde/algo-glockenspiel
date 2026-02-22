package glockenspiel_test

import (
	"os"
	"testing"
)

func TestPhase0TestdataExists(t *testing.T) {
	requiredPaths := []string{
		"testdata/reference/glockenspiel_a4.wav",
		"testdata/presets",
	}

	for _, path := range requiredPaths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
}
