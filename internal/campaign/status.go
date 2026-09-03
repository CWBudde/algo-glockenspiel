package campaign

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
)

// Status is where a campaign has got to, read from its directory rather than
// from the process running it.
//
// It exists because a campaign is an hour or two of jobs behind a single line
// of output each, and until now the only way to see how far along one was, was
// to count files. It reads only what run has already written, so it is safe to
// call from another shell while a run is in flight, and it never opens the
// manifest for writing.
type Status struct {
	Design    string
	Blocks    int
	Arms      []string
	Total     int
	Finished  int
	Canceled  int
	Pending   int
	InFlight  string
	Elapsed   time.Duration
	MeanJob   time.Duration
	Remaining time.Duration
	Scores    map[string]ArmStatus
}

// ArmStatus is one arm's share of the progress, so a campaign stopped halfway
// says which arms it has and has not covered rather than only how many jobs
// are left.
type ArmStatus struct {
	Finished int
	Total    int
	Best     float64
	BestJob  string
}

// ReadStatus reports a planned campaign's progress.
//
// A job is finished when its result.json records a stop reason other than
// cancellation, which is the same rule Run resumes by, so the count here is
// the count of jobs Run would skip. A job directory holding a config.json and
// no result.json is the one in flight: fitrun writes the config before the
// search and the result after it.
func ReadStatus(dir string) (*Status, error) {
	manifest, err := ReadManifest(dir)
	if err != nil {
		return nil, err
	}

	status := &Status{
		Design: manifest.Design.Name,
		Blocks: manifest.Design.Blocks,
		Total:  len(manifest.Jobs),
		Scores: make(map[string]ArmStatus, len(manifest.Design.Arms)),
	}

	for _, arm := range manifest.Design.Arms {
		status.Arms = append(status.Arms, arm.Name)
		status.Scores[arm.Name] = ArmStatus{Total: manifest.Design.Blocks, Best: 0}
	}

	var (
		spent   time.Duration
		counted int
		first   time.Time
		last    time.Time
	)

	for _, job := range manifest.Jobs {
		jobPath := filepath.Join(dir, job.Dir)

		summary, state, err := readJobSummary(jobPath)
		if err != nil {
			return nil, fmt.Errorf("job %s: %w", job.ID, err)
		}

		switch state {
		case jobFinished:
			status.Finished++

			arm := status.Scores[job.Arm]
			arm.Finished++

			if arm.BestJob == "" || summary.Score < arm.Best {
				arm.Best, arm.BestJob = summary.Score, job.ID
			}

			status.Scores[job.Arm] = arm

			spent += time.Duration(summary.ElapsedSeconds * float64(time.Second))
			counted++

			first, last = spanWith(first, last, jobPath)
		case jobCanceled:
			status.Canceled++
			status.Pending++
		case jobPending:
			status.Pending++

			if status.InFlight == "" && startedButUnfinished(jobPath) {
				status.InFlight = job.ID
			}
		}
	}

	if counted > 0 {
		status.MeanJob = spent / time.Duration(counted)
		status.Remaining = status.MeanJob * time.Duration(status.Pending)
	}

	if !first.IsZero() && last.After(first) {
		status.Elapsed = last.Sub(first)
	}

	return status, nil
}

// readJobSummary reads a job's summary and classifies it, so ReadStatus can
// use the score of a job Run only had to count.
func readJobSummary(dir string) (fitrun.Summary, jobState, error) {
	var summary fitrun.Summary

	raw, err := os.ReadFile(filepath.Join(dir, fitrun.FileResult))
	if err != nil {
		if os.IsNotExist(err) {
			return summary, jobPending, nil
		}

		return summary, jobPending, err
	}

	if err := json.Unmarshal(raw, &summary); err != nil {
		return summary, jobPending, nil
	}

	if summary.StopReason == stopReasonCanceled {
		return summary, jobCanceled, nil
	}

	return summary, jobFinished, nil
}

// startedButUnfinished reports whether a job has been begun: fitrun writes
// config.json before the search and result.json after it, so a directory
// holding the first and not the second is the job in flight.
func startedButUnfinished(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, fitrun.FileConfig))

	return err == nil
}

// spanWith widens the wall-clock span the finished jobs cover, using the
// result files' own timestamps. It is how long the campaign has been going
// rather than how much work it did, which is what an estimate has to be built
// on when a campaign was interrupted and resumed.
func spanWith(first, last time.Time, dir string) (time.Time, time.Time) {
	info, err := os.Stat(filepath.Join(dir, fitrun.FileResult))
	if err != nil {
		return first, last
	}

	stamp := info.ModTime()

	if first.IsZero() || stamp.Before(first) {
		first = stamp
	}

	if stamp.After(last) {
		last = stamp
	}

	return first, last
}

// RenderStatus writes the progress as the few lines someone watching a
// campaign wants: where it is, what is running, how long is left, and what the
// arms have found so far.
func RenderStatus(status *Status) string {
	var out strings.Builder

	fmt.Fprintf(&out, "%s: %d of %d jobs finished (%d blocks x %d arms)\n",
		status.Design, status.Finished, status.Total, status.Blocks, len(status.Arms))

	if status.Canceled > 0 {
		fmt.Fprintf(&out, "%d cancelled job(s) will be repeated by the next run\n", status.Canceled)
	}

	if status.InFlight != "" {
		fmt.Fprintf(&out, "in flight: %s\n", status.InFlight)
	}

	if status.MeanJob > 0 {
		fmt.Fprintf(&out, "%s per job, %s spent, about %s left\n",
			roundSecond(status.MeanJob), roundSecond(status.Elapsed), roundSecond(status.Remaining))
	}

	if status.Finished == 0 {
		return out.String()
	}

	fmt.Fprintf(&out, "\n| arm | finished | best so far | job |\n| --- | --- | --- | --- |\n")

	names := append([]string(nil), status.Arms...)
	sort.Strings(names)

	for _, name := range names {
		arm := status.Scores[name]
		if arm.BestJob == "" {
			fmt.Fprintf(&out, "| %s | 0/%d | n/a | n/a |\n", name, arm.Total)

			continue
		}

		fmt.Fprintf(&out, "| %s | %d/%d | %.6f | %s |\n", name, arm.Finished, arm.Total, arm.Best, arm.BestJob)
	}

	return out.String()
}

// roundSecond keeps a duration readable: nobody reading a progress line wants
// the nanoseconds of an estimate built from a mean.
func roundSecond(d time.Duration) time.Duration {
	return d.Round(time.Second)
}
