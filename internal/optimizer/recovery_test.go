package optimizer

import (
	"context"
	"math"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/model"
)

// sixModeTarget is a bar-like target: six partials at glockenspiel-like
// ratios, levels falling with frequency, half-lives shortening.
func sixModeTarget() *preset.Preset {
	target := threeModePreset()
	target.Name = "six modes"
	target.Parameters.InputMix = 0.05
	target.Parameters.FilterFrequency = 16000
	target.Parameters.Modes = []model.ModeParams{
		{Amplitude: 1.0, Frequency: 1000, DecayMs: 400},
		{Amplitude: 0.6, Frequency: 2710, DecayMs: 200},
		{Amplitude: 0.4, Frequency: 5150, DecayMs: 120},
		{Amplitude: 0.3, Frequency: 8400, DecayMs: 90},
		{Amplitude: 0.25, Frequency: 11000, DecayMs: 70},
		{Amplitude: 0.2, Frequency: 14500, DecayMs: 50},
	}

	return target
}

// modeRecovery says how far a fitted preset's nearest mode sits from one
// target mode, in cents and in half-life ratio.
func modeRecovery(fitted *model.BarParams, target model.ModeParams) (cents, decayRatio float64) {
	cents = math.Inf(1)

	for _, mode := range fitted.Modes {
		distance := math.Abs(1200 * math.Log2(mode.Frequency/target.Frequency))
		if distance < cents {
			cents = distance
			decayRatio = mode.DecayMs / target.DecayMs
		}
	}

	return cents, decayRatio
}

// TestAColdFitFromTheRealBoxRecoversASixModeTarget is Phase 8.3's acceptance
// test: with the seed from the analysis, the frequency box from its
// fundamental and the two-second decay box -- no narrowed bounds -- a Mayfly
// fit recovers a synthetic six-mode target's frequencies within five cents
// and half-lives within ten percent in at least ten of twelve seeds.
func TestAColdFitFromTheRealBoxRecoversASixModeTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("twelve Mayfly fits; skipped under -short")
	}

	const (
		sampleRate = 44100
		note       = 69
		velocity   = 100
		seeds      = 12
		required   = 10
		maxCents   = 5.0
		maxDecay   = 1.10
	)

	target := sixModeTarget()
	reference := renderReference(t, target, sampleRate, note, velocity, 0.6)

	measurement := MeasureReference(reference, sampleRate)
	if measurement == nil {
		t.Fatal("the target could not be measured")
	}

	// The template is wrong everywhere the analysis cannot see: the dry mix,
	// the lowpass, and the modes it carries, which the seed replaces.
	template := threeModePreset()
	template.Parameters.InputMix = 0.3
	template.Parameters.FilterFrequency = 8000

	seed, seeded, err := SeedPreset(template, measurement, note, 0)
	if err != nil {
		t.Fatalf("SeedPreset: %v", err)
	}

	if seeded != len(target.Parameters.Modes) {
		t.Fatalf("the analysis seeded %d modes, want %d: partials %+v", seeded, len(target.Parameters.Modes), measurement.Partials)
	}

	config := DefaultObjectiveConfig(MetricBalanced)
	config.Alignment = AlignOnsetCorrelation
	config.Analysis = measurement
	config.Bounds.Frequency = FrequencyBoundsFor(measurement, sampleRate, seed.Note, note)

	objective, err := NewObjectiveFunctionWithConfig(reference, seed, sampleRate, note, velocity, config)
	if err != nil {
		t.Fatalf("NewObjectiveFunctionWithConfig: %v", err)
	}

	initial, err := objective.Codec().EncodeParams(&seed.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams: %v", err)
	}

	seedCost := objective.Evaluate(initial)
	recovered := 0

	for run := 1; run <= seeds; run++ {
		result, err := (&MayflyOptimizer{
			Variant:    "desma",
			Population: 12,
			Seed:       int64(run),
		}).Optimize(context.Background(), objective.Objective(), initial, objective.Codec().EncodedBounds(), OptimizeOptions{
			MaxIterations: 30,
		})
		if err != nil {
			t.Fatalf("seed %d: Optimize: %v", run, err)
		}

		if result.BestCost > seedCost+1e-12 {
			t.Fatalf("seed %d: the search lost the incumbent: best %g, seed %g", run, result.BestCost, seedCost)
		}

		fitted, err := objective.Codec().DecodeParams(result.BestParams)
		if err != nil {
			t.Fatalf("seed %d: DecodeParams: %v", run, err)
		}

		ok := true

		for i, mode := range target.Parameters.Modes {
			cents, ratio := modeRecovery(fitted, mode)
			if cents > maxCents || ratio > maxDecay || ratio < 1/maxDecay {
				ok = false
			}

			t.Logf("seed %2d mode %d: %6.2f cents, half-life x%.3f", run, i, cents, ratio)
		}

		t.Logf("seed %2d: cost %.4f (seed %.4f), evals %d, recovered %t", run, result.BestCost, seedCost, result.Evaluations, ok)

		if ok {
			recovered++
		}
	}

	if recovered < required {
		t.Fatalf("recovered the target in %d of %d seeds, want at least %d", recovered, seeds, required)
	}
}
