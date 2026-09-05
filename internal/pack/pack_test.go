package pack_test

import (
	"context"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/pack"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
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

// TestFitJointProducesOnePresetForEveryNote walks the joint fit end to end at a
// budget small enough for a test, and asserts the three things that make it a
// joint fit rather than a single-note one: one preset, a per-note render beside
// each per-note recording, and no top-level render.wav that could be mistaken
// for the fit's own.
func TestFitJointProducesOnePresetForEveryNote(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a joint fit")
	}

	_, runDir := planTwoNotes(t, 200)

	outDir := filepath.Join(t.TempDir(), "joint")

	outcome, fitted, err := pack.FitJoint(context.Background(), runDir, outDir, nil, pack.JointOptions{
		Budget: 400,
		Seed:   7,
	})
	if err != nil {
		t.Fatalf("FitJoint: %v", err)
	}

	if len(fitted) != 2 || fitted[0] != 84 || fitted[1] != 103 {
		t.Fatalf("fitted notes %v, want [84 103]", fitted)
	}

	// The authored note defaults to the median, which for two notes is the
	// upper of the pair.
	if outcome.Preset.Note != 103 {
		t.Errorf("preset authored at note %d, want the median 103", outcome.Preset.Note)
	}

	for _, note := range fitted {
		for _, name := range []string{"analysis.json", "reference.wav", "render.wav"} {
			path := filepath.Join(outDir, "notes", fmt.Sprintf("%03d", note), name)
			if _, err := os.Stat(path); err != nil {
				t.Errorf("note %d has no %s: %v", note, name, err)
			}
		}
	}

	// Nothing at the top level that promises to be the fit's own reference or
	// render: a consumer reading those by name would compare one note's
	// recording against a two-note fit and see nothing wrong.
	for _, name := range []string{"reference.wav", "render.wav", "analysis.json"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err == nil {
			t.Errorf("a multi-note run wrote a top-level %s", name)
		}
	}

	if _, err := os.Stat(filepath.Join(outDir, "preset.json")); err != nil {
		t.Errorf("the joint fit wrote no preset: %v", err)
	}
}

// TestFitJointRefusesASingleNote keeps the command honest: a "joint" fit over
// one note is a single-note fit under another name, and would silently accept a
// subset that matched nothing but one recording.
func TestFitJointRefusesASingleNote(t *testing.T) {
	_, runDir := planTwoNotes(t, 200)

	_, _, err := pack.FitJoint(context.Background(), runDir, filepath.Join(t.TempDir(), "j"), nil,
		pack.JointOptions{Budget: 100, Notes: []int{84}})
	if err == nil {
		t.Fatal("a joint fit over one note was accepted")
	}

	if !strings.Contains(err.Error(), "single-note") {
		t.Errorf("the error does not say what it is: %v", err)
	}
}

// TestPerNoteFitIsAuthoredAtItsOwnNote is the property the whole step-1 table
// depends on, and it was wrong until it was measured.
//
// PresetFromAnalysis expresses every measured partial at the *template's* note,
// so without an explicit authored note a fit of a C6 recording under the
// embedded note-69 template writes a preset whose fundamental reads 439.7 Hz
// rather than 1046. The preset renders correctly, because transposition puts
// the ratio back, which is exactly why nothing downstream would have complained
// -- but the frequency and decay columns of pack-modes.csv would have been the
// note-69 equivalents, and a regression of log2(decay) against pitch would have
// come out a whole exponent off.
func TestPerNoteFitIsAuthoredAtItsOwnNote(t *testing.T) {
	if testing.Short() {
		t.Skip("fits two notes")
	}

	_, runDir := planTwoNotes(t, 200)

	if err := pack.Run(context.Background(), runDir, nil, pack.RunOptions{}); err != nil {
		t.Fatalf("run: %v", err)
	}

	manifest, err := pack.ReadManifest(runDir)
	if err != nil {
		t.Fatal(err)
	}

	for _, job := range manifest.Jobs {
		fitted, err := preset.Load(filepath.Join(runDir, job.Dir, "preset.json"))
		if err != nil {
			t.Fatalf("note %s: %v", job.Name, err)
		}

		if fitted.Note != job.Note {
			t.Errorf("note %s: preset authored at %d, want the bar's own note %d",
				job.Name, fitted.Note, job.Note)
		}

		// And the numbers really are the bar's: the lowest fitted mode sits
		// within a semitone of the recording's measured fundamental.
		lowest := math.Inf(1)
		for _, mode := range fitted.Parameters.Modes {
			lowest = math.Min(lowest, mode.Frequency)
		}

		measured := 440 * math.Pow(2, float64(job.Note-69)/12)
		if cents := 1200 * math.Log2(lowest/measured); math.Abs(cents) > 100 {
			t.Errorf("note %s: lowest fitted mode is %.1f Hz, %.0f cents from the bar's %.1f Hz",
				job.Name, lowest, cents, measured)
		}
	}
}
