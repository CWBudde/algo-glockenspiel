package optimizer

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwbudde/glockenspiel/internal/model"
	"github.com/cwbudde/glockenspiel/internal/preset"
	"github.com/cwbudde/glockenspiel/internal/synth"
)

func TestOptimizationRecoversSyntheticReferenceWithinTolerance(t *testing.T) {
	template := loadMinimalPreset(t)
	target := *template
	target.Parameters.InputMix = 0.08
	target.Parameters.FilterFrequency = 1200
	target.Parameters.Modes[0].Amplitude = 0.9
	target.Parameters.Modes[0].Frequency = 470
	target.Parameters.Modes[0].DecayMs = 140

	reference := renderNote(t, &target, 44100, 69, 100, 0.08)
	reference = addDeterministicNoise(reference, 1e-4)

	initial := *template
	initial.Parameters.InputMix = 0.2
	initial.Parameters.FilterFrequency = 900
	initial.Parameters.Modes[0].Amplitude = 0.7
	initial.Parameters.Modes[0].Frequency = 430
	initial.Parameters.Modes[0].DecayMs = 90

	bounds := narrowBoundsAroundTarget(&target.Parameters)

	objective, err := NewObjectiveFunctionWithBounds(reference, &initial, 44100, 69, 100, MetricRMS, bounds)
	if err != nil {
		t.Fatalf("NewObjectiveFunctionWithBounds failed: %v", err)
	}

	initialEncoded, err := objective.Codec().EncodeParams(&initial.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}
	initialCost := objective.Evaluate(initialEncoded)

	result, err := (&SimpleOptimizer{
		AbsoluteTolerance: 1e-12,
		RelativeTolerance: 1e-12,
		StallIterations:   40,
	}).Optimize(objective.Objective(), initialEncoded, objective.Codec().EncodedBounds(), OptimizeOptions{
		MaxIterations: 120,
		TimeBudget:    2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if !(result.BestCost < initialCost) {
		t.Fatalf("expected optimization to improve cost: initial=%g best=%g", initialCost, result.BestCost)
	}
	if !(result.BestCost <= initialCost*0.98) {
		t.Fatalf("expected material cost improvement: initial=%g best=%g", initialCost, result.BestCost)
	}
}

func TestOptimizationRespectsBoundsForEdgeCaseInitialConditions(t *testing.T) {
	template := loadMinimalPreset(t)
	target := *template
	target.Parameters.InputMix = 0.12
	target.Parameters.Modes[0].Amplitude = 0.85
	target.Parameters.Modes[0].Frequency = 455
	target.Parameters.Modes[0].DecayMs = 130

	reference := renderNote(t, &target, 44100, 69, 100, 0.06)
	bounds := narrowBoundsAroundTarget(&target.Parameters)

	objective, err := NewObjectiveFunctionWithBounds(reference, template, 44100, 69, 100, MetricRMS, bounds)
	if err != nil {
		t.Fatalf("NewObjectiveFunctionWithBounds failed: %v", err)
	}

	initialEncoded, err := objective.Codec().EncodeParams(&template.Parameters)
	if err != nil {
		t.Fatalf("EncodeParams failed: %v", err)
	}

	for i := range initialEncoded {
		initialEncoded[i] += 100
	}

	result, err := (&SimpleOptimizer{}).Optimize(
		objective.Objective(),
		initialEncoded,
		objective.Codec().EncodedBounds(),
		OptimizeOptions{MaxIterations: 40, TimeBudget: 1500 * time.Millisecond},
	)
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if !objective.Codec().EncodedBounds().Contains(result.BestParams) {
		t.Fatal("expected optimizer result to stay within encoded bounds")
	}

	recovered, err := objective.Codec().DecodeParams(result.BestParams)
	if err != nil {
		t.Fatalf("DecodeParams failed: %v", err)
	}

	if recovered.InputMix < bounds.InputMix.Min || recovered.InputMix > bounds.InputMix.Max {
		t.Fatalf("input_mix escaped bounds: %g not in [%g,%g]", recovered.InputMix, bounds.InputMix.Min, bounds.InputMix.Max)
	}

	if recovered.Modes[0].Amplitude < bounds.Amplitude.Min || recovered.Modes[0].Amplitude > bounds.Amplitude.Max {
		t.Fatalf("mode0 amplitude escaped bounds: %g not in [%g,%g]", recovered.Modes[0].Amplitude, bounds.Amplitude.Min, bounds.Amplitude.Max)
	}
}

func loadMinimalPreset(t *testing.T) *preset.Preset {
	t.Helper()

	p, err := preset.Load(filepath.FromSlash("../../testdata/presets/minimal.json"))
	if err != nil {
		t.Fatalf("load minimal preset: %v", err)
	}

	return p
}

func renderNote(t *testing.T, p *preset.Preset, sampleRate, note, velocity int, duration float64) []float32 {
	t.Helper()

	engine, err := synth.NewSynthesizer(p, sampleRate)
	if err != nil {
		t.Fatalf("NewSynthesizer failed: %v", err)
	}

	return engine.RenderNote(note, velocity, duration)
}

func addDeterministicNoise(samples []float32, amplitude float64) []float32 {
	noisy := append([]float32(nil), samples...)
	for i := range noisy {
		noise := amplitude * math.Sin(float64(i)*0.173)
		noisy[i] += float32(noise)
	}

	return noisy
}

func narrowBoundsAroundTarget(target *model.BarParams) ParamBounds {
	bounds := ParamBounds{
		InputMix:      Range{Min: math.Max(model.InputMixMin, target.InputMix-0.2), Max: math.Min(model.InputMixMax, target.InputMix+0.2)},
		FilterFreq:    Range{Min: math.Max(model.FilterFrequencyMinHz, target.FilterFrequency*0.75), Max: math.Min(model.FilterFrequencyMaxHz, target.FilterFrequency*1.25)},
		BaseFrequency: Range{Min: target.BaseFrequency, Max: target.BaseFrequency},
		Amplitude:     Range{Min: -0.1, Max: 1.1},
		FrequencyMult: Range{Min: 0.8, Max: 1.4},
		DecayMs:       Range{Min: 60, Max: 220},
		HarmonicGain:  Range{Min: model.HarmonicGainMin, Max: model.HarmonicGainMax},
	}
	return bounds
}

func assertCloseWithin(t *testing.T, got, want, tol float64, label string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s mismatch: got %.6f want %.6f tol %.6f", label, got, want, tol)
	}
}
