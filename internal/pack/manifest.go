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
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

// The three files a pack directory owns. Everything else under it is a fitrun
// run directory, one per note.
const (
	FileManifest    = "manifest.json"
	FileResults     = "pack-results.csv"
	FileModeResults = "pack-modes.csv"

	// FileSeed is the pooled starting preset a joint fit was seeded from, in
	// the run directory the fit wrote. Only a pooled-seed run writes it.
	FileSeed = "pooled-seed.json"
)

// writePresetFile writes a preset as the indented JSON every other document
// here is written as.
func writePresetFile(path string, value *preset.Preset) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %q: %w", path, err)
	}

	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory for %q: %w", path, err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}

	return nil
}

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

	// SampleRate is the rate every recording in the pack is at, discovered
	// when the pack was measured rather than assumed.
	//
	// It is recorded because the whole pipeline used to default to 44,100 and
	// then refuse anything else, which made a 48 kHz pack -- the morphagene
	// one in testdata, for instance -- plannable and then unrunnable,
	// unfittable and unscoreable. A pack whose files disagree with each other
	// is refused at plan time instead, where the disagreement is visible, and
	// omitting the field is how a manifest written before this existed says
	// "assume the old default".
	SampleRate int           `json:"sample_rate,omitempty"`
	Engine     fitrun.Engine `json:"engine"`
	Jobs       []Job         `json:"jobs"`
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

// Rate is the sample rate every fit and every score against this pack must
// use.
//
// A manifest written before SampleRate existed carries zero, and 44,100 is
// what those runs actually used, so that is what a zero means rather than "ask
// the file". Reading it from the recordings instead would silently rescore an
// old pack at a different rate than the one its results were produced at.
func (m *Manifest) Rate() int {
	if m.SampleRate == 0 {
		return 44100
	}

	return m.SampleRate
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
