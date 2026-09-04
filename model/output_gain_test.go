package model

import (
	"math"
	"testing"
)

// TestOutputGainIsExactlyAScalarOnTheOutput is the property the whole design
// rests on: rendering at gain G is G times rendering at unity.
//
// The gain is never applied to the finished buffer. It is folded into
// coefficients the bar computes once per retune -- the mode amplitudes, or the
// shaper's gains when the shaper sits after the bank, plus the dry mix either
// way -- so that the audio path costs exactly what it did before. That is only
// a legitimate optimisation if every fold reproduces the multiply it replaces,
// and the shaper is a nonlinearity sitting in the middle of the chain, so the
// question is settled per configuration rather than in general.
//
// It is also what lets a fit measure the gain from one render and trust it.
func TestOutputGainIsExactlyAScalarOnTheOutput(t *testing.T) {
	const (
		sampleRate = 48000
		samples    = 4096
		velocity   = 100
	)

	for _, testCase := range []struct {
		name  string
		shape func(*BarParams)
	}{
		{
			name: "shaper disabled, bank and dry mix are the whole chain",
			shape: func(p *BarParams) {
				p.Chebyshev.Enabled = false
			},
		},
		{
			name: "shaper on the excitation, the v1 placement",
			shape: func(p *BarParams) {
				p.Chebyshev.Enabled = true
				p.Chebyshev.Stage = ChebyshevStageExcitation
			},
		},
		{
			name: "shaper on the output, where the fold cannot use the amplitudes",
			shape: func(p *BarParams) {
				p.Chebyshev.Enabled = true
				p.Chebyshev.Stage = ChebyshevStageOutput
			},
		},
		{
			name: "no dry mix, so the bank is the only path",
			shape: func(p *BarParams) {
				p.Chebyshev.Enabled = true
				p.Chebyshev.Stage = ChebyshevStageOutput
				p.InputMix = 0
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			for _, gainDB := range []float64{-20, -6.0206, 6.0206, 20} {
				unity := validTestParams()
				testCase.shape(&unity)

				gained := validTestParams()
				testCase.shape(&gained)
				gained.OutputGainDB = gainDB

				want := renderBarAt(t, &unity, sampleRate, velocity, samples)
				got := renderBarAt(t, &gained, sampleRate, velocity, samples)

				factor := math.Pow(10, gainDB/20)

				// Relative to the scaled peak rather than to each sample: the
				// fold rounds in float32 at a different place than a multiply
				// over the buffer would, and near a zero crossing that is the
				// whole value.
				tolerance := 1e-5 * peakAbs(want) * factor

				for i := range want {
					expected := float64(want[i]) * factor
					if math.Abs(float64(got[i])-expected) > tolerance {
						t.Fatalf("gain %+g dB, sample %d: got %g, want %g (tolerance %g)",
							gainDB, i, got[i], expected, tolerance)
					}
				}
			}
		})
	}
}

// TestOutputGainDefaultsToUnity pins that a preset written before the field
// existed renders bit-identically, which is what makes the zero value safe to
// leave out of every stored document.
func TestOutputGainDefaultsToUnity(t *testing.T) {
	params := validTestParams()

	explicit := validTestParams()
	explicit.OutputGainDB = 0

	want := renderBarAt(t, &params, 48000, 100, 2048)
	got := renderBarAt(t, &explicit, 48000, 100, 2048)

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d differs: got %g, want %g", i, got[i], want[i])
		}
	}
}

// TestOutputGainDoesNotTranspose pins that the gain is not pitch-dependent.
//
// Frequency scales with the note and DecayMs scales inversely, because a bar an
// octave down really does ring lower and longer. It does not ring louder, so a
// transposed preset keeps the level it was authored at and the keyboard's own
// level law is left to do its job.
func TestOutputGainDoesNotTranspose(t *testing.T) {
	params := validTestParams()
	params.OutputGainDB = 7.5

	for _, note := range []int{KeyboardFirstNote, 60, KeyboardLastNote} {
		transposed := params.Clone()
		TransposeToNote(&transposed, 69, note)

		if transposed.OutputGainDB != params.OutputGainDB {
			t.Fatalf("note %d: output gain became %g, want %g unchanged",
				note, transposed.OutputGainDB, params.OutputGainDB)
		}
	}
}

func renderBarAt(t *testing.T, params *BarParams, sampleRate, velocity, samples int) []float32 {
	t.Helper()

	bar, err := NewBar(params, sampleRate)
	if err != nil {
		t.Fatalf("build bar: %v", err)
	}

	out := bar.Synthesize(velocity, samples)
	if len(out) != samples {
		t.Fatalf("rendered %d samples, want %d", len(out), samples)
	}

	// Synthesize hands back the bar's own output buffer, which the next render
	// overwrites.
	rendered := make([]float32, len(out))
	copy(rendered, out)

	return rendered
}
