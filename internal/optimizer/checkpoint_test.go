package optimizer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadCheckpointRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint_0001.json")

	want := &Checkpoint{
		Version:    "1.0",
		Iteration:  12,
		BestCost:   0.123,
		BestParams: []float64{1, 2, 3},
		Optimizer:  "simple",
		Metric:     "rms",
	}
	if err := SaveCheckpoint(path, want); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	got, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if got.Iteration != want.Iteration || got.BestCost != want.BestCost || len(got.BestParams) != len(want.BestParams) {
		t.Fatalf("unexpected checkpoint round-trip: got %#v want %#v", got, want)
	}
}

func TestFindLatestCheckpoint(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"checkpoint_0001.json", "checkpoint_0010.json", "checkpoint_0003.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatalf("write checkpoint fixture: %v", err)
		}
	}

	got, err := FindLatestCheckpoint(dir)
	if err != nil {
		t.Fatalf("FindLatestCheckpoint failed: %v", err)
	}
	if filepath.Base(got) != "checkpoint_0010.json" {
		t.Fatalf("unexpected latest checkpoint: %s", got)
	}
}

func TestFindLatestCheckpointMissing(t *testing.T) {
	_, err := FindLatestCheckpoint(t.TempDir())
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}
