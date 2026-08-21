//go:build !amd64

package model

func processChebyshev4OscillatorBlockAVX2(_ *QuadDecayOscillator, _ []float32, _ []float32, _ *[4]float32) bool {
	return false
}
