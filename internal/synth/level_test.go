package synth

import (
	"math"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/model"
)

// TestApplyOutputGainHitsTheTarget is the outcome assertion for the level a
// fitted preset is written at: whatever level a preset arrives at, it leaves at
// PresetPeakTargetDBFS.
//
// The quiet case is the one that matters. A fit scores every candidate with the
// level solved in closed form and subtracted, so level is a flat ridge the
// search drifts along, and the best fit of the Morphagene c6 recording came out
// 37.11 dB below its reference with nothing in its score saying so. A factor of
// a hundred here stands in for that.
func TestApplyOutputGainHitsTheTarget(t *testing.T) {
	for _, tc := range []struct {
		name  string
		scale float64
	}{
		{name: "as shipped, a little above the target", scale: 1},
		{name: "quiet, as a drifted fit is", scale: 1.0 / 100},
		{name: "very quiet", scale: 1.0 / 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := loadTestPreset(t)

			// The dry mix carries no mode amplitudes, so it has to be scaled
			// alongside them or it alone decides the peak.
			candidate.Parameters.InputMix *= tc.scale
			for i := range candidate.Parameters.Modes {
				candidate.Parameters.Modes[i].Amplitude *= tc.scale
			}

			gainDB, clamped, err := ApplyOutputGain(candidate)
			if err != nil {
				t.Fatalf("ApplyOutputGain: %v", err)
			}

			if clamped {
				t.Fatalf("gain %+g dB was clamped; the fixture should sit inside the bounds", gainDB)
			}

			if got := peakDBFSOf(t, candidate); math.Abs(got-PresetPeakTargetDBFS) > 0.1 {
				t.Fatalf("preset peaks at %+.3f dBFS, want %+.1f", got, PresetPeakTargetDBFS)
			}
		})
	}
}

// TestApplyOutputGainIsIdempotent pins that the gain is solved absolutely
// rather than accumulated, so re-writing a preset does not walk its level.
func TestApplyOutputGainIsIdempotent(t *testing.T) {
	candidate := loadTestPreset(t)

	first, _, err := ApplyOutputGain(candidate)
	if err != nil {
		t.Fatalf("first ApplyOutputGain: %v", err)
	}

	second, _, err := ApplyOutputGain(candidate)
	if err != nil {
		t.Fatalf("second ApplyOutputGain: %v", err)
	}

	if math.Abs(first-second) > 1e-9 {
		t.Fatalf("solving twice gave %+g dB then %+g dB; the gain must be absolute", first, second)
	}
}

// TestApplyOutputGainUpgradesAV1Preset pins that a preset carrying a gain is a
// v2 document. A v1 loader rejects the field rather than ignoring it, which is
// the point: a loader that ignored it would play the preset at the wrong level.
func TestApplyOutputGainUpgradesAV1Preset(t *testing.T) {
	candidate := loadTestPreset(t)
	if candidate.Version != preset.VersionV1 {
		t.Skipf("fixture is version %q, not the v1 case this test is about", candidate.Version)
	}

	if _, _, err := ApplyOutputGain(candidate); err != nil {
		t.Fatalf("ApplyOutputGain: %v", err)
	}

	if candidate.Version != preset.VersionV3 {
		t.Fatalf("preset carries a gain in version %q, want %q", candidate.Version, preset.VersionV3)
	}

	if err := preset.Validate(candidate); err != nil {
		t.Fatalf("the upgraded preset does not validate: %v", err)
	}
}

// TestSolveOutputGainDBReportsAClamp pins that a preset needing more than the
// bounds allow says so instead of silently sitting on the bound.
func TestSolveOutputGainDBReportsAClamp(t *testing.T) {
	candidate := loadTestPreset(t)

	candidate.Parameters.InputMix = 0
	for i := range candidate.Parameters.Modes {
		candidate.Parameters.Modes[i].Amplitude *= 1e-5
	}

	gainDB, clamped, err := SolveOutputGainDB(candidate)
	if err != nil {
		t.Fatalf("SolveOutputGainDB: %v", err)
	}

	if !clamped {
		t.Fatalf("gain %+g dB was not reported as clamped", gainDB)
	}

	if gainDB != model.OutputGainDBMax {
		t.Fatalf("clamped gain is %+g dB, want the bound %+g", gainDB, model.OutputGainDBMax)
	}
}

func peakDBFSOf(t *testing.T, p *preset.Preset) float64 {
	t.Helper()

	engine, err := NewSynthesizer(p, presetPeakCalibrationSampleRate)
	if err != nil {
		t.Fatalf("build synthesizer: %v", err)
	}

	peak := engine.peakForNote(p.Note, presetPeakCalibrationVelocity)
	if peak <= 0 {
		t.Fatal("preset renders silence")
	}

	return 20 * math.Log10(peak)
}
