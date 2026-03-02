//go:build amd64

package model

import "github.com/cwbudde/glockenspiel/internal/cpufeat"

func processBlock32AVX2(o *QuadDecayOscillator, input, output []float32) bool {
	if len(input) == 0 || !cpufeat.Detect().HasAVX2 {
		return false
	}

	switch currentAVX2OscillatorStrategy() {
	case avx2OscillatorStrategyModeBlock4:
		return processBlock32ModeBlock4KernelAVX2(o, input, output)
	default:
		processBlock4AVX2Asm(
			&o.realState[0],
			&o.imagState[0],
			&o.amplitude[0],
			&o.cosCoeff[0],
			&o.sinCoeff[0],
			&input[0],
			&output[0],
			len(input),
		)
		return true
	}
}

func processBlock32ModeBlock4PrototypeAVX2(o *QuadDecayOscillator, input, output []float32) bool {
	if len(input) == 0 || len(output) < len(input) || !cpufeat.Detect().HasAVX2 {
		return false
	}

	i := 0
	for ; i+3 < len(input); i += 4 {
		var in4 [4]float32
		copy(in4[:], input[i:i+4])

		var out0, out1, out2, out3 [4]float64
		if !processModeBlock4AVX2(&o.realState[0], &o.imagState[0], &o.amplitude[0], &o.block4Coeff[0], &in4, &out0) {
			return false
		}
		if !processModeBlock4AVX2(&o.realState[1], &o.imagState[1], &o.amplitude[1], &o.block4Coeff[1], &in4, &out1) {
			return false
		}
		if !processModeBlock4AVX2(&o.realState[2], &o.imagState[2], &o.amplitude[2], &o.block4Coeff[2], &in4, &out2) {
			return false
		}
		if !processModeBlock4AVX2(&o.realState[3], &o.imagState[3], &o.amplitude[3], &o.block4Coeff[3], &in4, &out3) {
			return false
		}

		output[i] = float32(out0[0] + out1[0] + out2[0] + out3[0])
		output[i+1] = float32(out0[1] + out1[1] + out2[1] + out3[1])
		output[i+2] = float32(out0[2] + out1[2] + out2[2] + out3[2])
		output[i+3] = float32(out0[3] + out1[3] + out2[3] + out3[3])
	}

	if i < len(input) {
		o.processBlock32Generic(input[i:], output[i:])
	}

	return true
}

func processBlock32ModeBlock4KernelAVX2(o *QuadDecayOscillator, input, output []float32) bool {
	if len(input) == 0 || len(output) < len(input) || !cpufeat.Detect().HasAVX2 {
		return false
	}

	i := 0
	for ; i+3 < len(input); i += 4 {
		var out4 [4]float64
		processBlock4x4AVX2Asm(
			&o.realState[0],
			&o.imagState[0],
			&o.amplitude[0],
			&o.block4Coeff[0],
			&input[i],
			&out4[0],
		)
		output[i] = float32(out4[0])
		output[i+1] = float32(out4[1])
		output[i+2] = float32(out4[2])
		output[i+3] = float32(out4[3])
	}

	if i < len(input) {
		o.processBlock32Generic(input[i:], output[i:])
	}

	return true
}

//go:noescape
func processBlock4AVX2Asm(realState, imagState, amplitude, cosCoeff, sinCoeff *float64, input, output *float32, count int)

//go:noescape
func processBlock4x4AVX2Asm(realState, imagState, amplitude *float64, coeff *modeBlock4Coeff, input *float32, output *float64)
