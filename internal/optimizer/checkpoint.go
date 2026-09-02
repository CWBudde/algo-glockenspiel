package optimizer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// CheckpointVersion is the current checkpoint format.
//
// BestParams is an encoded parameter vector, so its meaning is tied to the
// encoding in params.go. Version 2.0 encodes decay times logarithmically;
// version 1.0 encoded them linearly, so a 1.0 vector resumed under the current
// encoding would silently restart from a completely different decay. Old
// checkpoints are therefore rejected rather than reinterpreted.
const CheckpointVersion = "2.0"

// Checkpoint stores resumable optimizer state at a coarse granularity.
//
// Iteration follows Progress.Iteration: it is a count of progress reports, not
// of optimizer iterations or objective evaluations. It orders the checkpoint
// files and must never be subtracted from an iteration budget -- use
// OptimizerIterations for that.
type Checkpoint struct {
	Version string `json:"version"`
	// Timestamp records when the checkpoint was written; the fit command shows
	// it when resuming so a stale work directory is easy to spot.
	Timestamp time.Time `json:"timestamp"`
	Iteration int       `json:"iteration"`
	// OptimizerIterations is the backend's own iteration count when the
	// checkpoint was written, in the same unit as OptimizeOptions.MaxIterations.
	// It is the only value a resumed run may charge against its budget. Zero
	// means the writer did not record it.
	OptimizerIterations int             `json:"optimizer_iterations,omitempty"`
	BestCost            float64         `json:"best_cost"`
	BestParams          []float64       `json:"best_params"`
	Optimizer           string          `json:"optimizer"`
	Metric              string          `json:"metric"`
	State               *OptimizerState `json:"state,omitempty"`
}

// OptimizerState stores coarse optimizer-specific resume metadata.
type OptimizerState struct {
	Kind   string               `json:"kind"`
	Mayfly *MayflyCheckpointEnv `json:"mayfly,omitempty"`

	// Modes is how the run's modes were chosen: the number seeded from the
	// reference's partials, or KeepTemplateModes for the template's own. A
	// resumed run must make the same choice or its codec will not fit the
	// vector. Absent -- zero -- in a checkpoint written before seeding
	// existed, which is read as the template's modes, because that is what
	// every such run used.
	Modes int `json:"modes,omitempty"`
}

// MayflyCheckpointEnv stores the Mayfly settings needed to resume consistently.
//
// The tuning surface fields are all omitempty, and CheckpointVersion is
// deliberately unchanged by them: the version guards the meaning of the encoded
// parameter vector, which none of this touches, and LoadCheckpoint decodes with
// a plain json.Unmarshal, so a checkpoint written before these fields existed
// still loads and one written with them still loads in an older build.
type MayflyCheckpointEnv struct {
	Variant    string `json:"variant"`
	Preset     string `json:"preset,omitempty"`
	Population int    `json:"population"`
	Seed       int64  `json:"seed"`
	Epochs     int    `json:"epochs,omitempty"`
	Restarts   int    `json:"restarts,omitempty"`

	// Tuning is the merged document the run was configured with, so a resume
	// reproduces it without the tuning file having to still be on disk.
	Tuning *MayflyTuning `json:"tuning,omitempty"`
}

// SaveCheckpoint writes a checkpoint atomically to disk.
func SaveCheckpoint(path string, cp *Checkpoint) error {
	if cp == nil {
		return fmt.Errorf("checkpoint cannot be nil")
	}

	if cp.Version == "" {
		cp.Version = CheckpointVersion
	}

	if cp.Timestamp.IsZero() {
		cp.Timestamp = time.Now().UTC()
	}

	if len(cp.BestParams) == 0 {
		return fmt.Errorf("checkpoint best_params cannot be empty")
	}

	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("encode checkpoint: %w", err)
	}

	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create checkpoint directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".checkpoint-*.json")
	if err != nil {
		return fmt.Errorf("create checkpoint temp file: %w", err)
	}

	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write checkpoint temp file: %w", err)
	}

	// Without this fsync the rename can become visible before the data does, so
	// a crash mid-run would leave a checkpoint that resumes into garbage.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync checkpoint temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close checkpoint temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename checkpoint temp file: %w", err)
	}

	return syncDir(filepath.Dir(path))
}

// syncDir flushes a directory entry so the rename above survives a crash.
func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open checkpoint directory: %w", err)
	}

	defer func() {
		_ = handle.Close()
	}()

	// Directory fsync is not portable; on platforms that reject it the rename
	// is already durable enough, so this is not a checkpoint failure.
	if err := handle.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) {
		return fmt.Errorf("sync checkpoint directory: %w", err)
	}

	return nil
}

// LoadCheckpoint loads a checkpoint from disk.
func LoadCheckpoint(path string) (*Checkpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read checkpoint %q: %w", path, err)
	}

	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("decode checkpoint %q: %w", path, err)
	}

	if cp.Version == "" {
		return nil, fmt.Errorf("checkpoint %q missing version", path)
	}

	if cp.Version != CheckpointVersion {
		return nil, fmt.Errorf(
			"checkpoint %q has format version %s, want %s: its encoded parameters "+
				"use a different decay encoding and cannot be resumed; delete the work "+
				"directory or start a fresh fit",
			path, cp.Version, CheckpointVersion,
		)
	}

	if len(cp.BestParams) == 0 {
		return nil, fmt.Errorf("checkpoint %q missing best_params", path)
	}

	return &cp, nil
}

// FindLatestCheckpoint returns the checkpoint with the highest iteration index
// in workDir.
func FindLatestCheckpoint(workDir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(workDir, "checkpoint_*.json"))
	if err != nil {
		return "", fmt.Errorf("glob checkpoints: %w", err)
	}

	if len(matches) == 0 {
		return "", os.ErrNotExist
	}

	// Sorting by name would put checkpoint_10000.json before checkpoint_9999.json
	// once the index outgrows its zero padding, so compare the parsed index and
	// fall back to the name only when it cannot be parsed.
	sort.Slice(matches, func(i, j int) bool {
		left, leftOK := checkpointIndex(matches[i])
		right, rightOK := checkpointIndex(matches[j])

		if leftOK != rightOK {
			return rightOK
		}

		if leftOK && left != right {
			return left < right
		}

		return matches[i] < matches[j]
	})

	return matches[len(matches)-1], nil
}

// checkpointIndex extracts the iteration index from a checkpoint file name.
func checkpointIndex(path string) (int, bool) {
	name := strings.TrimSuffix(filepath.Base(path), ".json")

	digits, found := strings.CutPrefix(name, "checkpoint_")
	if !found {
		return 0, false
	}

	index, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}

	return index, true
}
