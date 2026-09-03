package campaign

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
)

// FileManifest and FileResults are the two files a campaign directory owns.
// Everything else under it is a fitrun run directory.
const (
	FileManifest = "manifest.json"
	FileResults  = "results.csv"
)

// Manifest is the campaign directory's plan: the design as planned, the build
// and the reference that planned it, and every job in the order they run.
//
// It is written once, with O_EXCL, and never rewritten. A campaign that could
// be re-planned in place would be a set of run directories paired with a
// design that no longer describes them.
type Manifest struct {
	Design     Design    `json:"design"`
	DesignHash string    `json:"design_hash"`
	Created    time.Time `json:"created"`
	Binary     Binary    `json:"binary"`
	Reference  FileHash  `json:"reference"`
	Workers    int       `json:"workers"`
	Jobs       []Job     `json:"jobs"`
}

// Binary is the executable that planned the campaign. Run refuses to add jobs
// with a different one, because a mid-campaign rebuild would put two versions
// of the objective into one result set and nothing in the numbers would say
// so.
type Binary struct {
	Path     string          `json:"path"`
	SHA256   string          `json:"sha256"`
	Identity fitrun.Identity `json:"identity"`
}

// FileHash pins a file by content rather than by name, because a path is a
// name someone can point at a different recording tomorrow.
type FileHash struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Job is one run: one arm in one block, with the block's seed.
type Job struct {
	ID    string `json:"id"`
	Arm   string `json:"arm"`
	Block int    `json:"block"`
	Seed  int64  `json:"seed"`
	Dir   string `json:"dir"`
}

// jobID and jobDir are the naming rules. Blocks are numbered from zero and
// zero padded to two digits so a directory listing sorts in run order.
func jobID(block int, arm string) string {
	return fmt.Sprintf("b%02d-%s", block, arm)
}

func jobDir(block int, arm string) string {
	return filepath.Join("jobs", fmt.Sprintf("b%02d", block), arm)
}

// jobs builds the job list in block-major order: block outer, arm inner. Every
// arm of a block shares the block's seed, which is what makes a block a pair
// (or a five-tuple) rather than five unrelated runs.
func (d Design) jobs() []Job {
	list := make([]Job, 0, d.Blocks*len(d.Arms))

	for block := range d.Blocks {
		for _, arm := range d.Arms {
			list = append(list, Job{
				ID:    jobID(block, arm.Name),
				Arm:   arm.Name,
				Block: block,
				Seed:  d.SeedBase + int64(block),
				Dir:   jobDir(block, arm.Name),
			})
		}
	}

	return list
}

// ReadManifest loads a campaign's manifest.
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

	// Run clears a cancelled job's directory before repeating it, so a job
	// path is the one thing in the manifest that must stay inside the campaign
	// directory: a hand-edited "../.." would otherwise name a tree to delete.
	for _, job := range manifest.Jobs {
		if job.Dir == "" || !filepath.IsLocal(job.Dir) {
			return nil, fmt.Errorf("manifest %q: job %s has a directory %q outside the campaign", path, job.ID, job.Dir)
		}
	}

	// The hash is recomputed rather than trusted. The manifest is the frozen
	// record of what was planned, and an edit to the design block after the
	// fact would silently change what the jobs are taken to have run under
	// while every result still carried the old hash. Refusing costs nothing:
	// nobody edits a manifest by hand except to change what it says.
	hash, err := manifest.Design.Hash()
	if err != nil {
		return nil, fmt.Errorf("manifest %q: hash its design: %w", path, err)
	}

	if manifest.DesignHash != hash {
		return nil, fmt.Errorf(
			"manifest %q records design hash %s but its design hashes to %s, so the design was edited after planning",
			path, manifest.DesignHash, hash,
		)
	}

	return &manifest, nil
}
