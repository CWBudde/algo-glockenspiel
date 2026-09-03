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

func TestCheckpointCarriesMayflyTuning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint_0001.json")

	epochs := 3
	selection := "rank"

	want := &Checkpoint{
		Version:    CheckpointVersion,
		Iteration:  4,
		BestCost:   0.5,
		BestParams: []float64{0.25},
		Optimizer:  "mayfly",
		Metric:     "rms",
		State: &OptimizerState{
			Kind: "mayfly",
			Mayfly: &MayflyCheckpointEnv{
				Variant:    "desma",
				Preset:     "balanced",
				Population: 8,
				Seed:       17,
				Epochs:     epochs,
				Restarts:   2,
				Tuning: &MayflyTuning{
					Selection: &selection,
					Schedule:  &MayflySchedule{Epochs: &epochs},
				},
			},
		},
	}
	if err := SaveCheckpoint(path, want); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	got, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}

	env := got.State.Mayfly
	if env == nil {
		t.Fatal("expected the checkpoint to carry mayfly state")
	}

	if env.Preset != "balanced" || env.Epochs != 3 || env.Restarts != 2 {
		t.Fatalf("unexpected mayfly environment round-trip: %#v", env)
	}

	if env.Tuning == nil || env.Tuning.Selection == nil || *env.Tuning.Selection != selection {
		t.Fatalf("expected the tuning document to round-trip, got %#v", env.Tuning)
	}

	if env.Tuning.Schedule == nil || env.Tuning.Schedule.Epochs == nil || *env.Tuning.Schedule.Epochs != epochs {
		t.Fatalf("expected the schedule block to round-trip, got %#v", env.Tuning.Schedule)
	}
}

func TestLoadCheckpointWithoutTuningStaysReadable(t *testing.T) {
	// The tuning fields are additive: they describe how the search was
	// configured, not how BestParams is encoded, so CheckpointVersion does not
	// move and a checkpoint written before they existed still resumes.
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint_0001.json")

	legacy := `{"version":"2.0","iteration":5,"best_cost":0.25,"best_params":[0.1,0.2],` +
		`"optimizer":"mayfly","metric":"rms",` +
		`"state":{"kind":"mayfly","mayfly":{"variant":"desma","population":10,"seed":1}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write checkpoint fixture: %v", err)
	}

	cp, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}

	if cp.Version != CheckpointVersion {
		t.Fatalf("checkpoint format version should still be %s, got %s", CheckpointVersion, cp.Version)
	}

	env := cp.State.Mayfly
	if env.Tuning != nil || env.Preset != "" || env.Epochs != 0 || env.Restarts != 0 {
		t.Fatalf("expected the tuning fields to stay empty, got %#v", env)
	}
}

// TestCheckpointCarriesTheCMAESRestartLadder pins the two keys a resumed
// CMA-ES run needs to keep searching the way the run that wrote the checkpoint
// did. The JSON names are a contract: a run directory's checkpoint.json is
// read back by tooling that never sees this struct.
func TestCheckpointCarriesTheCMAESRestartLadder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint_0001.json")

	want := &Checkpoint{
		Version:    CheckpointVersion,
		Iteration:  9,
		BestCost:   0.75,
		BestParams: []float64{0.5},
		Optimizer:  "cmaes",
		Metric:     "rms",
		State: &OptimizerState{
			Kind: "cmaes",
			CMAES: &CMAESCheckpointEnv{
				Covariance:     "separable",
				Lambda:         12,
				Sigma:          0.3,
				Seed:           23,
				RunEvaluations: 4000,
				LambdaGrowth:   2,
			},
		},
	}
	if err := SaveCheckpoint(path, want); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}

	for _, key := range []string{`"run_evaluations": 4000`, `"lambda_growth": 2`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("expected the checkpoint to hold %s, got %s", key, raw)
		}
	}

	got, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}

	env := got.State.CMAES
	if env == nil {
		t.Fatal("expected the checkpoint to carry cmaes state")
	}

	if env.RunEvaluations != 4000 || env.LambdaGrowth != 2 {
		t.Fatalf("unexpected restart ladder round-trip: %#v", env)
	}
}
