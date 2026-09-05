package pack_test

import (
	"math"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/pack"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/model"
)

func seedTemplate(t *testing.T) *preset.Preset {
	t.Helper()

	template, err := preset.Load("../../assets/presets/default.json")
	if err != nil {
		t.Fatalf("load the default preset: %v", err)
	}

	return template
}

// TestPooledSeedRecoversTheLawItWasBuiltFrom is the calibration. Rows are
// generated from one bar transposed across the pack under the model's own law,
// so the pooled seed must recover that bar exactly: the ratios are constant by
// construction and the decays differ only by the transposition each note
// applies.
//
// If this drifts, the seed is undoing transposition with the wrong exponent or
// the wrong direction, which is the failure that would look like a slightly
// worse joint fit and nothing else.
func TestPooledSeedRecoversTheLawItWasBuiltFrom(t *testing.T) {
	const authored = 94

	// One bar: two partials, at the authored note.
	want := []struct{ ratio, decay float64 }{
		{2.76, 600},
		{5.35, 180},
	}

	rows := make([]pack.ModeRow, 0, 40)

	for note := 84; note <= 103; note++ {
		for _, mode := range want {
			// The decay this note would carry, which is the authored decay
			// divided by the transposition ratio -- exactly what
			// TransposeToNote does at render time.
			ratio := math.Pow(2, float64(note-authored)/12)

			rows = append(rows, pack.ModeRow{
				Note:      note,
				Ratio:     mode.ratio,
				DecayMs:   mode.decay / ratio,
				Amplitude: 1,
			})
		}
	}

	seeded, dropped, err := pack.PresetFromClusters(
		seedTemplate(t), pack.ByRatioCluster(rows), authored, 20, 0)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	if len(dropped) != 0 {
		t.Errorf("dropped %v, want nothing: every partial is at every note", dropped)
	}

	if len(seeded.Parameters.Modes) != len(want) {
		t.Fatalf("seeded %d modes, want %d", len(seeded.Parameters.Modes), len(want))
	}

	if seeded.Note != authored {
		t.Errorf("seeded preset is authored at %d, want %d", seeded.Note, authored)
	}

	fundamental := seeded.Parameters.BaseFrequency

	for i, mode := range want {
		got := seeded.Parameters.Modes[i]

		if ratio := got.Frequency / fundamental; math.Abs(ratio-mode.ratio) > 1e-9 {
			t.Errorf("mode %d sits at %.6f x the fundamental, want %.4f", i, ratio, mode.ratio)
		}

		// The decays were generated from this number and then transposed away
		// from it twenty times; recovering it is the whole claim.
		if math.Abs(got.DecayMs-mode.decay)/mode.decay > 1e-9 {
			t.Errorf("mode %d decays in %.6f ms at the authored note, want %.1f", i, got.DecayMs, mode.decay)
		}
	}
}

// TestPooledSeedDropsAPartialTooFewNotesHold is why coverage is a parameter. A
// partial fitted at three notes of twenty is a coincidence, and seeding it
// spends a dimension the joint fit then has to carry at all twenty.
func TestPooledSeedDropsAPartialTooFewNotesHold(t *testing.T) {
	rows := make([]pack.ModeRow, 0, 24)

	for note := 84; note <= 103; note++ {
		rows = append(rows, pack.ModeRow{Note: note, Ratio: 2.76, DecayMs: 600, Amplitude: 1})
	}

	// A partial at three notes only.
	for _, note := range []int{86, 87, 88} {
		rows = append(rows, pack.ModeRow{Note: note, Ratio: 9.11, DecayMs: 50, Amplitude: 0.4})
	}

	seeded, dropped, err := pack.PresetFromClusters(
		seedTemplate(t), pack.ByRatioCluster(rows), 94, 20, 0)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	if len(dropped) != 1 {
		t.Errorf("dropped %v, want exactly the thin one", dropped)
	}

	if len(seeded.Parameters.Modes) != 1 {
		t.Fatalf("seeded %d modes, want 1", len(seeded.Parameters.Modes))
	}

	if ratio := seeded.Parameters.Modes[0].Frequency / seeded.Parameters.BaseFrequency; math.Abs(ratio-2.76) > 1e-9 {
		t.Errorf("the surviving mode sits at %.4f x the fundamental, want 2.76", ratio)
	}
}

// TestPooledSeedAveragesGeometrically pins the choice of mean. A decay is a
// ratio-scale quantity, so the average of 100 ms and 400 ms is 200 ms; an
// arithmetic mean would say 250 ms and let one long-tailed bar drag the mode.
func TestPooledSeedAveragesGeometrically(t *testing.T) {
	const authored = 90

	// The second row is an octave up, where transposition has already halved
	// the decay, so it stands for 400 ms at the authored note.
	rows := []pack.ModeRow{
		{Note: authored, Ratio: 2.76, DecayMs: 100, Amplitude: 1},
		{Note: authored + 12, Ratio: 2.76, DecayMs: 200, Amplitude: 1},
	}

	seeded, _, err := pack.PresetFromClusters(
		seedTemplate(t), pack.ByRatioCluster(rows), authored, 2, 0)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := seeded.Parameters.Modes[0].DecayMs
	if math.Abs(got-200) > 1e-9 {
		t.Errorf("pooled decay %.6f ms, want 200 (the geometric mean of 100 and 400)", got)
	}
}

// TestPooledSeedKeepsTheAuthoredNoteOfTheTemplate is the transposition the
// seed has to do first: the template ships authored at note 69, and a pooled
// seed for a pack fitted at 84..103 is authored in the middle of that range, so
// the template's own modes and base frequency have to move before the pooled
// ratios are applied to them.
func TestPooledSeedKeepsTheAuthoredNoteOfTheTemplate(t *testing.T) {
	rows := make([]pack.ModeRow, 0, 20)
	for note := 84; note <= 103; note++ {
		rows = append(rows, pack.ModeRow{Note: note, Ratio: 2.76, DecayMs: 600, Amplitude: 1})
	}

	seeded, _, err := pack.PresetFromClusters(
		seedTemplate(t), pack.ByRatioCluster(rows), 94, 20, 0)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Note 94 is A#6, 1864.66 Hz in equal temperament.
	want := 440 * math.Pow(2, float64(94-69)/12)
	if math.Abs(seeded.Parameters.BaseFrequency-want)/want > 1e-9 {
		t.Errorf("base frequency %.4f Hz, want %.4f -- the template was not transposed",
			seeded.Parameters.BaseFrequency, want)
	}

	if err := preset.Validate(seeded); err != nil {
		t.Errorf("the pooled seed does not validate: %v", err)
	}

	if err := model.ValidateAuthoredBarParams(&seeded.Parameters, seeded.Note); err != nil {
		t.Errorf("the pooled seed cannot be authored: %v", err)
	}
}
