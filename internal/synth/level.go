package synth

import (
	"errors"
	"fmt"
	"math"

	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/model"
)

// PresetPeakTargetDBFS is the level a preset is written at: the peak at its own
// note, under the calibration conditions below, sits this far below full scale.
//
// It is a headroom rule rather than a loudness rule, and it is the rule the
// shipped presets already follow -- TestBuiltinPresetsRenderNearMinusThreeDBFS
// asserts it of every embedded preset, within
// PresetPeakToleranceDB. A fit that wrote any other level would produce presets
// that could not be promoted without failing that test, which is the whole
// point of writing a level in the first place.
//
// Why a level has to be written at all: the fit objective solves the level in
// closed form and divides it out of every term, so nothing in a fit's score
// speaks for its level. The best fit of the Morphagene c6 recording came out
// 37.11 dB below its reference, and it is worse than a free choice -- the
// spectral term's magnitude floor is absolute, so a very quiet render has its
// low bins flattened and then lifted back by the solved gain, which flatters it.
// Level had to be pinned from outside the search.
//
// The consequence reaches the whole instrument rather than one file: the
// realtime engine normalises every note to the preset's own note (see
// calibrateNoteTrims), so a preset that is quiet makes every key quiet.
const (
	PresetPeakTargetDBFS = -3.0

	// PresetPeakToleranceDB is how far from the target a preset may sit and
	// still be considered at the level. It is the tolerance the shipped-preset
	// assertion uses.
	PresetPeakToleranceDB = 0.25
)

// The conditions the peak is measured under. They are fixed rather than taken
// from the caller, because the level is a property of the preset and not of the
// fit that produced it.
//
// A fit runs at whatever rate its reference was recorded at -- 48 kHz for the
// Morphagene pack -- and the peak is mildly rate-dependent: the strike is a
// single-sample impulse, so a higher rate feeds the modes a wider-band
// excitation and 48 kHz comes out about 0.7 dB hotter than 44.1 kHz. Solving
// the gain at the fit's own rate would therefore land a 48 kHz fit 0.7 dB off
// the target when it is later measured at 44.1 kHz, which is three times the
// tolerance and would fail the shipped-preset assertion for a reason that has
// nothing to do with the preset.
//
// Velocity 100 rather than the loudest a keyboard can send, for the same
// reason: it is what the assertion measures at. Headroom at velocity 127 is
// left to the engine's own per-note level law, which is what
// TestRealtimeKeyboardIsLevelAndUnclipped covers.
const (
	presetPeakCalibrationSampleRate = 44100
	presetPeakCalibrationVelocity   = 100
)

// SolveOutputGainDB returns the model.BarParams.OutputGainDB that puts p at
// PresetPeakTargetDBFS, and reports whether the bounds clamped the answer.
//
// The gain the preset already carries is ignored rather than added to, so the
// result is absolute and solving twice is idempotent.
//
// This is a measurement, not a search. One render of one note answers it,
// because the gain is exactly a scalar on the output: model.Bar folds it into
// coefficients it computes once per retune, so a bar at gain G renders G times
// what the same bar renders at unity, and the peak scales with it.
func SolveOutputGainDB(p *preset.Preset) (gainDB float64, clamped bool, err error) {
	if p == nil {
		return 0, false, errors.New("preset cannot be nil")
	}

	measured := p.Clone()
	measured.Parameters.OutputGainDB = 0

	engine, err := NewSynthesizer(measured, presetPeakCalibrationSampleRate)
	if err != nil {
		return 0, false, fmt.Errorf("build synthesizer to measure the output level: %w", err)
	}

	peak := engine.peakForNote(measured.Note, presetPeakCalibrationVelocity)
	if peak <= 0 {
		return 0, false, fmt.Errorf("preset renders silence at note %d: no output level to solve for", measured.Note)
	}

	gainDB = PresetPeakTargetDBFS - 20*math.Log10(peak)

	switch {
	case gainDB < model.OutputGainDBMin:
		return model.OutputGainDBMin, true, nil
	case gainDB > model.OutputGainDBMax:
		return model.OutputGainDBMax, true, nil
	default:
		return gainDB, false, nil
	}
}

// ApplyOutputGain solves p's output gain and writes it into p, returning the
// gain it set and whether the bounds clamped it.
//
// It is the step that stops a fit from shipping at whatever level it happened
// to land on, and it is idempotent: the gain p already carries is replaced
// rather than compounded.
//
// A preset that carries a gain is a v3 document whatever its template was. The
// gain changes how the preset renders, so a v1 or v2 loader that quietly
// ignored an unknown field would play the preset at the wrong level instead of
// refusing it; upgrading is rendering-identical for everything else the older
// schemas describe. A preset that needs no gain is left exactly as it was,
// version included -- it stays readable by everything that could read it
// before.
func ApplyOutputGain(p *preset.Preset) (gainDB float64, clamped bool, err error) {
	gainDB, clamped, err = SolveOutputGainDB(p)
	if err != nil {
		return 0, false, err
	}

	if gainDB == 0 {
		return 0, false, nil
	}

	if p.Version != preset.VersionV3 {
		upgraded, upgradeErr := preset.Upgrade(p)
		if upgradeErr != nil {
			return 0, false, fmt.Errorf("upgrade the preset so it can carry an output gain: %w", upgradeErr)
		}

		*p = *upgraded
	}

	p.Parameters.OutputGainDB = gainDB

	return gainDB, clamped, nil
}
