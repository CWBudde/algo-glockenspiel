package optimizer

import (
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/model"
)

func TestFrequencyBoundsForFollowTheFundamentalAndTheSampleRate(t *testing.T) {
	measurement := &analysis.Measurement{FundamentalHz: 1000}

	same := FrequencyBoundsFor(measurement, 44100, 69, 69)
	if math.Abs(same.Min-500) > 1e-9 || math.Abs(same.Max-0.45*44100) > 1e-9 {
		t.Fatalf("bounds at the authored note = %+v, want [500, 19845]", same)
	}

	// Fitting a fifth below the authored note: what plays at 500 Hz is
	// written at 500 / 2^(-9/12), and the Nyquist ceiling is capped by the
	// default box once converted the same way.
	below := FrequencyBoundsFor(measurement, 44100, 69, 60)
	ratio := math.Pow(2, -9.0/12)

	if math.Abs(below.Min-500/ratio) > 1e-6 || below.Max != DefaultParamBounds.Frequency.Max {
		t.Fatalf("bounds a fifth below = %+v, want [%g, %g]", below, 500/ratio, DefaultParamBounds.Frequency.Max)
	}

	// Fitting above the authored note lowers the ceiling: a mode written at
	// 16.7 kHz already plays above 0.45 of the sample rate at note 72.
	above := FrequencyBoundsFor(measurement, 44100, 69, 72)
	if want := 0.45 * 44100 / math.Pow(2, 3.0/12); math.Abs(above.Max-want) > 1e-6 {
		t.Fatalf("ceiling three semitones up = %g, want %g", above.Max, want)
	}

	if none := FrequencyBoundsFor(nil, 8000, 69, 69); none.Min != DefaultParamBounds.Frequency.Min || none.Max != 3600 {
		t.Fatalf("bounds without a measurement at 8 kHz = %+v, want [20, 3600]", none)
	}

	if degenerate := FrequencyBoundsFor(measurement, 100, 69, 69); degenerate != DefaultParamBounds.Frequency {
		t.Fatalf("a ceiling below the floor should fall back to the default box, got %+v", degenerate)
	}
}

func TestSeedPresetKeepsTheTemplateOnRequestAndUpgradesWhenSeeding(t *testing.T) {
	template := loadMinimalPreset(t)
	reference := renderNote(t, template, 44100, 69, 100, 0.5)

	measurement := MeasureReference(reference, 44100)
	if measurement == nil || len(measurement.Partials) == 0 {
		t.Fatal("the minimal preset's render measured no partials")
	}

	kept, seeded, err := SeedPreset(template, measurement, 69, KeepTemplateModes)
	if err != nil || seeded != 0 || kept != template {
		t.Fatalf("KeepTemplateModes: preset %p seeded %d err %v, want the template untouched", kept, seeded, err)
	}

	unmeasured, seeded, err := SeedPreset(template, nil, 69, 0)
	if err != nil || seeded != 0 || unmeasured != template {
		t.Fatalf("no measurement: preset %p seeded %d err %v, want the template untouched", unmeasured, seeded, err)
	}

	all, seeded, err := SeedPreset(template, measurement, 69, 0)
	if err != nil {
		t.Fatalf("SeedPreset: %v", err)
	}

	if seeded != len(measurement.Partials) || len(all.Parameters.Modes) != seeded {
		t.Fatalf("seeded %d modes from %d partials, preset holds %d", seeded, len(measurement.Partials), len(all.Parameters.Modes))
	}

	// A v1 template holds exactly four modes, so a seeded preset is written
	// in the current version, with the v1 defaults made explicit.
	if all.Version != preset.CurrentVersion || all.Parameters.Chebyshev.Stage == "" {
		t.Fatalf("seeded preset version %q stage %q, want %q with an explicit stage", all.Version, all.Parameters.Chebyshev.Stage, preset.CurrentVersion)
	}

	if template.Version != preset.VersionV1 || len(template.Parameters.Modes) != 4 {
		t.Fatal("seeding modified the template")
	}

	capped, seeded, err := SeedPreset(template, measurement, 69, 1)
	if err != nil || seeded != 1 || len(capped.Parameters.Modes) != 1 {
		t.Fatalf("capped at one mode: seeded %d, preset holds %d, err %v", seeded, len(capped.Parameters.Modes), err)
	}

	more, seeded, err := SeedPreset(template, measurement, 69, 500)
	if err != nil || seeded != len(measurement.Partials) || len(more.Parameters.Modes) != seeded {
		t.Fatalf("asking for more modes than partials: seeded %d, err %v, want every partial", seeded, err)
	}
}

func TestObjectiveNarrowsTheDecayBoxToTheAuthoredCeiling(t *testing.T) {
	template := threeModePreset()
	reference := renderReference(t, template, 44100, 69, 100, 0.1)

	objective, err := NewObjectiveFunction(reference, template, 44100, 69, 100, MetricRMS)
	if err != nil {
		t.Fatalf("NewObjectiveFunction: %v", err)
	}

	ceiling := model.AuthoredDecayMsMax(template.Note)
	if got := objective.Codec().Bounds().DecayMs.Max; math.Abs(got-ceiling) > 1e-9 {
		t.Fatalf("decay box ceiling at note %d = %g, want the authoring ceiling %g", template.Note, got, ceiling)
	}

	if got := objective.Codec().Bounds().DecayMs.Min; got != model.DecayMsSearchMin {
		t.Fatalf("decay box floor = %g, want %g", got, model.DecayMsSearchMin)
	}

	// A decay written at the ceiling is one the preset file accepts.
	encoded, err := objective.Codec().EncodeParams(&template.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams: %v", err)
	}

	bounds := objective.Codec().EncodedBounds()
	for i := range encoded {
		encoded[i] = bounds.Ranges[i].Max
	}

	decoded, err := objective.Codec().DecodeParams(encoded)
	if err != nil {
		t.Fatalf("DecodeParams at the box's corner: %v", err)
	}

	written := template.Clone()
	written.Parameters = *decoded

	if err := preset.Validate(written); err != nil {
		t.Fatalf("a preset written from the corner of the box does not validate: %v", err)
	}

	config := DefaultObjectiveConfig(MetricRMS)
	config.Bounds.DecayMs = Range{Min: 800, Max: 2000}
	config.StrictBounds = true

	if _, err := NewObjectiveFunctionWithConfig(reference, template, 44100, 69, 100, config); err == nil || !strings.Contains(err.Error(), "may carry") {
		t.Fatalf("a decay box above the authoring ceiling was accepted: %v", err)
	}
}

// TestEveryPointOfTheDefaultBoxDecodes is the plateau the review found: the
// old box could decode to a frequency the model refuses, so whole regions of
// it scored +Inf. The absolute frequency box lies inside the model's range, so
// no point of it fails.
func TestEveryPointOfTheDefaultBoxDecodes(t *testing.T) {
	template := threeModePreset()

	codec, err := NewParamCodec(&template.Parameters)
	if err != nil {
		t.Fatalf("NewParamCodec: %v", err)
	}

	bounds := codec.EncodedBounds()
	rng := rand.New(rand.NewSource(1))

	points := [][]float64{make([]float64, codec.Dimension()), make([]float64, codec.Dimension())}
	for i := range points[1] {
		points[1][i] = 1
	}

	for range 2000 {
		unit := make([]float64, codec.Dimension())
		for i := range unit {
			unit[i] = rng.Float64()
		}

		points = append(points, unit)
	}

	for _, unit := range points {
		encoded, err := bounds.Denormalize(unit)
		if err != nil {
			t.Fatalf("Denormalize: %v", err)
		}

		if _, err := codec.DecodeParams(encoded); err != nil {
			t.Fatalf("a point of the default box does not decode: %v (unit %v)", err, unit)
		}
	}
}

func TestCodecWritesTheTemplateBaseFrequencyThrough(t *testing.T) {
	template := threeModePreset()

	codec, err := NewParamCodec(&template.Parameters)
	if err != nil {
		t.Fatalf("NewParamCodec: %v", err)
	}

	if want := 2 + 3*len(template.Parameters.Modes); codec.Dimension() != want {
		t.Fatalf("dimension %d, want %d: the base frequency is not a dimension", codec.Dimension(), want)
	}

	unit := make([]float64, codec.Dimension())
	for i := range unit {
		unit[i] = 0.37
	}

	encoded, err := codec.EncodedBounds().Denormalize(unit)
	if err != nil {
		t.Fatalf("Denormalize: %v", err)
	}

	decoded, err := codec.DecodeParams(encoded)
	if err != nil {
		t.Fatalf("DecodeParams: %v", err)
	}

	if decoded.BaseFrequency != template.Parameters.BaseFrequency {
		t.Fatalf("decoded base frequency %g, want the template's %g", decoded.BaseFrequency, template.Parameters.BaseFrequency)
	}
}

func TestPinnedNamesModesByTheirWrittenIndex(t *testing.T) {
	template := threeModePreset()

	codec, err := NewParamCodec(&template.Parameters)
	if err != nil {
		t.Fatalf("NewParamCodec: %v", err)
	}

	encoded, err := codec.EncodeParams(&template.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams: %v", err)
	}

	// Swap the first and last slots, as a search is free to, and pin the
	// amplitude of the slot that now holds the highest mode.
	first, last := scalarParameterCount, scalarParameterCount+2*3
	for k := range 3 {
		encoded[first+k], encoded[last+k] = encoded[last+k], encoded[first+k]
	}

	encoded[first] = codec.Bounds().Amplitude.Max

	pinned, err := codec.Pinned(encoded)
	if err != nil {
		t.Fatalf("Pinned: %v", err)
	}

	var names []string
	for _, dimension := range pinned {
		names = append(names, dimension.Name)
	}

	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "modes[2].amplitude") || strings.Contains(joined, "modes[0].amplitude") {
		t.Fatalf("pinned %q, want the highest mode named modes[2] whatever slot it sits in", joined)
	}

	decoded, err := codec.DecodeParams(encoded)
	if err != nil {
		t.Fatalf("DecodeParams: %v", err)
	}

	if math.Abs(decoded.Modes[2].Frequency-5400) > 1e-6 || decoded.Modes[2].Amplitude != codec.Bounds().Amplitude.Max {
		t.Fatalf("decoded modes are not sorted with their amplitudes: %+v", decoded.Modes)
	}
}

func TestSeedPopulationSurroundsTheIncumbent(t *testing.T) {
	incumbent := []float64{0.5, 0.02, 0.98}
	rng := rand.New(rand.NewSource(1))

	rows := seedPopulation(incumbent, 10, 0.5, 0.05, rng)
	if len(rows) != 5 {
		t.Fatalf("%d rows for half of ten, want 5", len(rows))
	}

	for i, value := range rows[0] {
		if value != incumbent[i] {
			t.Fatalf("row 0 = %v, want the incumbent %v", rows[0], incumbent)
		}
	}

	moved := false

	for _, row := range rows[1:] {
		for i, value := range row {
			if value < 0 || value > 1 {
				t.Fatalf("row %v left the unit cube", row)
			}

			if math.Abs(value-incumbent[i]) > 0.25 {
				t.Fatalf("row %v is further from the incumbent than five sigma", row)
			}

			if value != incumbent[i] {
				moved = true
			}
		}
	}

	if !moved {
		t.Fatal("no seeded row differs from the incumbent")
	}

	if got := len(seedPopulation(incumbent, 10, 1, 0.05, rng)); got != 10 {
		t.Fatalf("a fraction of one should fill the population, got %d rows", got)
	}

	if got := len(seedPopulation(incumbent, 10, 0.01, 0.05, rng)); got != 1 {
		t.Fatalf("a tiny fraction should still seed the incumbent, got %d rows", got)
	}
}
