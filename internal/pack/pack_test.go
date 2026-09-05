package pack_test

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/pack"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/model"
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

	// The ratio is against the note's fundamental, not against the note's own
	// lowest fitted mode. The difference is the whole comparability of the
	// table: a ratio of 1 for mode 0 would mean the denominator moved with the
	// fit, and two notes' "mode 0" rows would then denote different partials.
	index := columnOf(t, long[0], "mode_index")
	ratio := columnOf(t, long[0], "ratio_to_fundamental")
	hz := columnOf(t, long[0], "frequency_hz")
	noteAt := columnOf(t, long[0], "note")

	// Ascending order within each note is what makes "mode k" the k-th partial
	// everywhere, and it holds under either denominator -- so it is asserted
	// here rather than inferred from a ratio of 1.
	previous := map[string]float64{}

	for _, row := range long[1:] {
		frequency := parseFloat(t, row[hz])
		got := parseFloat(t, row[ratio])

		fundamental := frequency / got
		if fundamental < model.FrequencyMinHz || fundamental > model.FrequencyMaxHz {
			t.Errorf("note %s mode %s implies a fundamental of %g Hz", row[noteAt], row[index], fundamental)
		}

		if row[index] == "0" && got == 1 {
			t.Errorf("note %s mode 0 has ratio exactly 1, which is the lowest mode dividing itself",
				row[noteAt])
		}

		if last, seen := previous[row[noteAt]]; seen && frequency <= last {
			t.Errorf("note %s mode %s is at %g Hz, not above the mode before it at %g",
				row[noteAt], row[index], frequency, last)
		}

		previous[row[noteAt]] = frequency
	}
}

func parseFloat(t *testing.T, text string) float64 {
	t.Helper()

	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}

	return value
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

// TestTheMatrixScoresThePresetItWasGiven pins the property ScorePresets rests
// on: nothing it scores is clamped on the way in.
//
// optimizer.Evaluate scores the decoded vector and DecodeParams clamps into the
// codec's box, so a preset carrying a mode outside that box would be scored as
// a different preset -- silently, with a number that looks like every other
// number in the matrix. The non-strict codec widens its box until it contains
// the template, and ScorePresets passes the candidate as its own template, so
// the encode/decode round trip is exact. Setting StrictBounds there would break
// this and nothing else would say so.
func TestTheMatrixScoresThePresetItWasGiven(t *testing.T) {
	fitted, err := preset.Load(filepath.Join("..", "..", "assets", "presets", "default.json"))
	if err != nil {
		t.Fatalf("load the default preset: %v", err)
	}

	// A mode deliberately outside the box on both axes it can leave. The search
	// frequency ceiling is 20 kHz and the search decay ceiling is 2 s, narrowed
	// further by the authored note; the model's own limits are far wider, so
	// these are values a preset can legally carry and the codec cannot hold.
	//
	// Both matter and the decay is the one that bites in practice: the ceiling
	// is narrowed per authored note, so a preset authored near the top of the
	// keyboard has a box under a second and a long-tailed bar leaves it easily.
	last := &fitted.Parameters.Modes[len(fitted.Parameters.Modes)-1]
	last.Frequency = 25000
	last.DecayMs = 3000

	config := optimizer.DefaultObjectiveConfig(optimizer.MetricBalanced)
	config.Bounds = optimizer.DefaultParamBounds

	objective, err := optimizer.NewObjectiveFunctionWithConfig(
		make([]float32, 44100), fitted, 44100, fitted.Note, 100, config)
	if err != nil {
		t.Fatalf("build objective: %v", err)
	}

	// The same preset under a strict codec, which is what ScorePresets would be
	// doing if StrictBounds were ever set there. It must clamp, or the values
	// above are inside the box after all and this test proves nothing.
	strict := config
	strict.StrictBounds = true

	strictObjective, err := optimizer.NewObjectiveFunctionWithConfig(
		make([]float32, 44100), fitted, 44100, fitted.Note, 100, strict)
	if err != nil {
		t.Fatalf("build the strict objective: %v", err)
	}

	strictEncoded, err := strictObjective.Codec().EncodeParams(&fitted.Parameters)
	if err != nil {
		t.Fatalf("strict encode: %v", err)
	}

	strictDecoded, err := strictObjective.Codec().DecodeParams(strictEncoded)
	if err != nil {
		t.Fatalf("strict decode: %v", err)
	}

	clamped := false

	for i, want := range fitted.Parameters.Modes {
		if strictDecoded.Modes[i].Frequency != want.Frequency || strictDecoded.Modes[i].DecayMs != want.DecayMs {
			clamped = true
		}
	}

	if !clamped {
		t.Fatal("the strict codec changed nothing, so the mode is inside the box and this test is vacuous")
	}

	encoded, err := objective.Codec().EncodeParams(&fitted.Parameters)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := objective.Codec().DecodeParams(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// EncodeParams sorts the modes ascending by frequency to kill the
	// permutation symmetry, and default.json does not store them that way, so
	// the comparison has to be against the same order the codec used.
	wanted := append([]model.ModeParams(nil), fitted.Parameters.Modes...)
	sort.Slice(wanted, func(i, j int) bool { return wanted[i].Frequency < wanted[j].Frequency })

	for i, want := range wanted {
		got := decoded.Modes[i]

		for _, pair := range []struct {
			name     string
			got, exp float64
		}{
			{"frequency", got.Frequency, want.Frequency},
			{"decay", got.DecayMs, want.DecayMs},
			{"amplitude", got.Amplitude, want.Amplitude},
		} {
			// The log10/pow round trip costs an ulp or two; a clamp costs
			// orders of magnitude, so the tolerance separates them cleanly.
			if math.Abs(pair.got-pair.exp) > 1e-9*math.Max(math.Abs(pair.exp), 1) {
				t.Errorf("mode %d %s was clamped: %.10g scored as %.10g", i, pair.name, pair.exp, pair.got)
			}
		}
	}
}

// TestTheDiagonalExcludesTheJointPreset guards the one number this phase
// exists to produce.
//
// The joint preset is authored at the median of the pack, so it occupies a
// diagonal cell exactly as a per-note preset does. A diagonal that included it
// would be the comparison scoring itself, and the price of one preset covering
// the range would come out smaller than the truth by however well the joint fit
// did at that single note -- with nothing in the output to say so.
func TestTheDiagonalExcludesTheJointPreset(t *testing.T) {
	rows := []pack.Scored{
		{Name: "084", Note: 84, Mean: 0.40, Scores: map[int]float64{84: 0.30, 94: 0.50}},
		{Name: "094", Note: 94, Mean: 0.44, Scores: map[int]float64{84: 0.58, 94: 0.30}},
		{Name: "joint", Note: 94, Mean: 0.35, Joint: true, Scores: map[int]float64{84: 0.36, 94: 0.34}},
	}

	got := pack.Compare(rows)

	// Two per-note presets contribute, at 0.30 each. The joint preset's 0.34 at
	// note 94 must not be among them.
	if got.DiagonalN != 2 {
		t.Errorf("the diagonal spans %d notes, want 2", got.DiagonalN)
	}

	if math.Abs(got.DiagonalMean-0.30) > 1e-12 {
		t.Errorf("diagonal mean %.6f, want 0.30 -- the joint preset leaked into it", got.DiagonalMean)
	}

	if math.Abs(got.JointMean-0.35) > 1e-12 {
		t.Errorf("joint mean %.6f, want 0.35", got.JointMean)
	}

	if math.Abs(got.Price-0.05) > 1e-12 {
		t.Errorf("price %.6f, want +0.05", got.Price)
	}

	// The best single-note preset is the best row mean among the non-joint
	// rows, and the joint preset must not win that either -- it is the thing
	// being compared against them, not one of them.
	if got.BestSingleName != "084" {
		t.Errorf("best single-note preset is %q, want %q", got.BestSingleName, "084")
	}
}

// morphagene is the 48 kHz pack. It exists in this file only to keep the
// pipeline honest about sample rates, not to be fitted: the README records it
// as effectively single-mode, 1.1 partials a note.
const morphagene = "../../testdata/reference/packs/radiohummingbird-morphagene-glockenspiel"

// TestAPackIsFittedAtItsOwnSampleRate covers a pack that could be planned and
// then not used for anything.
//
// Every stage after plan defaulted to 44,100 and then refused a reference that
// was not at 44,100, so the 48 kHz pack in testdata could be measured, resolved
// and written into a manifest, and then failed to run, to fit jointly and to
// score -- three separate errors, all of them about a rate nobody had chosen.
// The rate is discovered once, at plan time, and every later stage reads it
// from the manifest.
func TestAPackIsFittedAtItsOwnSampleRate(t *testing.T) {
	entries, err := os.ReadDir(morphagene)
	if err != nil {
		t.Fatalf("read %s: %v", morphagene, err)
	}

	packDir := filepath.Join(t.TempDir(), "pack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}

	copied := 0

	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".wav") || copied == 2 {
			continue
		}

		raw, err := os.ReadFile(filepath.Join(morphagene, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}

		if err := os.WriteFile(filepath.Join(packDir, entry.Name()), raw, 0o644); err != nil {
			t.Fatal(err)
		}

		copied++
	}

	if copied < 2 {
		t.Fatalf("the 48 kHz pack holds %d usable recordings, want at least 2", copied)
	}

	runDir := filepath.Join(t.TempDir(), "run")

	manifest, err := pack.Plan(packDir, runDir, pack.Options{Budget: 200, SeedBase: 910_000})
	if err != nil {
		t.Fatalf("plan a 48 kHz pack: %v", err)
	}

	if manifest.SampleRate != 48000 || manifest.Rate() != 48000 {
		t.Fatalf("the manifest records %d Hz (Rate %d), want the pack's own 48000",
			manifest.SampleRate, manifest.Rate())
	}

	// The stage that used to fail: running the fit at all.
	if err := pack.Run(context.Background(), runDir, io.Discard, pack.RunOptions{}); err != nil {
		t.Fatalf("run a 48 kHz pack: %v", err)
	}

	// And scoring a fitted preset back against the recordings, which is where
	// the rate mismatch surfaced as "is at 48000 Hz, not the requested 44100".
	first := filepath.Join(runDir, manifest.Jobs[0].Dir, "preset.json")
	if _, _, err := pack.ScorePresets(runDir, []string{first}, 0, 0); err != nil {
		t.Fatalf("score a 48 kHz pack: %v", err)
	}
}

// TestPlanRefusesAPackThatMixesSampleRates. One pack, one rate: the objective
// compares a render against a recording sample for sample, so a mixed pack
// would mean some notes scored against a resampled reference and others
// against the file. Refused where the disagreement is visible.
func TestPlanRefusesAPackThatMixesSampleRates(t *testing.T) {
	packDir := filepath.Join(t.TempDir(), "pack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}

	at44k, err := os.ReadFile(filepath.Join(hollandm, "c6.wav"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(packDir, "c6.wav"), at44k, 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(morphagene)
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".wav") {
			continue
		}

		raw, err := os.ReadFile(filepath.Join(morphagene, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(packDir, entry.Name()), raw, 0o644); err != nil {
			t.Fatal(err)
		}

		break
	}

	runDir := filepath.Join(t.TempDir(), "run")

	_, err = pack.Plan(packDir, runDir, pack.Options{Budget: 200, SeedBase: 920_000})
	if err == nil {
		t.Fatal("a pack mixing 44.1 and 48 kHz was planned")
	}

	if !strings.Contains(err.Error(), "Hz") {
		t.Errorf("the refusal does not say which rates disagreed: %v", err)
	}
}
