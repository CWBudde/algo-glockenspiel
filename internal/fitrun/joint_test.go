package fitrun_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	embeddedassets "github.com/cwbudde/algo-glockenspiel/assets"
	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
)

// jointNotes are two notes two octaves apart, which is past the octave the
// exponent needs to be identifiable at all.
var jointNotes = []int{84, 108}

// writeJointReferences renders the embedded preset at each note and writes it
// as a wav, which gives a joint fit a reference set the model can reach
// exactly. The point of these tests is the plumbing -- what reaches the
// objective and what the run directory records -- not the quality of a fit, so
// a synthetic reference is the right one: it keeps the budget at a few hundred
// evaluations and leaves nothing about the recording to explain a failure.
func writeJointReferences(t *testing.T, dir string) []fitrun.ReferenceSpec {
	t.Helper()

	source, err := embeddedassets.DefaultPreset()
	if err != nil {
		t.Fatalf("load the default preset: %v", err)
	}

	const sampleRate = 44100

	engine, err := synth.NewSynthesizer(source, sampleRate)
	if err != nil {
		t.Fatalf("build synthesizer: %v", err)
	}

	refs := make([]fitrun.ReferenceSpec, 0, len(jointNotes))

	for _, note := range jointNotes {
		path := filepath.Join(dir, fmt.Sprintf("ref-%03d.wav", note))

		if err := wavio.WriteMono(path, sampleRate, engine.RenderNote(note, 100, 0.5)); err != nil {
			t.Fatalf("write reference for note %d: %v", note, err)
		}

		refs = append(refs, fitrun.ReferenceSpec{Path: path, Note: note})
	}

	return refs
}

func jointSpec(t *testing.T, dir string) fitrun.Spec {
	t.Helper()

	return fitrun.Spec{
		Dir:            dir,
		References:     writeJointReferences(t, t.TempDir()),
		Engine:         fitrun.Engine{Name: fitrun.EngineCMAES},
		MaxEvaluations: 300,
		Seed:           7,
		Workers:        2,
	}
}

// TestJointRunRecordsEveryReferenceItScored is the gap a joint fit opens in the
// provenance: every file the run directory and the preset name is read by name
// by something, and until this test the singular reference block carried the
// lowest note's file for a fit of twenty. A reader would have seen a
// well-formed record of a search that never happened.
func TestJointRunRecordsEveryReferenceItScored(t *testing.T) {
	dir := t.TempDir()

	outcome, err := fitrun.Run(context.Background(), jointSpec(t, dir), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var config struct {
		Reference  *json.RawMessage `json:"reference"`
		References []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Note   int    `json:"note"`
		} `json:"references"`
	}

	data, err := os.ReadFile(filepath.Join(dir, fitrun.FileConfig))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}

	if config.Reference != nil {
		t.Errorf("a joint fit wrote a singular reference block: %s", *config.Reference)
	}

	if len(config.References) != len(jointNotes) {
		t.Fatalf("config.json names %d references, want %d", len(config.References), len(jointNotes))
	}

	for i, ref := range config.References {
		if ref.Note != jointNotes[i] {
			t.Errorf("reference %d is at note %d, want %d", i, ref.Note, jointNotes[i])
		}

		if ref.Path == "" || ref.SHA256 == "" {
			t.Errorf("reference %d is unpinned: path %q sha %q", i, ref.Path, ref.SHA256)
		}
	}

	// The preset has to answer for itself once it is copied out of the run
	// directory, so the same set is in its provenance with the score each note
	// actually reached.
	refs := outcome.Preset.Provenance.References
	if len(refs) != len(jointNotes) {
		t.Fatalf("the preset's provenance names %d references, want %d", len(refs), len(jointNotes))
	}

	for i, ref := range refs {
		if ref.Note != jointNotes[i] {
			t.Errorf("provenance reference %d is at note %d, want %d", i, ref.Note, jointNotes[i])
		}

		if math.IsNaN(ref.Score) || ref.Score <= 0 {
			t.Errorf("provenance reference %d carries no score: %v", i, ref.Score)
		}
	}
}

// TestJointSummaryRefusesToInventASingleSetOfTerms pins that the score's own
// arithmetic is visible in the file. The score is the mean of the per-note
// scores, so no set of terms reproduces it; writing the first note's terms
// there would be a number a reader could not tell from the real thing.
func TestJointSummaryRefusesToInventASingleSetOfTerms(t *testing.T) {
	dir := t.TempDir()

	if _, err := fitrun.Run(context.Background(), jointSpec(t, dir), nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	summary := readSummary(t, dir)

	if !math.IsNaN(summary.Terms.PartialCents) {
		t.Errorf("a joint fit wrote partial_cents %v, want NaN", summary.Terms.PartialCents)
	}

	if len(summary.NoteTerms) != len(jointNotes) {
		t.Fatalf("summary carries %d per-note blocks, want %d", len(summary.NoteTerms), len(jointNotes))
	}

	mean := 0.0
	for _, note := range summary.NoteTerms {
		mean += note.Score
	}

	mean /= float64(len(summary.NoteTerms))

	if math.Abs(mean-summary.Score) > 1e-9 {
		t.Errorf("the mean of the per-note scores is %.9f but the run scored %.9f", mean, summary.Score)
	}
}

// TestJointFitCanSearchTheDecayKeytrack is the whole reason the multi-note
// objective exists in this phase: the exponent is a gauge freedom at one note
// and only a spread of notes can see it.
func TestJointFitCanSearchTheDecayKeytrack(t *testing.T) {
	dir := t.TempDir()

	spec := jointSpec(t, dir)
	spec.SearchDecayKeytrack = true

	outcome, err := fitrun.Run(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	beta := outcome.Preset.Parameters.DecayKeytrack
	if beta == nil {
		t.Fatal("a fit that searched the exponent shipped a preset without one")
	}

	if preset.OlderThan(outcome.Preset.Version, preset.VersionV4) {
		t.Errorf("a preset carrying an exponent is version %q, which cannot carry one", outcome.Preset.Version)
	}

	if err := preset.Validate(outcome.Preset); err != nil {
		t.Errorf("the fitted preset does not validate: %v", err)
	}

	// The exponent is a searched coordinate, so it is one of the dimensions the
	// pinned report covers and one more than the same fit without it.
	fixed := jointSpec(t, t.TempDir())
	fixed.Dir = t.TempDir()

	baseline, err := fitrun.Run(context.Background(), fixed, nil)
	if err != nil {
		t.Fatalf("baseline run: %v", err)
	}

	if got, want := outcome.Summary.Dimension, baseline.Summary.Dimension+1; got != want {
		t.Errorf("searching the exponent gave dimension %d, want %d", got, want)
	}

	if baseline.Preset.Parameters.DecayKeytrack != nil {
		t.Error("a fit that did not search the exponent shipped one anyway")
	}
}

// TestPlainFitCannotSearchTheDecayKeytrack pins the refusal by construction. A
// single recording cannot identify the exponent -- any value is absorbed by
// scaling the decays -- so asking for it is an error rather than a request that
// quietly returns a number.
func TestPlainFitCannotSearchTheDecayKeytrack(t *testing.T) {
	spec := cmaesSpec(t.TempDir())
	spec.SearchDecayKeytrack = true

	if _, err := fitrun.Run(context.Background(), spec, nil); err == nil {
		t.Fatal("a single-reference fit searched the decay keytrack")
	}
}
