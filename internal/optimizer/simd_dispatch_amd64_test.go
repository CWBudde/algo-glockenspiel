//go:build amd64

package optimizer

import (
	"math"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/cpufeat"
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

	// With AVX2 forced off the dispatch must land on exactly the generic
	// implementation, so anything short of bit-equality means the fallback is
	// not the function it claims to be.
	if got != want {
		t.Fatalf("fallback did not use the generic path: got %.17g want %.17g", got, want)
	}
}
