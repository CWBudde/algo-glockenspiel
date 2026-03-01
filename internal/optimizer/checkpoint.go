package optimizer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Checkpoint stores resumable optimizer state at a coarse granularity.
type Checkpoint struct {
	Version    string    `json:"version"`
	Timestamp  time.Time `json:"timestamp"`
	Iteration  int       `json:"iteration"`
	BestCost   float64   `json:"best_cost"`
	BestParams []float64 `json:"best_params"`
	Optimizer  string    `json:"optimizer"`
	Metric     string    `json:"metric"`
}

// SaveCheckpoint writes a checkpoint atomically to disk.
func SaveCheckpoint(path string, cp *Checkpoint) error {
	if cp == nil {
		return fmt.Errorf("checkpoint cannot be nil")
	}
	if cp.Version == "" {
		cp.Version = "1.0"
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
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close checkpoint temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename checkpoint temp file: %w", err)
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
	if len(cp.BestParams) == 0 {
		return nil, fmt.Errorf("checkpoint %q missing best_params", path)
	}
	return &cp, nil
}

// FindLatestCheckpoint returns the lexicographically latest checkpoint in workDir.
func FindLatestCheckpoint(workDir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(workDir, "checkpoint_*.json"))
	if err != nil {
		return "", fmt.Errorf("glob checkpoints: %w", err)
	}
	if len(matches) == 0 {
		return "", os.ErrNotExist
	}
	sort.Strings(matches)
	return matches[len(matches)-1], nil
}
