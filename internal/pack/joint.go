package pack

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
)

// JointOptions are what FitJoint needs beyond the planned pack it reads its
// notes from.
type JointOptions struct {
	// Budget is the evaluation cap for the whole fit, not per note. One
	// evaluation renders the candidate at every note, so a joint evaluation
	// costs about as much as the pack is long.
	Budget int

	// AuthoredNote is the note the preset is authored at. Zero takes the median
	// of the pack's notes.
	AuthoredNote int

	// Notes restricts the fit to these MIDI notes. Empty fits every note of the
	// pack. A subset is for iterating: a seven-note run costs about a third of
	// a twenty-note one and still spans the range, but a published number
	// should come from the whole pack.
	Notes []int

	// Modes is how many partials to seed. Zero takes every partial the authored
	// note's analysis found.
	Modes int

	// Seed selects the random stream.
	Seed int64

	// Workers bounds parallel evaluation. Zero follows the machine.
	Workers int
}

// FitJoint fits one preset against every note of a planned pack at once.
//
// It reads the pack's manifest rather than the directory, so the notes it fits
// are the ones a human read in the plan table and every recording is the one
// the manifest pinned by hash. The result is an ordinary fitrun run directory,
// with its per-note files under notes/<nnn>/.
func FitJoint(ctx context.Context, packRunDir, outDir string, log io.Writer, opts JointOptions) (*fitrun.Outcome, []int, error) {
	if log == nil {
		log = io.Discard
	}

	manifest, err := ReadManifest(packRunDir)
	if err != nil {
		return nil, nil, err
	}

	if opts.Budget <= 0 {
		return nil, nil, fmt.Errorf("budget must be positive, got %d", opts.Budget)
	}

	wanted := make(map[int]bool, len(opts.Notes))
	for _, note := range opts.Notes {
		wanted[note] = true
	}

	refs := make([]fitrun.ReferenceSpec, 0, len(manifest.Jobs))

	for _, job := range manifest.Jobs {
		if len(wanted) > 0 && !wanted[job.Note] {
			continue
		}

		refs = append(refs, fitrun.ReferenceSpec{Path: job.Reference.Path, Note: job.Note})
	}

	if len(refs) == 0 {
		return nil, nil, fmt.Errorf("no note of %s matches the requested subset %v", packRunDir, opts.Notes)
	}

	if len(refs) < 2 {
		return nil, nil, fmt.Errorf(
			"a joint fit over one note is a single-note fit: use `pack run`, or widen the subset")
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create %q: %w", outDir, err)
	}

	spec := fitrun.Spec{
		Dir:            outDir,
		References:     refs,
		AuthoredNote:   opts.AuthoredNote,
		Modes:          opts.Modes,
		Metric:         manifest.Profile,
		Engine:         manifest.Engine,
		MaxEvaluations: opts.Budget,
		TimeBudget:     0,
		Seed:           opts.Seed,
		Workers:        opts.Workers,
		ReportEvery:    1,
		GeneratedBy:    "glockenspiel-campaign pack fit-joint",
		Name:           fmt.Sprintf("pack-joint/%s", filepath.Base(manifest.Pack)),
	}

	fitted := make([]int, 0, len(refs))
	for _, ref := range refs {
		fitted = append(fitted, ref.Note)
	}

	outcome, err := fitrun.Run(ctx, spec, log)
	if err != nil {
		return nil, nil, err
	}

	return outcome, fitted, nil
}
