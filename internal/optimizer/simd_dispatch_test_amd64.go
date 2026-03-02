//go:build amd64

package optimizer

import (
	"math"
	"testing"

	"github.com/cwbudde/glockenspiel/internal/cpufeat"
)

func TestSquaredDiffSumFallsBackWhenAVX2ForcedOff(t *testing.T) {
	t.Cleanup(cpufeat.ResetDetection)
	cpufeat.SetForcedFeatures(cpufeat.Features{HasAVX2: false})

	a := make([]float32, 64)
	b := make([]float32, 64)
	for i := range a {
		a[i] = float32(math.Sin(float64(i) * 0.11))
		b[i] = float32(math.Cos(float64(i) * 0.07))
	}

	got := squaredDiffSum(a, b)
	want := squaredDiffSumGeneric(a, b)

	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("unexpected fallback squared diff sum: got %.12f want %.12f", got, want)
	}
}
