package optimizer

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cwbudde/glockenspiel/internal/preset"
)

func BenchmarkObjectiveEvaluateLegacyRMS(b *testing.B) {
	benchmarkObjectiveEvaluate(b, MetricRMS)
}

func BenchmarkObjectiveEvaluateLegacyLog(b *testing.B) {
	benchmarkObjectiveEvaluate(b, MetricLog)
}

func BenchmarkObjectiveEvaluateLegacySpectral(b *testing.B) {
	benchmarkObjectiveEvaluate(b, MetricSpectral)
}

func BenchmarkSimpleOptimizeLegacyShort(b *testing.B) {
	benchmarkOptimizeLegacyShort(b, &SimpleOptimizer{
		AbsoluteTolerance: 1e-10,
		RelativeTolerance: 1e-10,
		StallIterations:   8,
	})
}

func BenchmarkMayflyOptimizeLegacyShort(b *testing.B) {
	benchmarkOptimizeLegacyShort(b, &MayflyOptimizer{
		Variant:    "desma",
		Population: 10,
		Seed:       1,
	})
}

func benchmarkOptimizeLegacyShort(b *testing.B, opt Optimizer) {
	legacyPreset, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		b.Fatalf("load default preset: %v", err)
	}
	reference, sampleRate, err := loadLegacyReferenceForBenchmark()
	if err != nil {
		b.Fatalf("load legacy reference: %v", err)
	}
	objective, err := NewObjectiveFunctionWithBounds(reference, legacyPreset, sampleRate, 69, 100, MetricRMS, legacyValidationBounds(&legacyPreset.Parameters))
	if err != nil {
		b.Fatalf("NewObjectiveFunctionWithBounds failed: %v", err)
	}

	initial, err := objective.Codec().EncodeParams(&legacyPreset.Parameters)
	if err != nil {
		b.Fatalf("EncodeParams failed: %v", err)
	}

	b.ReportAllocs()
	var totalEvaluations int
	var totalIterations int
	var totalSamples int
	var totalConvergence time.Duration
	start := time.Now()
	for i := 0; i < b.N; i++ {
		result, err := opt.Optimize(objective.Objective(), initial, objective.Codec().EncodedBounds(), OptimizeOptions{
			MaxIterations: 20,
			TimeBudget:    time.Second,
		})
		if err != nil {
			b.Fatalf("Optimize failed: %v", err)
		}
		totalEvaluations += result.Evaluations
		totalIterations += result.Iterations
		totalSamples += result.Evaluations * len(reference)
		totalConvergence += result.Elapsed
	}
	elapsed := time.Since(start).Seconds()
	if elapsed > 0 {
		b.ReportMetric(float64(totalEvaluations)/elapsed, "eval/s")
		b.ReportMetric(float64(totalIterations)/elapsed, "iter/s")
		b.ReportMetric(float64(totalSamples)/elapsed, "samples/s")
	}
	if b.N > 0 {
		b.ReportMetric(float64(totalConvergence.Microseconds())/1000/float64(b.N), "convergence-ms")
	}
}

func benchmarkObjectiveEvaluate(b *testing.B, metric Metric) {
	legacyPreset, err := preset.Load(filepath.FromSlash("../../assets/presets/default.json"))
	if err != nil {
		b.Fatalf("load default preset: %v", err)
	}
	reference, sampleRate, err := loadLegacyReferenceForBenchmark()
	if err != nil {
		b.Fatalf("load legacy reference: %v", err)
	}
	objective, err := NewObjectiveFunctionWithBounds(reference, legacyPreset, sampleRate, 69, 100, metric, legacyValidationBounds(&legacyPreset.Parameters))
	if err != nil {
		b.Fatalf("NewObjectiveFunctionWithBounds failed: %v", err)
	}

	encoded, err := objective.Codec().EncodeParams(&legacyPreset.Parameters)
	if err != nil {
		b.Fatalf("EncodeParams failed: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = objective.Evaluate(encoded)
	}

	elapsed := b.Elapsed().Seconds()
	if elapsed > 0 {
		b.ReportMetric(float64(b.N)/elapsed, "eval/s")
		b.ReportMetric(float64(len(reference)*b.N)/elapsed, "samples/s")
	}
}
