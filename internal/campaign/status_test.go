package campaign_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/campaign"
	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
)

// writeSummary gives a job the result file ReadStatus classifies it by.
func writeSummary(t *testing.T, dir string, summary fitrun.Summary) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create job directory: %v", err)
	}

	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, fitrun.FileResult), raw, 0o644); err != nil {
		t.Fatalf("write result: %v", err)
	}
}

// TestReadStatusCountsFinishedJobsAndEstimatesTheRest is the case status
// exists for: a campaign part way through, read from another shell.
func TestReadStatusCountsFinishedJobsAndEstimatesTheRest(t *testing.T) {
	dir, manifest := planSmoke(t)

	// Block zero finishes, at two different scores so the best-so-far column
	// has something to choose between.
	for i, job := range manifest.Jobs {
		if job.Block != 0 {
			continue
		}

		writeSummary(t, filepath.Join(dir, job.Dir), fitrun.Summary{
			Score:          0.5 - float64(i)/100,
			StopReason:     "max_evaluations",
			ElapsedSeconds: 10,
		})
	}

	status, err := campaign.ReadStatus(dir)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}

	if status.Finished != 2 || status.Pending != 2 {
		t.Fatalf("status reports %d finished and %d pending, want 2 and 2", status.Finished, status.Pending)
	}

	if status.Total != len(manifest.Jobs) {
		t.Errorf("status reports %d jobs, the manifest holds %d", status.Total, len(manifest.Jobs))
	}

	// Two jobs of ten seconds each, two left: twenty seconds.
	if got := status.Remaining.Seconds(); got != 20 {
		t.Errorf("status estimates %.0fs left, want 20s from a ten second mean", got)
	}

	rendered := campaign.RenderStatus(status)
	if !strings.Contains(rendered, "2 of 4 jobs finished") {
		t.Errorf("rendered status does not say where the campaign is:\n%s", rendered)
	}

	for _, arm := range manifest.Design.Arms {
		if !strings.Contains(rendered, arm.Name) {
			t.Errorf("rendered status omits arm %q:\n%s", arm.Name, rendered)
		}
	}
}

// TestReadStatusCountsACancelledJobAsPending pins the rule status shares with
// run: a job a cancellation cut is not finished, whatever its result says, and
// the next run repeats it.
func TestReadStatusCountsACancelledJobAsPending(t *testing.T) {
	dir, manifest := planSmoke(t)

	writeSummary(t, filepath.Join(dir, manifest.Jobs[0].Dir), fitrun.Summary{
		Score:          0.5,
		StopReason:     "context_canceled",
		ElapsedSeconds: 3,
	})

	status, err := campaign.ReadStatus(dir)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}

	if status.Finished != 0 {
		t.Errorf("status counts %d jobs finished, want none: the only one was cancelled", status.Finished)
	}

	if status.Canceled != 1 || status.Pending != len(manifest.Jobs) {
		t.Errorf("status reports %d cancelled and %d pending, want 1 and %d",
			status.Canceled, status.Pending, len(manifest.Jobs))
	}

	if !strings.Contains(campaign.RenderStatus(status), "will be repeated") {
		t.Error("rendered status does not say the cancelled job will be repeated")
	}
}

// TestReadStatusNamesTheJobInFlight pins how a running job is recognised: a
// config written, no result yet.
func TestReadStatusNamesTheJobInFlight(t *testing.T) {
	dir, manifest := planSmoke(t)

	running := manifest.Jobs[1]

	jobDir := filepath.Join(dir, running.Dir)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("create job directory: %v", err)
	}

	if err := os.WriteFile(filepath.Join(jobDir, fitrun.FileConfig), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	status, err := campaign.ReadStatus(dir)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}

	if status.InFlight != running.ID {
		t.Errorf("status names %q in flight, want %q", status.InFlight, running.ID)
	}
}
