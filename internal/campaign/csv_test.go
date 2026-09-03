package campaign_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/campaign"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// sampleRows are one cmaes row and one mayfly row, so the round trip covers
// both sets of engine-specific columns.
func sampleRows() []campaign.Row {
	terms := make(map[optimizer.Term]float64, len(optimizer.Terms()))
	for index, term := range optimizer.Terms() {
		terms[term] = 0.1 * float64(index+1)
	}

	return []campaign.Row{
		{
			Design: "engine-shape", Arm: "blk-cmaes-r", Block: 0, Seed: 121_000, Job: "b00-blk-cmaes-r",
			Engine: "cmaes", Covariance: "block", Lambda: 14, RestartsPlanned: 0, Budget: 24_000,
			Score: 0.26179114159362793, ScoredEvaluations: 23_988, FinalScore: 0.2617911415936,
			Evaluations: 24_003, Iterations: 900, Restarts: 5, StopReason: "max evaluations",
			Converged: false, ElapsedS: 61.25, Pinned: 2, Dimension: 30, Matched: 8,
			Terms: terms, MayflyVersion: "v0.7.1", CMAESVersion: "v0.1.0", Revision: "abc123",
		},
		{
			Design: "engine-shape", Arm: "mayfly-r16", Block: 1, Seed: 121_001, Job: "b01-mayfly-r16",
			Engine: "mayfly", Population: 10, RestartsPlanned: 16, Budget: 24_000,
			Score: 0.31, ScoredEvaluations: 23_900, FinalScore: 0.31,
			Evaluations: 24_010, Iterations: 558, Restarts: 15, StopReason: "max iterations",
			Converged: true, ElapsedS: 59.5, Pinned: 2, Dimension: 30, Matched: 7,
			Terms: terms, MayflyVersion: "v0.7.1", CMAESVersion: "v0.1.0", Revision: "abc123",
		},
	}
}

func TestResultsRoundTripThroughCSV(t *testing.T) {
	path := filepath.Join(t.TempDir(), campaign.FileResults)

	want := sampleRows()
	if err := campaign.WriteResults(path, want); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := campaign.ReadResults(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip changed the rows:\n got %+v\nwant %+v", got, want)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	header := strings.SplitN(string(raw), "\n", 2)[0]
	if header != strings.Join(campaign.Header(), ",") {
		t.Errorf("header is %q, want %q", header, strings.Join(campaign.Header(), ","))
	}
}

func TestReadResultsLatchesTheFirstParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), campaign.FileResults)

	if err := campaign.WriteResults(path, sampleRows()); err != nil {
		t.Fatalf("write: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("wrote %d lines, want a header and two rows", len(lines))
	}

	// Two cells of the first row are broken. The first is what the error has
	// to name: a reader that reported the last one, or none at all, would let
	// a hand-edited file through as zeros.
	fields := strings.Split(lines[1], ",")
	fields[11] = "not-a-number"
	fields[19] = "also-not-a-number"
	lines[1] = strings.Join(fields, ",")

	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	rows, err := campaign.ReadResults(path)
	if err == nil {
		t.Fatalf("a hand-edited results file parsed cleanly into %d rows", len(rows))
	}

	if !strings.Contains(err.Error(), "score") || !strings.Contains(err.Error(), "not-a-number") {
		t.Errorf("error %q does not name the first broken cell", err)
	}

	if strings.Contains(err.Error(), "also-not-a-number") {
		t.Errorf("error %q reports a later cell than the first failure", err)
	}
}

func TestReadResultsRefusesAnUnexpectedHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), campaign.FileResults)

	header := campaign.Header()
	header[3] = "random_seed"

	body := strings.Join(header, ",") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := campaign.ReadResults(path); err == nil {
		t.Fatal("a results file with a renamed column was accepted")
	}
}

// contractHeader is results.csv's header as the phase's constraints fix it. It
// is written out in full rather than derived from campaign.Header, because the
// point of the check is that neither an added optimizer term nor a reordering
// of the ten term columns can change the file format without a failing test
// and a deliberate edit here.
const contractHeader = "design,arm,block,seed,job,engine,covariance,lambda,population,restarts_planned," +
	"budget,score,scored_evaluations,final_score,evaluations,iterations,restarts,stop_reason,converged," +
	"elapsed_s,pinned,dimension,matched,partial_cents,partial_level_db,partial_decay_octaves," +
	"partial_missing,partial_extra,spectral_fine_db,spectral_coarse_db,envelope_db,decay_slope_dbps," +
	"waveform,mayfly_version,cmaes_version,revision"

func TestHeaderIsTheContractsColumnSet(t *testing.T) {
	if got := strings.Join(campaign.Header(), ","); got != contractHeader {
		t.Fatalf("results header is\n %s\nwant\n %s", got, contractHeader)
	}

	path := filepath.Join(t.TempDir(), campaign.FileResults)

	if err := campaign.WriteResults(path, sampleRows()); err != nil {
		t.Fatalf("write: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	if got := strings.SplitN(string(raw), "\n", 2)[0]; got != contractHeader {
		t.Fatalf("the written header is %q", got)
	}
}
