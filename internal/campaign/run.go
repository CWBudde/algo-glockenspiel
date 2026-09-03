package campaign

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
)

// RunOptions narrows what a single invocation executes. They exist for
// operating a long campaign, not for changing it: neither option can alter a
// job, only decide whether it runs now.
type RunOptions struct {
	// Limit stops after this many jobs have been run. Zero runs them all.
	// Jobs skipped because they are already finished do not count.
	Limit int

	// OnlyBlock runs one block. Minus one runs every block, which is what a
	// zero value must not mean here because block zero is a real block.
	OnlyBlock int
}

// Run executes the jobs of a planned campaign, in manifest order.
//
// The jobs run in this process and one at a time, each at the manifest's fixed
// worker width. Running two at once would let them contend for cores and turn
// the elapsed column into a measure of the machine's load rather than of the
// arm, and 8.4 showed the result is bit-identical across widths at a fixed
// seed, so a fixed width costs nothing.
//
// A finished job is skipped, so an interrupted campaign resumes where it
// stopped. A job a cancelled context cut short is not finished, whatever its
// result.json says, so its directory is cleared and the job runs again. A job
// that fails aborts the campaign with the job in the error: a silently skipped
// job would leave a gap that collect reads as an unfinished campaign and
// analyze never sees at all.
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

	for _, job := range manifest.Jobs {
		if err := ctx.Err(); err != nil {
			return err
		}

		if opts.OnlyBlock >= 0 && job.Block != opts.OnlyBlock {
			continue
		}

		jobPath := filepath.Join(dir, job.Dir)

		state, err := inspectJob(jobPath)
		if err != nil {
			return fmt.Errorf("job %s: %w", job.ID, err)
		}

		if state == jobFinished {
			_, _ = fmt.Fprintf(log, "block %02d %-16s skipped, result.json exists\n", job.Block, job.Arm)

			continue
		}

		if opts.Limit > 0 && ran >= opts.Limit {
			break
		}

		if state == jobCanceled {
			// The whole directory goes rather than being written over. A
			// second run leaves its own files, but a trace from the cut run
			// would otherwise survive alongside them and collect scores from
			// the trace.
			if err := os.RemoveAll(jobPath); err != nil {
				return fmt.Errorf("job %s: clear the cancelled run directory %q: %w", job.ID, jobPath, err)
			}

			_, _ = fmt.Fprintf(log, "block %02d %-16s repeating, the previous run was cancelled\n",
				job.Block, job.Arm)
		}

		if err := runJob(ctx, manifest, job, jobPath, log); err != nil {
			return err
		}

		ran++
	}

	return ctx.Err()
}

// jobState is what a job directory says about the job.
type jobState int

const (
	// jobPending is a job that has not run, or whose summary cannot be read.
	// An unreadable summary is treated as no summary because a run that
	// cannot be described is a run nothing can be concluded from, and running
	// it again is cheaper than reasoning about the wreckage.
	jobPending jobState = iota

	// jobFinished is a job that ran to one of its own stopping criteria.
	jobFinished

	// jobCanceled is a job a cancelled context cut short. fitrun writes the
	// whole run directory whatever happened, so such a job looks finished on
	// disk while having spent a fraction of its budget.
	jobCanceled
)

// stopReasonCanceled is the stop reason every backend reports for a run a
// cancelled context ended.
const stopReasonCanceled = "context_canceled"

// inspectJob reads a job's summary and says whether the job is done.
//
// The distinction that matters is the cancelled one. Ctrl-C during a campaign
// leaves a result.json behind, and a resume that trusted the file's existence
// would skip that job forever and collect would then score a job that ran for
// ten seconds against the same budget as one that ran for a minute.
func inspectJob(dir string) (jobState, error) {
	path := filepath.Join(dir, fitrun.FileResult)

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return jobPending, nil
		}

		return jobPending, fmt.Errorf("read summary %q: %w", path, err)
	}

	var summary fitrun.Summary

	if err := json.Unmarshal(raw, &summary); err != nil {
		return jobPending, nil
	}

	if summary.StopReason == stopReasonCanceled {
		return jobCanceled, nil
	}

	return jobFinished, nil
}

// checkProvenance refuses to add jobs to a campaign under a different build or
// a different reference.
//
// There is no override. A result set is only a comparison if every row came
// out of the same objective, and the two things that silently change it are a
// rebuild between jobs and a reference file edited in place.
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
			"this binary is not the one that planned the campaign: %s is %s, the manifest names %s (%s); "+
				"finish the campaign with the planning build or plan a new campaign",
			path, sum, manifest.Binary.SHA256, manifest.Binary.Path)
	}

	referenceSum, err := fitrun.FileSHA256(manifest.Reference.Path)
	if err != nil {
		return err
	}

	if referenceSum != manifest.Reference.SHA256 {
		return fmt.Errorf(
			"the reference has changed since the campaign was planned: %s is %s, the manifest names %s; "+
				"every job would be scored against a different recording",
			manifest.Reference.Path, referenceSum, manifest.Reference.SHA256)
	}

	return nil
}

// runJob runs one job and reports it in one line.
//
// fitrun writes the job's own log.txt, so nothing of the search's chatter is
// passed up here: a sixty job campaign printing every progress report would
// bury the one line per job that says how the campaign is going.
func runJob(ctx context.Context, manifest *Manifest, job Job, dir string, log io.Writer) error {
	design := manifest.Design

	arm, err := design.ArmByName(job.Arm)
	if err != nil {
		return fmt.Errorf("job %s: %w", job.ID, err)
	}

	spec := fitrun.Spec{
		Dir:            dir,
		ReferencePath:  manifest.Reference.Path,
		Note:           design.Note,
		Modes:          design.Modes,
		Metric:         design.Profile,
		Engine:         arm.Engine,
		MaxIterations:  arm.MaxIterations,
		MaxEvaluations: design.Budget,
		Seed:           job.Seed,
		Workers:        manifest.Workers,
		ReportEvery:    1,
		GeneratedBy:    "glockenspiel-campaign",
		Name:           fmt.Sprintf("%s/%s/b%02d", design.Name, arm.Name, job.Block),
	}

	outcome, err := fitrun.Run(ctx, spec, nil)
	if err != nil {
		return fmt.Errorf("job %s: %w", job.ID, err)
	}

	summary := outcome.Summary

	_, _ = fmt.Fprintf(log, "block %02d %-16s score=%.6f evals=%d restarts=%d elapsed=%.1fs\n",
		job.Block, job.Arm, summary.Score, summary.Evaluations, summary.Restarts, summary.ElapsedSeconds)

	return nil
}
