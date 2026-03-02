//go:build !amd64

package model

func processModeBlock4AVX2(_ *float64, _ *float64, _ *float64, _ *modeBlock4Coeff, _ *[4]float32, _ *[4]float64) bool {
	return false
}
