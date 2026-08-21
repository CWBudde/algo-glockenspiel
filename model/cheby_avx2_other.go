//go:build !amd64

package model

func processChebyshevBlockAVX2(_ []float32, _ []float32, _ *[4]float32) bool {
	return false
}
