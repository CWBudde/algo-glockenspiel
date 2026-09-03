package campaign

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// traceRecord is the part of a trace line the scoring rule reads. Best is a
// pointer because the line writes null for a cost that is not a number, and a
// null cost is not a zero cost.
type traceRecord struct {
	Evaluations int      `json:"evaluations"`
	Best        *float64 `json:"best"`
}

// Collect turns a campaign's run directories into one results.csv and returns
// its path.
//
// The scoring rule is the one the whole design rests on. Backends do not stop
// exactly on an evaluation cap: a generation is atomic, so a run may overrun
// the budget by up to one generation, and the arm with the larger population
// would get the larger overrun. So a job is scored at the best cost its trace
// recorded at or below the cap, not at the cost it finished with. The finished
// cost is kept in its own column, because the difference between the two is
// worth seeing.
func Collect(dir string, partial bool) (string, error) {
	manifest, err := ReadManifest(dir)
	if err != nil {
		return "", err
	}

	present := make([]Job, 0, len(manifest.Jobs))

	canceled := 0

	for _, job := range manifest.Jobs {
		state, err := inspectJob(filepath.Join(dir, job.Dir))
		if err != nil {
			return "", fmt.Errorf("job %s: %w", job.ID, err)
		}

		switch state {
		case jobFinished:
			present = append(present, job)
		case jobCanceled:
			// A cancelled job wrote a summary, but it spent a fraction of its
			// budget, so scoring it against the budget would put a run that
			// never happened into the comparison.
			if !partial {
				return "", fmt.Errorf(
					"job %s was cut short by a cancelled context and is not comparable: run the campaign "+
						"again to repeat it, or collect with partial results to leave it out",
					job.ID)
			}

			canceled++
		case jobPending:
		}
	}

	missing := len(manifest.Jobs) - len(present)

	if missing > 0 {
		if !partial {
			return "", fmt.Errorf(
				"campaign %q is unfinished: %d of %d jobs have a %s; run the rest, or collect with partial "+
					"results if an incomplete set is what you want",
				dir, len(present), len(manifest.Jobs), fitrun.FileResult)
		}

		fmt.Fprintf(os.Stderr, "warning: collecting %d of %d jobs, %d are unfinished (%d of them cancelled)\n",
			len(present), len(manifest.Jobs), missing, canceled)
	}

	rows := make([]Row, 0, len(present))

	for _, job := range present {
		row, err := collectJob(manifest, job, filepath.Join(dir, job.Dir))
		if err != nil {
			return "", err
		}

		rows = append(rows, row)
	}

	path := filepath.Join(dir, FileResults)

	if err := WriteResults(path, rows); err != nil {
		return "", err
	}

	return path, nil
}

// collectJob turns one run directory into one row.
func collectJob(manifest *Manifest, job Job, dir string) (Row, error) {
	design := manifest.Design

	arm, err := design.ArmByName(job.Arm)
	if err != nil {
		return Row{}, fmt.Errorf("job %s: %w", job.ID, err)
	}

	summary, err := readSummary(dir)
	if err != nil {
		return Row{}, fmt.Errorf("job %s: %w", job.ID, err)
	}

	score, scoredEvaluations, err := scoreTrace(filepath.Join(dir, fitrun.FileTrace), design.Budget)
	if err != nil {
		return Row{}, fmt.Errorf("job %s: %w", job.ID, err)
	}

	terms := make(map[optimizer.Term]float64, len(optimizer.Terms()))
	for _, term := range optimizer.Terms() {
		terms[term] = summary.Terms.Value(term)
	}

	return Row{
		Design:            design.Name,
		Arm:               job.Arm,
		Block:             job.Block,
		Seed:              job.Seed,
		Job:               job.ID,
		Engine:            arm.Engine.Name,
		Covariance:        arm.Engine.CMAES.Covariance,
		Lambda:            summary.Lambda,
		Population:        summary.Population,
		RestartsPlanned:   arm.RestartsPlanned,
		Budget:            design.Budget,
		Score:             score,
		ScoredEvaluations: scoredEvaluations,
		FinalScore:        summary.Score,
		Evaluations:       summary.Evaluations,
		Iterations:        summary.Iterations,
		Restarts:          summary.Restarts,
		StopReason:        summary.StopReason,
		Converged:         summary.Converged,
		ElapsedS:          summary.ElapsedSeconds,
		Pinned:            summary.Pinned,
		Dimension:         summary.Dimension,
		Matched:           summary.Matched,
		Terms:             terms,
		MayflyVersion:     summary.Identity.Libraries[fitrun.MayflyLibrary],
		CMAESVersion:      summary.Identity.Libraries[fitrun.CMAESLibrary],
		Revision:          summary.Identity.Revision,
	}, nil
}

// readSummary loads a job's result.json.
func readSummary(dir string) (fitrun.Summary, error) {
	path := filepath.Join(dir, fitrun.FileResult)

	raw, err := os.ReadFile(path)
	if err != nil {
		return fitrun.Summary{}, fmt.Errorf("read summary %q: %w", path, err)
	}

	var summary fitrun.Summary

	if err := json.Unmarshal(raw, &summary); err != nil {
		return fitrun.Summary{}, fmt.Errorf("parse summary %q: %w", path, err)
	}

	return summary, nil
}

// scoreTrace applies the cap-matched scoring rule: the best cost recorded at
// or below the budget, and the evaluation count of the line that first reached
// it. A run whose trace has nothing under the cap is an error rather than a
// missing row, because it means the run never reported inside its budget and
// no comparison could include it honestly.
func scoreTrace(path string, budget int) (float64, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("open trace %q: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	var (
		best      float64
		evaluated int
		found     bool
	)

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var record traceRecord

		if err := json.Unmarshal(line, &record); err != nil {
			return 0, 0, fmt.Errorf("parse trace %q: %w", path, err)
		}

		if record.Best == nil || record.Evaluations > budget {
			continue
		}

		if !found || *record.Best < best {
			best, evaluated, found = *record.Best, record.Evaluations, true
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("read trace %q: %w", path, err)
	}

	if !found {
		return 0, 0, fmt.Errorf("trace %q has no report at or below %d evaluations", path, budget)
	}

	return best, evaluated, nil
}
