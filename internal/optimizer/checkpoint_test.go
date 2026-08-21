package optimizer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveLoadCheckpointRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint_0001.json")

	want := &Checkpoint{
		Version:    CheckpointVersion,
		Iteration:  12,
		BestCost:   0.123,
		BestParams: []float64{1, 2, 3},
		Optimizer:  "simple",
		Metric:     "rms",
		State: &OptimizerState{
			Kind: "simple",
		},
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

	if got.State == nil || got.State.Kind != want.State.Kind {
		t.Fatalf("unexpected checkpoint state round-trip: got %#v want %#v", got.State, want.State)
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

func TestFindLatestCheckpointSortsNumerically(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  string
	}{
		{
			name:  "index outgrows zero padding",
			files: []string{"checkpoint_0001.json", "checkpoint_9999.json", "checkpoint_10000.json"},
			want:  "checkpoint_10000.json",
		},
		{
			name:  "padded indices",
			files: []string{"checkpoint_0001.json", "checkpoint_0010.json", "checkpoint_0003.json"},
			want:  "checkpoint_0010.json",
		},
		{
			name:  "unparsable names rank below numbered ones",
			files: []string{"checkpoint_final.json", "checkpoint_0002.json"},
			want:  "checkpoint_0002.json",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
					t.Fatalf("write checkpoint fixture: %v", err)
				}
			}

			got, err := FindLatestCheckpoint(dir)
			if err != nil {
				t.Fatalf("FindLatestCheckpoint failed: %v", err)
			}

			if filepath.Base(got) != tc.want {
				t.Fatalf("unexpected latest checkpoint: got %s want %s", filepath.Base(got), tc.want)
			}
		})
	}
}

func TestSaveCheckpointStampsTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint_0001.json")

	before := time.Now().UTC().Add(-time.Second)

	if err := SaveCheckpoint(path, &Checkpoint{BestParams: []float64{1}}); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	got, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}

	if got.Timestamp.Before(before) || got.Timestamp.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("unexpected checkpoint timestamp: %s", got.Timestamp)
	}

	if got.Version != CheckpointVersion {
		t.Fatalf("expected a default version, got %q", got.Version)
	}
}

func TestSaveCheckpointLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	if err := SaveCheckpoint(filepath.Join(dir, "checkpoint_0001.json"), &Checkpoint{BestParams: []float64{1, 2}}); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	if len(entries) != 1 || entries[0].Name() != "checkpoint_0001.json" {
		t.Fatalf("expected only the checkpoint to remain, got %v", entries)
	}
}

func TestSaveCheckpointRejectsEmptyParams(t *testing.T) {
	if err := SaveCheckpoint(filepath.Join(t.TempDir(), "checkpoint_0001.json"), &Checkpoint{}); err == nil {
		t.Fatal("expected empty best_params to be rejected")
	}
}

func TestLoadCheckpointRejectsOlderFormat(t *testing.T) {
	// Version 1.0 encoded decay times linearly; the current encoding is
	// logarithmic. Resuming a 1.0 vector would restart from a silently wrong
	// decay, so it must be refused rather than reinterpreted.
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint_0001.json")

	legacy := `{"version":"1.0","iteration":5,"best_cost":0.25,"best_params":[0.1,0.2]}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy checkpoint: %v", err)
	}

	_, err := LoadCheckpoint(path)
	if err == nil {
		t.Fatal("expected an error for a version 1.0 checkpoint, got nil")
	}

	if !strings.Contains(err.Error(), "format version 1.0") {
		t.Errorf("error should name the offending version, got %q", err)
	}
}
