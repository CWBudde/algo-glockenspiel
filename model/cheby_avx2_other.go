//go:build !amd64

package model

// processChebyshev4AVX2 shapes nothing off amd64, so every sample takes the
// scalar path.
func processChebyshev4AVX2(_ []float32, _ []float32, _ *[chebyshevFastGains]float32) int {
	return 0
}
