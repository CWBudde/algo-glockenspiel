package pack_test

import (
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/pack"
)

// hollandm is the pack this phase exists to fit: twenty chromatic semitones,
// the only complete run in testdata/reference/packs.
const hollandm = "../../testdata/reference/packs/hollandm-toy-glockenspiel"

// planTwoNotes plans a run over a two-note copy of the real pack. Copying keeps
// the measurement real -- the notes resolve from their own audio, which is the
// behaviour worth testing -- while keeping the fit short enough for a unit test.
func planTwoNotes(t *testing.T, budget int) (packDir, runDir string) {
	t.Helper()

	packDir = filepath.Join(t.TempDir(), "pack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"c6.wav", "g7.wav"} {
		raw, err := os.ReadFile(filepath.Join(hollandm, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}

		if err := os.WriteFile(filepath.Join(packDir, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	runDir = filepath.Join(t.TempDir(), "run")

	if _, err := pack.Plan(packDir, runDir, pack.Options{Budget: budget, SeedBase: 900_000}); err != nil {
		t.Fatalf("plan: %v", err)
	}

	return packDir, runDir
}

// TestPlanResolvesNotesByMeasurementAndOrdersThem pins the two properties the
// whole harness rests on: a note comes from the recording rather than the file
// name, and the jobs run low to high so a listing reads in pitch order.
func TestPlanResolvesNotesByMeasurementAndOrdersThem(t *testing.T) {
	_, runDir := planTwoNotes(t, 200)

	manifest, err := pack.ReadManifest(runDir)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	if len(manifest.Jobs) != 2 {
		t.Fatalf("planned %d notes, want 2", len(manifest.Jobs))
	}

	// c6 is MIDI 84 and g7 is 103, both measured rather than parsed.
	if got := manifest.Jobs[0].Note; got != 84 {
		t.Errorf("first note is MIDI %d, want 84", got)
	}

	if got := manifest.Jobs[1].Note; got != 103 {
		t.Errorf("second note is MIDI %d, want 103", got)
	}

	if manifest.Jobs[0].Seed == manifest.Jobs[1].Seed {
		t.Error("both notes were given the same seed, so the two fits share a random stream")
	}

	// Every recording is pinned by content, not by path.
	for _, job := range manifest.Jobs {
		if len(job.Reference.SHA256) != 64 {
			t.Errorf("note %s carries no content hash: %q", job.Name, job.Reference.SHA256)
		}
	}

	if manifest.Profile != optimizer.MetricBalanced {
		t.Errorf("profile is %q, want the balanced default", manifest.Profile)
	}
}

// TestPlanRefusesASecondPlanInTheSameDirectory is the O_EXCL rule campaign
// established: a second plan beside the first plan's run directories would
// leave a set of results paired with a plan that no longer describes them.
func TestPlanRefusesASecondPlanInTheSameDirectory(t *testing.T) {
	packDir, runDir := planTwoNotes(t, 200)

	_, err := pack.Plan(packDir, runDir, pack.Options{Budget: 200, SeedBase: 900_000})
	if err == nil {
		t.Fatal("a second plan into the same directory was accepted")
	}

	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("the error does not say why: %v", err)
	}
}

// TestPlanRefusesAMislabelledPack is the Freesound trap at the level of the
// whole directory rather than of one name.
func TestPlanRefusesAMislabelledPack(t *testing.T) {
	packDir := filepath.Join(t.TempDir(), "pack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// cs6.wav copied under the name of its natural, which is exactly how the
	// pack arrived from Freesound before it was measured and renamed.
	raw, err := os.ReadFile(filepath.Join(hollandm, "cs6.wav"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(packDir, "c6.wav"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = pack.Plan(packDir, filepath.Join(t.TempDir(), "run"), pack.Options{Budget: 200, SeedBase: 1})
	if err == nil {
		t.Fatal("a pack whose file name and pitch disagree was planned")
	}

	if !strings.Contains(err.Error(), "cs6") {
		t.Errorf("the error does not name the pitch that was measured: %v", err)
	}
}

// TestRunAndCollectProduceBothTables walks the whole harness at a budget small
// enough for a test, and asserts what the tables are for: one row per note in
// the wide one, one row per fitted mode in the long one, and a mode index that
// really is ordered by frequency so a regression over it means something.
func TestRunAndCollectProduceBothTables(t *testing.T) {
	if testing.Short() {
		t.Skip("fits two notes")
	}

	_, runDir := planTwoNotes(t, 400)

	if err := pack.Run(context.Background(), runDir, nil, pack.RunOptions{}); err != nil {
		t.Fatalf("run: %v", err)
	}

	notes, err := pack.Collect(runDir)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	if notes != 2 {
		t.Fatalf("collected %d notes, want 2", notes)
	}

	wide := readCSV(t, filepath.Join(runDir, pack.FileResults))
	if len(wide) != 3 {
		t.Fatalf("wide table holds %d lines, want a header and two notes", len(wide))
	}

	head := wide[0]
	for _, want := range []string{"note", "cents_off", "score", "measured_weight", "partial_cents", "waveform"} {
		if !contains(head, want) {
			t.Errorf("wide header has no %q column: %v", want, head)
		}
	}

	long := readCSV(t, filepath.Join(runDir, pack.FileModeResults))
	if len(long) < 3 {
		t.Fatalf("long table holds %d lines, want a header and at least one mode per note", len(long))
	}

	// mode 0 is the lowest mode of its note, so its ratio to the fundamental is
	// exactly 1 and every later mode is above it. That ordering is what makes
	// "mode k" the same thing at every note.
	index := columnOf(t, long[0], "mode_index")
	ratio := columnOf(t, long[0], "ratio_to_fundamental")

	for _, row := range long[1:] {
		if row[index] == "0" && row[ratio] != "1" {
			t.Errorf("mode 0 has ratio %q, want exactly 1", row[ratio])
		}
	}
}

// TestRunSkipsAFinishedNote is the resume rule: an interrupted run continues
// rather than repeating work, and a second run is therefore free.
func TestRunSkipsAFinishedNote(t *testing.T) {
	if testing.Short() {
		t.Skip("fits a note")
	}

	_, runDir := planTwoNotes(t, 200)

	if err := pack.Run(context.Background(), runDir, nil, pack.RunOptions{Limit: 1}); err != nil {
		t.Fatalf("first run: %v", err)
	}

	manifest, err := pack.ReadManifest(runDir)
	if err != nil {
		t.Fatal(err)
	}

	first := filepath.Join(runDir, manifest.Jobs[0].Dir, "result.json")

	before, err := os.Stat(first)
	if err != nil {
		t.Fatalf("the first note left no result: %v", err)
	}

	// Collect must refuse a half-finished run rather than write a table of it.
	if _, err := pack.Collect(runDir); err == nil {
		t.Error("collect accepted a run with an unfitted note")
	}

	if err := pack.Run(context.Background(), runDir, nil, pack.RunOptions{}); err != nil {
		t.Fatalf("second run: %v", err)
	}

	after, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}

	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("the finished note was fitted again instead of being skipped")
	}
}

func readCSV(t *testing.T, path string) [][]string {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %q: %v", path, err)
	}

	defer func() { _ = file.Close() }()

	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}

	return rows
}

func contains(row []string, want string) bool {
	for _, cell := range row {
		if cell == want {
			return true
		}
	}

	return false
}

func columnOf(t *testing.T, header []string, name string) int {
	t.Helper()

	for i, cell := range header {
		if cell == name {
			return i
		}
	}

	t.Fatalf("header has no %q column: %v", name, header)

	return -1
}
