package campaign_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/campaign"
	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
)

// bestWithinCap is the scoring rule read straight out of a job's trace, so the
// test checks collect against the file rather than against collect's own
// arithmetic.
func bestWithinCap(t *testing.T, dir string, budget int) float64 {
	t.Helper()

	file, err := os.Open(filepath.Join(dir, fitrun.FileTrace))
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}

	defer func() { _ = file.Close() }()

	best := math.Inf(1)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line struct {
			Evaluations int      `json:"evaluations"`
			Best        *float64 `json:"best"`
		}

		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("parse trace line: %v", err)
		}

		if line.Best != nil && line.Evaluations <= budget && *line.Best < best {
			best = *line.Best
		}
	}

	if math.IsInf(best, 1) {
		t.Fatalf("trace in %q has no report within %d evaluations", dir, budget)
	}

	return best
}

func TestSmokeCampaignEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("the smoke campaign runs four fits")
	}

	t.Chdir(repoRoot(t))

	dir := filepath.Join(t.TempDir(), "campaign")

	design, err := campaign.Lookup("smoke")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	manifest, err := campaign.Plan(design, dir, 2)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	var log bytes.Buffer

	if err := campaign.Run(t.Context(), dir, &log, campaign.RunOptions{OnlyBlock: -1}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := strings.Count(log.String(), "score="); got != len(manifest.Jobs) {
		t.Fatalf("the log reports %d finished jobs, want %d:\n%s", got, len(manifest.Jobs), log.String())
	}

	path, err := campaign.Collect(dir, false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read results: %v", err)
	}

	if header := strings.SplitN(string(raw), "\n", 2)[0]; header != strings.Join(campaign.Header(), ",") {
		t.Fatalf("results header is %q", header)
	}

	rows, err := campaign.ReadResults(path)
	if err != nil {
		t.Fatalf("read results: %v", err)
	}

	if len(rows) != 4 {
		t.Fatalf("collected %d rows, want 4", len(rows))
	}

	seeds := make(map[int][]int64)

	for index, row := range rows {
		job := manifest.Jobs[index]

		if row.Job != job.ID || row.Arm != job.Arm || row.Block != job.Block || row.Seed != job.Seed {
			t.Fatalf("row %d is %+v, want job %+v", index, row, job)
		}

		if row.Budget != design.Budget {
			t.Errorf("row %q has budget %d, want %d", row.Job, row.Budget, design.Budget)
		}

		want := bestWithinCap(t, filepath.Join(dir, job.Dir), design.Budget)
		if row.Score != want {
			t.Errorf("row %q scored %g, want the best trace cost within the cap, %g", row.Job, row.Score, want)
		}

		if row.ScoredEvaluations > design.Budget {
			t.Errorf("row %q was scored at %d evaluations, above the cap of %d",
				row.Job, row.ScoredEvaluations, design.Budget)
		}

		if row.Engine == fitrun.EngineCMAES && row.Lambda <= 0 {
			t.Errorf("row %q is a cmaes row with no resolved lambda", row.Job)
		}

		if row.Engine == fitrun.EngineMayfly && row.Population <= 0 {
			t.Errorf("row %q is a mayfly row with no population", row.Job)
		}

		seeds[row.Block] = append(seeds[row.Block], row.Seed)
	}

	for block, blockSeeds := range seeds {
		if len(blockSeeds) != 2 || blockSeeds[0] != blockSeeds[1] {
			t.Errorf("block %d has seeds %v, but the arms of a block are paired on one seed", block, blockSeeds)
		}
	}

	// A second run has nothing to do: every job already wrote a summary.
	var again bytes.Buffer

	if err := campaign.Run(t.Context(), dir, &again, campaign.RunOptions{OnlyBlock: -1}); err != nil {
		t.Fatalf("re-run: %v", err)
	}

	if strings.Contains(again.String(), "score=") {
		t.Errorf("re-running the campaign searched again:\n%s", again.String())
	}

	if got := strings.Count(again.String(), "skipped"); got != len(manifest.Jobs) {
		t.Errorf("re-run skipped %d of %d jobs", got, len(manifest.Jobs))
	}
}

func TestCollectRefusesAnUnfinishedCampaign(t *testing.T) {
	dir, manifest := planSmoke(t)

	if _, err := campaign.Collect(dir, false); err == nil {
		t.Fatal("collecting a campaign with no finished jobs succeeded")
	} else if !strings.Contains(err.Error(), "unfinished") {
		t.Errorf("error %q does not say the campaign is unfinished", err)
	}

	path, err := campaign.Collect(dir, true)
	if err != nil {
		t.Fatalf("partial collect: %v", err)
	}

	rows, err := campaign.ReadResults(path)
	if err != nil {
		t.Fatalf("read results: %v", err)
	}

	if len(rows) != 0 {
		t.Fatalf("a partial collect of %d unfinished jobs produced %d rows", len(manifest.Jobs), len(rows))
	}
}

func TestCollectRefusesACancelledJob(t *testing.T) {
	dir, manifest := planSmoke(t)

	// Every job but the first is given an ordinary summary, so the only thing
	// standing between the campaign and a results file is the cancelled one.
	for index, job := range manifest.Jobs {
		jobDir := filepath.Join(dir, job.Dir)
		if err := os.MkdirAll(jobDir, 0o755); err != nil {
			t.Fatalf("create job directory: %v", err)
		}

		body := `{"score":0.5,"stop_reason":"max_evaluations"}`
		if index == 0 {
			body = `{"score":0.5,"stop_reason":"context_canceled"}`
		}

		if err := os.WriteFile(filepath.Join(jobDir, fitrun.FileResult), []byte(body), 0o644); err != nil {
			t.Fatalf("write summary: %v", err)
		}

		trace := `{"iteration":1,"optimizer_iterations":1,"restart":0,"evaluations":1,` +
			`"elapsed_ms":1,"current":0.5,"best":0.5}` + "\n"
		if err := os.WriteFile(filepath.Join(jobDir, fitrun.FileTrace), []byte(trace), 0o644); err != nil {
			t.Fatalf("write trace: %v", err)
		}
	}

	_, err := campaign.Collect(dir, false)
	if err == nil {
		t.Fatal("a campaign with a cancelled job collected cleanly")
	}

	if !strings.Contains(err.Error(), manifest.Jobs[0].ID) {
		t.Errorf("error %q does not name the cancelled job %q", err, manifest.Jobs[0].ID)
	}

	path, err := campaign.Collect(dir, true)
	if err != nil {
		t.Fatalf("partial collect: %v", err)
	}

	rows, err := campaign.ReadResults(path)
	if err != nil {
		t.Fatalf("read results: %v", err)
	}

	if len(rows) != len(manifest.Jobs)-1 {
		t.Fatalf("collected %d rows, want %d with the cancelled job left out", len(rows), len(manifest.Jobs)-1)
	}

	for _, row := range rows {
		if row.Job == manifest.Jobs[0].ID {
			t.Errorf("the cancelled job %q was collected anyway", row.Job)
		}
	}
}
