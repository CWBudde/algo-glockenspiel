package optimizer

import (
	"encoding/json"
	"math"
	"path/filepath"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/model"
)

// threeModePreset is a bar with three partials of falling level and
// half-life, authored at note 69 with a base of 1000 Hz so that the mode
// multipliers sit inside the default box.
func threeModePreset() *preset.Preset {
	return &preset.Preset{
		Version: "2.0",
		Name:    "three modes",
		Note:    69,
		Parameters: model.BarParams{
			InputMix:        0,
			FilterFrequency: 12000,
			BaseFrequency:   1000,
			Modes: []model.ModeParams{
				{Amplitude: 1, Frequency: 1000, DecayMs: 300},
				{Amplitude: 0.5, Frequency: 2700, DecayMs: 120},
				{Amplitude: 0.25, Frequency: 5400, DecayMs: 60},
			},
			Chebyshev: model.ChebyshevParams{Enabled: false, HarmonicGains: []float64{}},
		},
	}
}

// compositeFixture renders the three-mode preset for a second and builds a
// balanced objective against it.
func compositeFixture(t *testing.T) (*preset.Preset, *ObjectiveFunction) {
	t.Helper()

	template := threeModePreset()
	reference := renderReference(t, template, 44100, 69, 100, 1.0)

	objective, err := NewObjectiveFunction(reference, template, 44100, 69, 100, MetricBalanced)
	if err != nil {
		t.Fatalf("NewObjectiveFunction failed: %v", err)
	}

	return template, objective
}

// metricsOf scores a preset through the objective.
func metricsOf(t *testing.T, objective *ObjectiveFunction, candidate *preset.Preset) Metrics {
	t.Helper()

	encoded, err := objective.Codec().EncodeParams(&candidate.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	metrics, err := objective.EvaluateMetrics(encoded)
	if err != nil {
		t.Fatalf("EvaluateMetrics failed: %v", err)
	}

	return metrics
}

func TestCompositeScoresTheReferenceItselfNearZero(t *testing.T) {
	template, objective := compositeFixture(t)
	metrics := metricsOf(t, objective, template)

	if metrics.ReferencePartials != 3 || metrics.ModelPartials != 3 || metrics.Matched != 3 {
		t.Fatalf("expected three partials matched three, got %+v", metrics)
	}

	for _, check := range []struct {
		term  Term
		limit float64
	}{
		{TermPartialCents, 1},
		{TermPartialLevel, 1.5},
		{TermPartialDecay, 0.2},
		{TermPartialMissing, 0},
		{TermPartialExtra, 0},
		{TermSpectralFine, 1},
		{TermSpectralCoarse, 1},
		{TermEnvelope, 0.5},
		{TermDecaySlope, 2},
		{TermWaveform, 0.01},
	} {
		if value := metrics.Value(check.term); math.IsNaN(value) || value > check.limit {
			t.Errorf("%s = %g, want at most %g", check.term, value, check.limit)
		}
	}

	if math.Abs(metrics.GainDB) > 0.1 || math.Abs(metrics.WaveformGainDB) > 0.1 {
		t.Errorf("gains = %g / %g dB, want 0", metrics.GainDB, metrics.WaveformGainDB)
	}

	if score := metrics.Score(ProfileBalanced); score > 0.05 {
		t.Errorf("score = %g, want under 0.05", score)
	}
}

func TestCompositeEvaluateIsTheScoreOfEvaluateMetrics(t *testing.T) {
	template, objective := compositeFixture(t)

	candidate := template.Clone()
	candidate.Parameters.Modes[1].Frequency *= 1.02
	candidate.Parameters.Modes[0].DecayMs *= 0.7

	encoded, err := objective.Codec().EncodeParams(&candidate.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	metrics, err := objective.EvaluateMetrics(encoded)
	if err != nil {
		t.Fatalf("EvaluateMetrics failed: %v", err)
	}

	if got, want := objective.Evaluate(encoded), metrics.Score(objective.Profile()); got != want {
		t.Fatalf("Evaluate = %g, Score = %g", got, want)
	}

	if objective.Metric() != MetricBalanced || objective.Profile().Name != string(MetricBalanced) {
		t.Fatalf("objective reports metric %q profile %q", objective.Metric(), objective.Profile().Name)
	}
}

func TestCompositeHearsADetunedPartial(t *testing.T) {
	template, objective := compositeFixture(t)
	self := metricsOf(t, objective, template)

	candidate := template.Clone()
	candidate.Parameters.Modes[1].Frequency *= math.Pow(2, 30.0/1200) // 30 cents sharp

	metrics := metricsOf(t, objective, candidate)

	// The second partial carries about a third of the level weight, so the
	// weighted RMS lands between a third and all of the thirty cents.
	if metrics.PartialCents < 10 || metrics.PartialCents > 30 {
		t.Errorf("partial cents = %g, want the weighted share of 30", metrics.PartialCents)
	}

	if metrics.Matched != 3 || metrics.PartialMissing != 0 {
		t.Errorf("a 30 cent detune should still match: %+v", metrics)
	}

	if metrics.SpectralFineDB <= self.SpectralFineDB+1 {
		t.Errorf("the fine spectral term does not see 30 cents: %g vs %g", metrics.SpectralFineDB, self.SpectralFineDB)
	}

	// The detuned partial is half the fundamental's amplitude and dies in
	// 120 ms, so the residual it leaves is a fraction of the waveform, not
	// all of it; but it is far more than the exact render leaves.
	if metrics.Waveform < 0.2 || metrics.Waveform < 10*self.Waveform {
		t.Errorf("thirty cents should lose part of the waveform, residual %g against %g", metrics.Waveform, self.Waveform)
	}

	if metrics.Score(ProfileBalanced) <= self.Score(ProfileBalanced) {
		t.Errorf("detuned scores no worse than exact")
	}
}

func TestCompositeChargesAMissingPartialItsWeight(t *testing.T) {
	template, objective := compositeFixture(t)

	candidate := template.Clone()
	candidate.Parameters.Modes[2].Amplitude = 0

	metrics := metricsOf(t, objective, candidate)

	if metrics.ModelPartials != 2 || metrics.Matched != 2 {
		t.Fatalf("expected two model partials both matched, got %+v", metrics)
	}

	// The third partial is the quietest of three at 0, -6 and -12 dB below
	// the lowpass, so it holds the smallest share of the weight above the
	// -40 dB floor: about a quarter.
	if metrics.PartialMissing < 0.15 || metrics.PartialMissing > 0.35 {
		t.Errorf("missing = %g, want the quietest partial's share", metrics.PartialMissing)
	}

	if metrics.PartialExtra != 0 {
		t.Errorf("extra = %g, want 0", metrics.PartialExtra)
	}
}

func TestCompositeChargesAnExtraPartialButNotAClick(t *testing.T) {
	template, objective := compositeFixture(t)

	extra := template.Clone()
	extra.Parameters.Modes = append(extra.Parameters.Modes, model.ModeParams{Amplitude: 0.5, Frequency: 3800, DecayMs: 100})

	// The codec is shaped by the template; a four-mode candidate needs an
	// objective built from a four-mode template.
	objectiveFour, err := NewObjectiveFunction(objective.reference, extra, 44100, 69, 100, MetricBalanced)
	if err != nil {
		t.Fatalf("NewObjectiveFunction failed: %v", err)
	}

	metrics := metricsOf(t, objectiveFour, extra)
	if metrics.ModelPartials != 4 || metrics.Matched != 3 || metrics.PartialExtra <= 0.1 {
		t.Errorf("an unmatched partial at -6 dB should cost: %+v", metrics)
	}

	click := extra.Clone()
	click.Parameters.Modes[3].DecayMs = 1

	metrics = metricsOf(t, objectiveFour, click)
	if metrics.ModelPartials != 3 || metrics.PartialExtra != 0 {
		t.Errorf("a one millisecond mode is a click, not a partial: %+v", metrics)
	}
}

func TestCompositeHearsAWrongDecay(t *testing.T) {
	template, objective := compositeFixture(t)

	candidate := template.Clone()
	for i := range candidate.Parameters.Modes {
		candidate.Parameters.Modes[i].DecayMs /= 2
	}

	metrics := metricsOf(t, objective, candidate)

	if math.Abs(metrics.PartialDecayOctaves-1) > 0.05 {
		t.Errorf("halving every half-life should read one octave, got %g", metrics.PartialDecayOctaves)
	}

	// The fundamental's 300 ms half-life is -20 dB/s; at 150 ms it is -40.
	if metrics.DecaySlopeDBps < 15 || metrics.DecaySlopeDBps > 25 {
		t.Errorf("decay slope difference = %g dB/s, want about 20", metrics.DecaySlopeDBps)
	}

	if metrics.EnvelopeDB < 2 {
		t.Errorf("envelope error = %g dB, want the faster decay to show", metrics.EnvelopeDB)
	}
}

func TestCompositeSolvesTheLevelGain(t *testing.T) {
	template, objective := compositeFixture(t)
	self := metricsOf(t, objective, template)

	candidate := template.Clone()
	for i := range candidate.Parameters.Modes {
		candidate.Parameters.Modes[i].Amplitude /= 2
	}

	metrics := metricsOf(t, objective, candidate)

	if math.Abs(metrics.GainDB-6.02) > 0.1 || math.Abs(metrics.WaveformGainDB-6.02) > 0.1 {
		t.Errorf("halving the amplitude should solve +6 dB, got %g / %g", metrics.GainDB, metrics.WaveformGainDB)
	}

	for _, term := range Terms() {
		if math.Abs(metrics.Value(term)-self.Value(term)) > 0.05 {
			t.Errorf("%s moved from %g to %g under a pure gain", term, self.Value(term), metrics.Value(term))
		}
	}
}

func TestCompositeLeavesOutWhatAShortReferenceCannotMeasure(t *testing.T) {
	template := threeModePreset()
	// 0.03 s is 1323 samples: under the coarse frame, over the smallest
	// analysis window.
	reference := renderReference(t, template, 44100, 69, 100, 0.03)

	objective, err := NewObjectiveFunction(reference, template, 44100, 69, 100, MetricBalanced)
	if err != nil {
		t.Fatalf("NewObjectiveFunction failed: %v", err)
	}

	metrics := metricsOf(t, objective, template)

	if !math.IsNaN(metrics.SpectralFineDB) || !math.IsNaN(metrics.SpectralCoarseDB) {
		t.Errorf("spectral terms should be unmeasured on 1323 samples: %g / %g", metrics.SpectralFineDB, metrics.SpectralCoarseDB)
	}

	if math.IsNaN(metrics.Waveform) || math.IsNaN(metrics.EnvelopeDB) {
		t.Errorf("waveform and envelope should still be measured: %+v", metrics)
	}

	score := metrics.Score(ProfileBalanced)
	if math.IsNaN(score) || math.IsInf(score, 0) || score > 0.2 {
		t.Errorf("score = %g, want finite and small", score)
	}

	measured := 0

	for _, contribution := range metrics.Contributions(ProfileBalanced) {
		if contribution.Measured {
			measured++
		} else if contribution.Share != 0 {
			t.Errorf("an unmeasured %s has share %g", contribution.Term, contribution.Share)
		}
	}

	if measured == 0 || measured == len(Terms()) {
		t.Errorf("%d of %d terms measured", measured, len(Terms()))
	}
}

func TestScoreIsAWeightedMeanOfSaturatedTerms(t *testing.T) {
	metrics := unmeasuredMetrics()

	if score := metrics.Score(ProfileBalanced); !math.IsInf(score, 1) {
		t.Fatalf("score of nothing measured = %g, want +Inf", score)
	}

	// One term at its norm scores one half on its own.
	metrics.Waveform = DefaultNorms[TermWaveform]
	if score := metrics.Score(ProfileBalanced); math.Abs(score-0.5) > 1e-12 {
		t.Fatalf("one term at its norm = %g, want 0.5", score)
	}

	// Every term perfect scores zero, and the shares sum to the score.
	for _, term := range Terms() {
		metrics.set(term, 0)
	}

	metrics.PartialCents = 30 // three norms: 0.75

	score := metrics.Score(ProfileBalanced)
	want := ProfileBalanced.Weights[TermPartialCents] * 0.75

	if math.Abs(score-want) > 1e-12 {
		t.Fatalf("score = %g, want %g", score, want)
	}

	var shares float64
	for _, contribution := range metrics.Contributions(ProfileBalanced) {
		shares += contribution.Share
	}

	if math.Abs(shares-score) > 1e-12 {
		t.Fatalf("shares sum to %g, score is %g", shares, score)
	}

	for _, profile := range []Profile{ProfileBalanced, ProfilePlacement, ProfilePolish} {
		var total float64
		for _, weight := range profile.Weights {
			total += weight
		}

		if math.Abs(total-1) > 1e-9 {
			t.Errorf("%s weights sum to %g", profile.Name, total)
		}
	}
}

func TestMetricsJSONRoundTripsWithNullForUnmeasured(t *testing.T) {
	metrics := unmeasuredMetrics()
	metrics.PartialCents = 12.5
	metrics.Waveform = 0.25
	metrics.GainDB = 3
	metrics.Lag = 7
	metrics.Matched = 2

	data, err := json.Marshal(metrics)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("the document is not JSON: %v", err)
	}

	if raw["spectral_fine_db"] != nil || raw["partial_cents"] != 12.5 || raw["waveform_gain_db"] != nil {
		t.Fatalf("unexpected document: %s", data)
	}

	var back Metrics
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if back.PartialCents != 12.5 || back.Waveform != 0.25 || back.GainDB != 3 || back.Lag != 7 || back.Matched != 2 {
		t.Fatalf("round trip lost values: %+v", back)
	}

	if !math.IsNaN(back.SpectralFineDB) || !math.IsNaN(back.WaveformGainDB) {
		t.Fatalf("null did not come back as NaN: %+v", back)
	}
}

func TestSpectralFloorIsNoiseOrDynamicRange(t *testing.T) {
	// A reference whose bins all sit at the magnitude floor has its floor at
	// the loudest bin's dynamic range.
	levels := make([]float64, 100)
	for i := range levels {
		levels[i] = -100
	}

	levels[0] = -10

	if got := spectralFloorDB(levels, -10); got != -70 {
		t.Fatalf("floor = %g, want -70 under a -10 dB peak", got)
	}

	// A noisy reference's floor is its median plus the margin, when that is
	// higher.
	for i := range levels {
		levels[i] = -50
	}

	if got := spectralFloorDB(levels, -10); got != -44 {
		t.Fatalf("floor = %g, want -44 over a -50 dB median", got)
	}
}

func TestPresetFromAnalysisMatchesTheReferenceItWasMeasuredFrom(t *testing.T) {
	template := threeModePreset()
	reference := renderReference(t, template, 44100, 69, 100, 1.0)

	measurement, err := analysis.Measure(reference, 44100, analysis.PartialOptions{})
	if err != nil {
		t.Fatalf("Measure failed: %v", err)
	}

	seeded, err := PresetFromAnalysis(template, measurement, 69, 0)
	if err != nil {
		t.Fatalf("PresetFromAnalysis failed: %v", err)
	}

	if len(seeded.Parameters.Modes) != 3 {
		t.Fatalf("seeded %d modes, want 3", len(seeded.Parameters.Modes))
	}

	objective, err := NewObjectiveFunction(reference, seeded, 44100, 69, 100, MetricBalanced)
	if err != nil {
		t.Fatalf("NewObjectiveFunction failed: %v", err)
	}

	metrics := metricsOf(t, objective, seeded)
	if metrics.PartialCents > 1 || metrics.PartialLevelDB > 1.5 || metrics.PartialDecayOctaves > 0.15 ||
		metrics.PartialMissing != 0 || metrics.PartialExtra != 0 {
		t.Fatalf("the seeded preset should be the partial term's perfect answer: %+v", metrics)
	}

	// Measured at another note, the seed is authored back at the template's.
	transposed, err := PresetFromAnalysis(template, measurement, 57, 2)
	if err != nil {
		t.Fatalf("PresetFromAnalysis at note 57 failed: %v", err)
	}

	if len(transposed.Parameters.Modes) != 2 {
		t.Fatalf("seeded %d modes with a cap of 2", len(transposed.Parameters.Modes))
	}

	if got := transposed.Parameters.Modes[0].Frequency; math.Abs(got-2000) > 5 {
		t.Fatalf("a 1000 Hz partial heard at note 57 is authored at %g Hz for note 69, want 2000", got)
	}

	if got := transposed.Parameters.Modes[0].DecayMs; math.Abs(got-150) > 10 {
		t.Fatalf("a 300 ms half-life at note 57 is authored as %g ms at note 69, want 150", got)
	}

	if _, err := PresetFromAnalysis(template, &analysis.Measurement{}, 69, 0); err == nil {
		t.Fatal("an empty analysis should be refused")
	}
}

// partialOnly weights the five partial terms and nothing else.
var partialOnly = Profile{
	Name: "partial",
	Weights: map[Term]float64{
		TermPartialCents:   0.3,
		TermPartialLevel:   0.2,
		TermPartialDecay:   0.2,
		TermPartialMissing: 0.15,
		TermPartialExtra:   0.15,
	},
}

func TestTheRecordedBarScoresWorseOnThePartialTermThanASeedFromTheAnalysis(t *testing.T) {
	reference, err := analysis.LoadReference(filepath.FromSlash("../../testdata/reference/glockenspiel_c5.wav"), analysis.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadReference failed: %v", err)
	}

	measurement, err := analysis.Measure(reference.Samples, reference.SampleRate, analysis.PartialOptions{})
	if err != nil {
		t.Fatalf("Measure failed: %v", err)
	}

	recorded, err := preset.Load(filepath.FromSlash("../../assets/presets/recorded-bar.json"))
	if err != nil {
		t.Fatalf("load recorded-bar: %v", err)
	}

	// Note 60 is the key that comes closest to undoing the hand retune, and
	// the seed is measured there too so both are judged at the same note.
	const note = 60

	seeded, err := PresetFromAnalysis(recorded, measurement, note, 6)
	if err != nil {
		t.Fatalf("PresetFromAnalysis failed: %v", err)
	}

	score := func(candidate *preset.Preset) (Metrics, float64) {
		objective, err := NewObjectiveFunctionWithConfig(reference.Samples, candidate, reference.SampleRate, note, 100,
			ObjectiveConfig{Metric: MetricBalanced, Bounds: DefaultParamBounds, Alignment: AlignOnsetCorrelation, Analysis: measurement})
		if err != nil {
			t.Fatalf("NewObjectiveFunctionWithConfig failed: %v", err)
		}

		metrics := metricsOf(t, objective, candidate)

		return metrics, metrics.Score(partialOnly)
	}

	recordedMetrics, recordedScore := score(recorded)
	seededMetrics, seededScore := score(seeded)

	t.Logf("recorded-bar: %.3f %+v", recordedScore, recordedMetrics)
	t.Logf("seeded:       %.3f %+v", seededScore, seededMetrics)

	if seededScore >= recordedScore {
		t.Fatalf("the seed scores %.3f on the partial terms, the shaped preset %.3f", seededScore, recordedScore)
	}

	if seededMetrics.PartialExtra >= recordedMetrics.PartialExtra || seededMetrics.PartialMissing >= recordedMetrics.PartialMissing {
		t.Fatalf("the seed should have fewer extra and missing partials: seed %+v, recorded %+v", seededMetrics, recordedMetrics)
	}

	if seededMetrics.Score(ProfileBalanced) >= recordedMetrics.Score(ProfileBalanced) {
		t.Fatalf("balanced: seed %.3f, recorded %.3f", seededMetrics.Score(ProfileBalanced), recordedMetrics.Score(ProfileBalanced))
	}
}

func TestDistanceCarriesTheCompositeMetrics(t *testing.T) {
	template, reference := distanceFixture(t)

	report, err := Distance(reference, template, DistanceConfig{SampleRate: 44100, Note: 69, Velocity: 100})
	if err != nil {
		t.Fatalf("Distance failed: %v", err)
	}

	if report.Metrics.Waveform > 0.01 || report.Metrics.Overlap == 0 {
		t.Fatalf("a preset against its own render should have no residual: %+v", report.Metrics)
	}

	for _, metric := range []Metric{MetricBalanced, MetricPlacement, MetricPolish} {
		score, ok := report.Scores[string(metric)]
		if !ok || math.IsNaN(score) || score > 0.1 {
			t.Errorf("score under %s = %g (%v)", metric, score, ok)
		}
	}
}
