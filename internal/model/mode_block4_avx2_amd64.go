//go:build amd64

package model

import "github.com/cwbudde/glockenspiel/internal/cpufeat"

func processModeBlock4AVX2(realState, imagState, amplitude *float64, coeff *modeBlock4Coeff, input *[4]float32, output *[4]float64) bool {
	if realState == nil || imagState == nil || amplitude == nil || coeff == nil || input == nil || output == nil || !cpufeat.Detect().HasAVX2 {
		return false
	}

	processModeBlock4AVX2Asm(realState, imagState, amplitude, coeff, &input[0], &output[0])
	return true
}

//go:noescape
func processModeBlock4AVX2Asm(realState, imagState, amplitude *float64, coeff *modeBlock4Coeff, input *float32, output *float64)
