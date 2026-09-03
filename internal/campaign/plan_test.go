package campaign_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/campaign"
)

// planSmoke plans the smoke design into a fresh directory from the repository
// root, which is where the design's reference path resolves.
func planSmoke(t *testing.T) (string, *campaign.Manifest) {
	t.Helper()
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

	return dir, manifest
}

func TestPlanRefusesToOverwriteAManifest(t *testing.T) {
	dir, _ := planSmoke(t)

	design, err := campaign.Lookup("smoke")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	_, err = campaign.Plan(design, dir, 2)
	if err == nil {
		t.Fatal("planning twice into the same directory succeeded")
	}

	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error %q does not say the campaign already exists", err)
	}
}

func TestPlanOrdersJobsBlockMajor(t *testing.T) {
	_, manifest := planSmoke(t)

	design := manifest.Design

	if len(manifest.Jobs) != design.Blocks*len(design.Arms) {
		t.Fatalf("planned %d jobs, want %d", len(manifest.Jobs), design.Blocks*len(design.Arms))
	}

	for index, job := range manifest.Jobs {
		wantBlock := index / len(design.Arms)
		wantArm := design.Arms[index%len(design.Arms)].Name

		if job.Block != wantBlock || job.Arm != wantArm {
			t.Fatalf("job %d is block %d arm %q, want block %d arm %q",
				index, job.Block, job.Arm, wantBlock, wantArm)
		}

		wantID := fmt.Sprintf("b%02d-%s", wantBlock, wantArm)
		if job.ID != wantID {
			t.Errorf("job %d has id %q, want %q", index, job.ID, wantID)
		}

		wantDir := filepath.Join("jobs", fmt.Sprintf("b%02d", wantBlock), wantArm)
		if job.Dir != wantDir {
			t.Errorf("job %d has directory %q, want %q", index, job.Dir, wantDir)
		}
	}
}

func TestPlanSeedsArePairedAcrossArms(t *testing.T) {
	_, manifest := planSmoke(t)

	seeds := make(map[int]int64)

	for _, job := range manifest.Jobs {
		want := manifest.Design.SeedBase + int64(job.Block)
		if job.Seed != want {
			t.Errorf("job %q has seed %d, want %d", job.ID, job.Seed, want)
		}

		if seen, ok := seeds[job.Block]; ok && seen != job.Seed {
			t.Errorf("block %d has two seeds, %d and %d, so its arms are not paired", job.Block, seen, job.Seed)
		}

		seeds[job.Block] = job.Seed
	}

	if len(seeds) != manifest.Design.Blocks {
		t.Fatalf("saw %d blocks, want %d", len(seeds), manifest.Design.Blocks)
	}

	if seeds[0] == seeds[1] {
		t.Errorf("blocks 0 and 1 share seed %d, so the two blocks repeat one search", seeds[0])
	}
}

func TestPlanRecordsTheDesignAndItsHash(t *testing.T) {
	dir, manifest := planSmoke(t)

	hash, err := manifest.Design.Hash()
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if manifest.DesignHash != hash {
		t.Errorf("manifest hash %q does not match the design it carries (%q)", manifest.DesignHash, hash)
	}

	raw, err := os.ReadFile(filepath.Join(dir, campaign.FileManifest))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	var written campaign.Manifest

	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	if written.DesignHash != manifest.DesignHash || len(written.Jobs) != len(manifest.Jobs) {
		t.Errorf("the manifest on disk is not the one Plan returned")
	}

	if written.Reference.SHA256 == "" || written.Binary.SHA256 == "" {
		t.Error("the manifest does not pin both the reference and the binary")
	}
}

func TestPlanTableNamesEveryArm(t *testing.T) {
	_, manifest := planSmoke(t)

	table := campaign.PlanTable(manifest)
	for _, arm := range manifest.Design.Arms {
		if !strings.Contains(table, arm.Name) {
			t.Errorf("the plan table does not mention arm %q", arm.Name)
		}
	}
}

func TestPlanFixesTheWorkerWidth(t *testing.T) {
	t.Chdir(repoRoot(t))

	design, err := campaign.Lookup("smoke")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	manifest, err := campaign.Plan(design, filepath.Join(t.TempDir(), "campaign"), 0)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	if manifest.Workers != runtime.NumCPU() {
		t.Errorf("a plan with no worker count recorded %d workers, want %d", manifest.Workers, runtime.NumCPU())
	}

	if _, err := campaign.Plan(design, filepath.Join(t.TempDir(), "campaign"), -1); err == nil {
		t.Error("a negative worker count was accepted")
	}
}
