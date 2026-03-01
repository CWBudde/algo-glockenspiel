package optimizer

import (
	"math"
	"testing"
	"time"
)

func TestMayflyConfigRejectsUnsupportedVariant(t *testing.T) {
	if _, err := newMayflyConfig("nope", 10, 3, 5); err == nil {
		t.Fatal("expected unsupported variant to fail")
	}
}

func TestNormalizeDenormalizeVectorRoundTrip(t *testing.T) {
	bounds := Bounds{Ranges: []Range{
		{Min: -2, Max: 2},
		{Min: 10, Max: 20},
		{Min: 5, Max: 5},
	}}
	input := []float64{1.5, 12.5, 5}

	normalized := normalizeVector(input, bounds)
	denormalized := denormalizeVector(normalized, bounds)

	for i := range input {
		if math.Abs(input[i]-denormalized[i]) > 1e-12 {
			t.Fatalf("round-trip mismatch at %d: got %.12f want %.12f", i, denormalized[i], input[i])
		}
	}
}

func TestMayflyOptimizerRejectsNilObjective(t *testing.T) {
	opt := &MayflyOptimizer{}
	_, err := opt.Optimize(nil, []float64{0.5}, Bounds{Ranges: []Range{{Min: 0, Max: 1}}}, OptimizeOptions{
		MaxIterations: 2,
		TimeBudget:    time.Second,
	})
	if err == nil {
		t.Fatal("expected nil objective to fail")
	}
}
