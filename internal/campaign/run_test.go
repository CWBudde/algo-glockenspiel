package campaign_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/campaign"
	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
)

func TestRunRefusesADifferentBinary(t *testing.T) {
	dir, _ := planSmoke(t)

	path := filepath.Join(dir, campaign.FileManifest)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	var document map[string]any

	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	binary, ok := document["binary"].(map[string]any)
	if !ok {
		t.Fatal("the manifest has no binary block")
	}

	planned, ok := binary["sha256"].(string)
	if !ok {
		t.Fatal("the manifest's binary block has no hash")
	}

	binary["sha256"] = strings.Repeat("0", len(planned))

	edited, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}

	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	err = campaign.Run(t.Context(), dir, nil, campaign.RunOptions{OnlyBlock: -1})
	if err == nil {
		t.Fatal("a campaign planned by another binary ran anyway")
	}

	if !strings.Contains(err.Error(), planned) || !strings.Contains(err.Error(), strings.Repeat("0", len(planned))) {
		t.Errorf("error %q does not name both the running hash and the manifest's", err)
	}
}

func TestRunRefusesAChangedReference(t *testing.T) {
	t.Chdir(repoRoot(t))

	dir := filepath.Join(t.TempDir(), "campaign")

	design, err := campaign.Lookup("smoke")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	// The reference is copied so it can be edited without touching the
	// repository's own testdata.
	source, err := os.ReadFile(design.Reference)
	if err != nil {
		t.Fatalf("read reference: %v", err)
	}

	copied := filepath.Join(t.TempDir(), "reference.wav")
	if err := os.WriteFile(copied, source, 0o644); err != nil {
		t.Fatalf("write reference: %v", err)
	}

	design.Reference = copied

	if _, err := campaign.Plan(design, dir, 1); err != nil {
		t.Fatalf("plan: %v", err)
	}

	if err := os.WriteFile(copied, append(source, 0), 0o644); err != nil {
		t.Fatalf("edit reference: %v", err)
	}

	err = campaign.Run(t.Context(), dir, nil, campaign.RunOptions{OnlyBlock: -1})
	if err == nil {
		t.Fatal("a campaign whose reference changed ran anyway")
	}

	if !strings.Contains(err.Error(), "reference has changed") {
		t.Errorf("error %q does not say the reference changed", err)
	}
}

func TestRunSkipsFinishedJobs(t *testing.T) {
	dir, manifest := planSmoke(t)

	// Every job of block zero is given a summary, so a run restricted to that
	// block has nothing left to do and finishes without searching.
	for _, job := range manifest.Jobs {
		if job.Block != 0 {
			continue
		}

		jobDir := filepath.Join(dir, job.Dir)
		if err := os.MkdirAll(jobDir, 0o755); err != nil {
			t.Fatalf("create job directory: %v", err)
		}

		if err := os.WriteFile(filepath.Join(jobDir, fitrun.FileResult), []byte("{}"), 0o644); err != nil {
			t.Fatalf("write result: %v", err)
		}
	}

	var log bytes.Buffer

	if err := campaign.Run(t.Context(), dir, &log, campaign.RunOptions{OnlyBlock: 0}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := strings.Count(log.String(), "skipped"); got != 2 {
		t.Fatalf("log reports %d skipped jobs, want 2:\n%s", got, log.String())
	}

	for _, job := range manifest.Jobs {
		if job.Block != 0 {
			continue
		}

		if _, err := os.Stat(filepath.Join(dir, job.Dir, fitrun.FileConfig)); err == nil {
			t.Errorf("job %q was re-run: it wrote a config", job.ID)
		}
	}
}

func TestRunStopsOnACancelledContext(t *testing.T) {
	dir, _ := planSmoke(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := campaign.Run(ctx, dir, nil, campaign.RunOptions{OnlyBlock: -1})
	if err == nil {
		t.Fatal("a cancelled campaign returned no error")
	}

	if !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Errorf("error %q is not the context's", err)
	}
}

func TestRunHonoursTheJobLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("the limit is only observable by running a job")
	}

	dir, manifest := planSmoke(t)

	var log bytes.Buffer

	if err := campaign.Run(t.Context(), dir, &log, campaign.RunOptions{Limit: 1, OnlyBlock: -1}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := strings.Count(log.String(), "score="); got != 1 {
		t.Fatalf("a limit of one ran %d jobs:\n%s", got, log.String())
	}

	for _, job := range manifest.Jobs[1:] {
		if _, err := os.Stat(filepath.Join(dir, job.Dir)); err == nil {
			t.Errorf("job %q ran past the limit", job.ID)
		}
	}
}

// cancelledSummary is what fitrun leaves behind when a context cancellation
// ends a run: a complete result.json whose stop reason says the run was cut
// short.
const cancelledSummary = `{"score":0.5,"stop_reason":"context_canceled","evaluations":17}`

func TestRunRepeatsAJobTheCancellationCut(t *testing.T) {
	if testing.Short() {
		t.Skip("repeating a cancelled job means running it")
	}

	dir, manifest := planSmoke(t)

	job := manifest.Jobs[0]
	jobDir := filepath.Join(dir, job.Dir)

	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatalf("create job directory: %v", err)
	}

	if err := os.WriteFile(filepath.Join(jobDir, fitrun.FileResult), []byte(cancelledSummary), 0o644); err != nil {
		t.Fatalf("write summary: %v", err)
	}

	// A stale trace with an unmistakable cost: collect scores from this file,
	// so it must not survive the repeat.
	stale := `{"iteration":1,"optimizer_iterations":1,"restart":0,"evaluations":1,"elapsed_ms":1,` +
		`"current":-12345,"best":-12345}` + "\n"
	if err := os.WriteFile(filepath.Join(jobDir, fitrun.FileTrace), []byte(stale), 0o644); err != nil {
		t.Fatalf("write trace: %v", err)
	}

	var log bytes.Buffer

	if err := campaign.Run(t.Context(), dir, &log, campaign.RunOptions{Limit: 1, OnlyBlock: -1}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.Contains(log.String(), "score=") {
		t.Fatalf("the cancelled job was not repeated:\n%s", log.String())
	}

	if strings.Contains(log.String(), "skipped") {
		t.Errorf("the cancelled job was skipped:\n%s", log.String())
	}

	raw, err := os.ReadFile(filepath.Join(jobDir, fitrun.FileResult))
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}

	var summary fitrun.Summary

	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatalf("parse summary: %v", err)
	}

	if summary.StopReason == "context_canceled" {
		t.Errorf("the repeated job still reports stop reason %q", summary.StopReason)
	}

	trace, err := os.ReadFile(filepath.Join(jobDir, fitrun.FileTrace))
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}

	if strings.Contains(string(trace), "-12345") {
		t.Error("the cancelled run's trace survived the repeat, so collect would score the wrong run")
	}
}
