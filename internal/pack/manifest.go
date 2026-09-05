package pack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/campaign"
	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// The three files a pack directory owns. Everything else under it is a fitrun
// run directory, one per note.
const (
	FileManifest    = "manifest.json"
	FileResults     = "pack-results.csv"
	FileModeResults = "pack-modes.csv"
)

// Manifest is the pack directory's plan: every note, the file it came from
// pinned by content hash, and the build that planned it.
//
// Written once with O_EXCL and never rewritten, for the reason campaign's is:
// a directory of run directories paired with a plan that no longer describes
// them is worse than no plan.
type Manifest struct {
	Pack        string           `json:"pack"`
	Description string           `json:"description"`
	Created     time.Time        `json:"created"`
	Binary      campaign.Binary  `json:"binary"`
	Profile     optimizer.Metric `json:"profile"`
	Modes       int              `json:"modes"`
	Budget      int              `json:"budget"`
	SeedBase    int64            `json:"seed_base"`
	MaxCents    float64          `json:"max_cents"`
	Workers     int              `json:"workers"`
	Engine      fitrun.Engine    `json:"engine"`
	Jobs        []Job            `json:"jobs"`
}

// Job is one note's fit.
//
// Note is the note the fit renders at *and* the note the resulting preset is
// authored at, so a fitted mode frequency reads directly as that bar's partial
// with no transposition in between. Cents records how far the recording sits
// from equal temperament, which is a property of the bar and is the part of it
// no single transposed preset can reproduce.
type Job struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Note      int               `json:"note"`
	Cents     float64           `json:"cents"`
	Reference campaign.FileHash `json:"reference"`
	Seed      int64             `json:"seed"`
	Dir       string            `json:"dir"`
}

func jobDir(note int) string {
	return filepath.Join("notes", fmt.Sprintf("%03d", note))
}

// writeManifest creates the pack directory and writes the manifest
// exclusively. An existing manifest is refused rather than replaced.
func writeManifest(dir string, manifest *Manifest) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create pack directory %q: %w", dir, err)
	}

	path := filepath.Join(dir, FileManifest)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf(
				"pack run %q already exists: planning it again would write a second plan beside the run "+
					"directories the first one produced; collect it or plan into a new directory", dir)
		}

		return fmt.Errorf("create manifest %q: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(manifest); err != nil {
		return fmt.Errorf("write manifest %q: %w", path, err)
	}

	return file.Close()
}

// ReadManifest loads a pack run's manifest.
func ReadManifest(dir string) (*Manifest, error) {
	path := filepath.Join(dir, FileManifest)

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %q: %w", path, err)
	}

	var manifest Manifest

	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest %q: %w", path, err)
	}

	if len(manifest.Jobs) == 0 {
		return nil, fmt.Errorf("manifest %q lists no notes", path)
	}

	// Run clears a cancelled job's directory before repeating it, so a job
	// path has to stay inside the pack directory: a hand-edited "../.." would
	// otherwise name a tree to delete.
	for _, job := range manifest.Jobs {
		if job.Dir == "" || !filepath.IsLocal(job.Dir) {
			return nil, fmt.Errorf("manifest %q: note %s has a directory %q outside the run",
				path, job.Name, job.Dir)
		}
	}

	return &manifest, nil
}
