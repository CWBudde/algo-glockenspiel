//go:build !amd64

package model

func processBlock32AVX2(_ *QuadDecayOscillator, _ []float32, _ []float32) bool {
	return false
}

func processBlock32ModeBlock4PrototypeAVX2(_ *QuadDecayOscillator, _ []float32, _ []float32) bool {
	return false
}

func processBlock32ModeBlock4KernelAVX2(_ *QuadDecayOscillator, _ []float32, _ []float32) bool {
	return false
}
