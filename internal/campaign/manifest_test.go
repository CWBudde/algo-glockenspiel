package campaign_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/campaign"
)

func TestReadManifestRefusesAJobDirectoryOutsideTheCampaign(t *testing.T) {
	dir := t.TempDir()

	raw := []byte(`{"design":{"name":"smoke"},"jobs":[{"id":"b00-x","arm":"x","block":0,"seed":1,"dir":"../../escape"}]}`)
	if err := os.WriteFile(filepath.Join(dir, campaign.FileManifest), raw, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	if _, err := campaign.ReadManifest(dir); err == nil || !strings.Contains(err.Error(), "outside the campaign") {
		t.Fatalf("expected an escape error, got %v", err)
	}
}

// TestReadManifestRefusesAnEditedDesign pins the hash check. The manifest is
// the frozen record of what was planned; a design block edited after the fact
// would change what the jobs are taken to have run under while every result
// still carried the old hash, and nothing else would notice.
func TestReadManifestRefusesAnEditedDesign(t *testing.T) {
	dir, _ := planSmoke(t)
	path := filepath.Join(dir, campaign.FileManifest)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	// The budget, because it is what the collect step scores against: an
	// edit here would rescore every job.
	edited := strings.Replace(string(raw), `"budget": 1200`, `"budget": 900`, 1)
	if edited == string(raw) {
		t.Fatalf("the manifest does not hold the budget the test edits:\n%s", raw)
	}

	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, err = campaign.ReadManifest(dir)
	if err == nil {
		t.Fatal("reading a manifest whose design was edited succeeded")
	}

	if !strings.Contains(err.Error(), "edited after planning") {
		t.Errorf("error %q does not say the design was edited", err)
	}
}
