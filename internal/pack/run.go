package pack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
)

// RunOptions narrows what a single invocation executes. They decide whether a
// note runs now, never what it runs.
type RunOptions struct {
	// Limit stops after this many notes have been fitted. Zero runs them all.
	// Notes skipped because they are already finished do not count.
	Limit int

	// OnlyNote fits one MIDI note. Zero runs every note, which is safe here in
	// a way it would not be for a block index, because MIDI 0 is not a note
	// any pack of struck bars contains.
	OnlyNote int
}

// Run fits every note of a planned pack, in ascending note order.
//
// One note at a time at the manifest's fixed worker width, for the reason a
// campaign does it: two fits at once contend for cores and turn the elapsed
// column into a measure of the machine's load. A finished note is skipped, so
// an interrupted run resumes; a note a cancelled context cut short is not
// finished whatever its result.json says, so its directory is cleared and it
// runs again.
func Run(ctx context.Context, dir string, log io.Writer, opts RunOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if log == nil {
		log = io.Discard
	}

	manifest, err := ReadManifest(dir)
	if err != nil {
		return err
	}

	if err := checkProvenance(manifest); err != nil {
		return err
	}

	ran := 0

	for index, job := range manifest.Jobs {
		if err := ctx.Err(); err != nil {
			return err
		}

		if opts.OnlyNote != 0 && job.Note != opts.OnlyNote {
			continue
		}

		jobPath := filepath.Join(dir, job.Dir)

		finished, canceled, err := inspectJob(jobPath)
		if err != nil {
			return fmt.Errorf("note %s: %w", job.Name, err)
		}

		if finished {
			_, _ = fmt.Fprintf(log, "[%2d/%2d] %-5s MIDI %3d  skipped, result.json exists\n",
				index+1, len(manifest.Jobs), job.Name, job.Note)

			continue
		}

		if opts.Limit > 0 && ran >= opts.Limit {
			break
		}

		if canceled {
			if err := os.RemoveAll(jobPath); err != nil {
				return fmt.Errorf("note %s: clear the cancelled run directory %q: %w", job.Name, jobPath, err)
			}

			_, _ = fmt.Fprintf(log, "[%2d/%2d] %-5s MIDI %3d  repeating, the previous run was cancelled\n",
				index+1, len(manifest.Jobs), job.Name, job.Note)
		}

		if err := runJob(ctx, manifest, job, jobPath, index, log); err != nil {
			return err
		}

		ran++
	}

	return ctx.Err()
}

// inspectJob reads what a run directory says about its note. An unreadable
// summary counts as no summary: a run that cannot be described is one nothing
// can be concluded from, and repeating it is cheaper than reasoning about it.
func inspectJob(dir string) (finished, canceled bool, err error) {
	raw, err := os.ReadFile(filepath.Join(dir, fitrun.FileResult))
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}

		return false, false, fmt.Errorf("read %s: %w", fitrun.FileResult, err)
	}

	var summary fitrun.Summary

	if err := json.Unmarshal(raw, &summary); err != nil {
		return false, false, nil
	}

	if summary.StopReason == "canceled" {
		return false, true, nil
	}

	return true, false, nil
}

// checkProvenance refuses to add notes under a different build or a recording
// edited since it was planned. There is no override, for the reason campaign
// has none: a result set is only a comparison if every row came out of the same
// objective.
func checkProvenance(manifest *Manifest) error {
	path, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the running binary: %w", err)
	}

	sum, err := fitrun.FileSHA256(path)
	if err != nil {
		return err
	}

	if sum != manifest.Binary.SHA256 {
		return fmt.Errorf(
			"this binary is not the one that planned the run: %s is %s, the manifest names %s (%s); "+
				"finish the run with the planning build or plan a new one",
			path, sum, manifest.Binary.SHA256, manifest.Binary.Path)
	}

	for _, job := range manifest.Jobs {
		referenceSum, err := fitrun.FileSHA256(job.Reference.Path)
		if err != nil {
			return err
		}

		if referenceSum != job.Reference.SHA256 {
			return fmt.Errorf(
				"the recording for %s has changed since the run was planned: %s is %s, the manifest names %s",
				job.Name, job.Reference.Path, referenceSum, job.Reference.SHA256)
		}
	}

	return nil
}

// runJob fits one note and reports it in one line. fitrun writes the note's own
// log.txt, so none of the search's chatter is passed up.
func runJob(ctx context.Context, manifest *Manifest, job Job, dir string, index int, log io.Writer) error {
	spec := fitrun.Spec{
		Dir:           dir,
		ReferencePath: job.Reference.Path,

		// The fit renders at the note the bar sounds, and AuthoredNote writes
		// the preset in that same frame. Nothing is transposed in between, so a
		// fitted mode frequency is that bar's partial as measured rather than
		// as converted, which is what the note-versus-partial regression needs.
		// Without the second line the preset would be authored at the embedded
		// template's note 69 and a C6 bar's 1046 Hz fundamental would be
		// written as 439.7 Hz -- correct to render, useless to regress.
		Note:         job.Note,
		AuthoredNote: job.Note,

		Modes:          manifest.Modes,
		Metric:         manifest.Profile,
		Engine:         manifest.Engine,
		MaxEvaluations: manifest.Budget,

		// The clock is off, so the evaluation cap is what ends the run and a
		// rerun at the same seed reproduces the fit.
		TimeBudget: 0,

		Seed:        job.Seed,
		Workers:     manifest.Workers,
		ReportEvery: 1,
		GeneratedBy: "glockenspiel-campaign pack",
		Name:        fmt.Sprintf("pack/%s/%s", filepath.Base(manifest.Pack), job.Name),
	}

	outcome, err := fitrun.Run(ctx, spec, nil)
	if err != nil {
		return fmt.Errorf("note %s: %w", job.Name, err)
	}

	summary := outcome.Summary

	_, _ = fmt.Fprintf(log,
		"[%2d/%2d] %-5s MIDI %3d  score %.6f  %2d modes  %d/%d pinned  %6d evals  %5.1fs  %s\n",
		index+1, len(manifest.Jobs), job.Name, job.Note, summary.Score, summary.SeededModes,
		summary.Pinned, summary.Dimension, summary.Evaluations, summary.ElapsedSeconds, summary.StopReason)

	return nil
}
