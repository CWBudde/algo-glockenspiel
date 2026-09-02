package optimizer

import (
	"math"

	"github.com/cwbudde/algo-glockenspiel/model"
)

// partialVisibilityKneeMs is the half-life below which a mode's line is wider
// than the analysis window's main lobe, so that its averaged level falls a
// second 20 dB per decade of half-life on top of the first. Measured on both
// references against what `glockenspiel analyze` lists: the averaged level
// of a partial relative to its attack level tracks 20 log10 of the half-life
// from 600 ms down to about 100 ms and 40 log10 below 40 ms.
const partialVisibilityKneeMs = 100.0

// averagedLevelOffsetDB is how far below its attack level a partial with the
// half-life reads in an averaged spectrum, up to a constant that cancels in
// any comparison between partials of one signal.
func averagedLevelOffsetDB(halfLifeMs float64) float64 {
	if halfLifeMs <= 0 {
		return math.Inf(-1)
	}

	offset := 20 * math.Log10(halfLifeMs/partialVisibilityKneeMs)
	if halfLifeMs < partialVisibilityKneeMs {
		offset *= 2
	}

	return offset
}

// modelPartials lists the partials a parameter set produces at a note, from
// the parameters alone: one per mode and one per harmonic riding on it, at
// the transposed frequency, with the decay the transposition leaves and the
// level the mode's amplitude reaches once the excitation lowpass has scaled
// the strike. Levels are relative to the strongest.
//
// A mode that dies within a few milliseconds is a click, not a partial: no
// analysis of the render would list it and no listener would hear a pitch.
// Such modes are left out, by the same rule the analysis applies to the
// reference -- a partial is listed when its averaged level is within
// minLevelDB of the strongest -- with the averaged level predicted from the
// attack level and the half-life.
//
// The shaper and the dry mix are not modelled: the shaper's products depend
// on the whole excitation waveform and the dry mix is a click, not a
// partial. Both reach the spectral terms through the render.
func modelPartials(params *model.BarParams, presetNote, note, sampleRate int, minLevelDB float64) []modelPartial {
	if params == nil || sampleRate <= 0 {
		return nil
	}

	transposed := params.Clone()
	model.TransposeToNote(&transposed, presetNote, note)

	nyquist := float64(sampleRate) / 2
	partials := make([]modelPartial, 0, len(transposed.Modes))
	averaged := make([]float64, 0, len(transposed.Modes))
	strongestAveraged := math.Inf(-1)

	add := func(frequencyHz, amplitude, halfLifeMs float64) {
		amplitude = math.Abs(amplitude)
		if amplitude <= 0 || frequencyHz <= 0 || frequencyHz >= nyquist || halfLifeMs <= 0 {
			return
		}

		level := 20 * math.Log10(amplitude*model.ExcitationResponse(transposed.FilterFrequency, frequencyHz, float64(sampleRate)))
		if math.IsInf(level, 0) || math.IsNaN(level) {
			return
		}

		partials = append(partials, modelPartial{frequencyHz: frequencyHz, levelDB: level, halfLifeMs: halfLifeMs})
		averaged = append(averaged, level+averagedLevelOffsetDB(halfLifeMs))
		strongestAveraged = math.Max(strongestAveraged, averaged[len(averaged)-1])
	}

	for _, mode := range transposed.Modes {
		add(mode.Frequency, mode.Amplitude, mode.DecayMs)

		for k, gain := range mode.Harmonics {
			add(mode.Frequency*float64(k+2), mode.Amplitude*gain, mode.DecayMs)
		}
	}

	if minLevelDB > 0 {
		minLevelDB = -minLevelDB
	}

	visible := partials[:0]
	strongest := math.Inf(-1)

	for i, partial := range partials {
		if averaged[i] < strongestAveraged+minLevelDB {
			continue
		}

		visible = append(visible, partial)
		strongest = math.Max(strongest, partial.levelDB)
	}

	for i := range visible {
		visible[i].levelDB -= strongest
	}

	return visible
}
