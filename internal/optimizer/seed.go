package optimizer

import (
	"fmt"
	"math"
	"sort"

	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/model"
)

// seedFallbackDecayMs is the half-life a seeded mode gets when the analysis
// could not measure one for its partial and no other partial's can stand in.
const seedFallbackDecayMs = 100.0

// PresetFromAnalysis writes a preset whose modes are the reference's
// measured partials: one mode per partial, strongest first, at the partial's
// frequency, its attack level and its half-life, authored at the template's
// note from a measurement taken at note. Everything that is not a mode --
// the dry mix, the excitation lowpass, the shaper -- is the template's.
//
// It is what the objective's partial term would call a perfect answer, and
// the starting point a search should be given rather than a hand-written
// template. modes caps the count; zero takes every partial the analysis
// listed. A partial whose decay could not be fitted takes the median
// half-life of those that could.
//
// The result is a current-version preset whatever the template's version
// was: a v1 preset holds exactly four modes, and the recording decides how
// many there are here. A v1 template's implicit defaults are made explicit
// by preset.Upgrade, so nothing else about it changes.
func PresetFromAnalysis(template *preset.Preset, measurement *analysis.Measurement, note, modes int) (*preset.Preset, error) {
	if template == nil {
		return nil, fmt.Errorf("template preset cannot be nil")
	}

	if template.Version != preset.CurrentVersion {
		upgraded, err := preset.Upgrade(template)
		if err != nil {
			return nil, err
		}

		template = upgraded
	}

	if measurement == nil || len(measurement.Partials) == 0 {
		return nil, fmt.Errorf("the analysis lists no partials to seed from")
	}

	partials := append([]analysis.Partial(nil), measurement.Partials...)
	if modes > 0 && modes < len(partials) {
		partials = partials[:modes]
	}

	var halfLives []float64

	for _, partial := range partials {
		if partial.HalfLifeMs > 0 && !math.IsNaN(partial.HalfLifeMs) && !math.IsInf(partial.HalfLifeMs, 0) {
			halfLives = append(halfLives, partial.HalfLifeMs)
		}
	}

	fallback := seedFallbackDecayMs

	if len(halfLives) > 0 {
		sort.Float64s(halfLives)
		fallback = halfLives[len(halfLives)/2]
	}

	strongest := math.Inf(-1)

	for _, partial := range partials {
		strongest = math.Max(strongest, seedLevel(partial))
	}

	// The measurement was taken at note; the preset is authored at the
	// template's note. Transposing the other way is dividing the frequency
	// by the ratio and multiplying the decay by it -- the inverse of what
	// model.TransposeToNote does at render time.
	ratio := math.Pow(2, float64(note-template.Note)/12)
	seeded := template.Clone()
	seeded.Parameters.Modes = make([]model.ModeParams, len(partials))

	for i, partial := range partials {
		halfLife := partial.HalfLifeMs
		if !(halfLife > 0) || math.IsInf(halfLife, 0) {
			halfLife = fallback
		}

		frequency := partial.FrequencyHz / ratio

		// The amplitude is what reaches the bank after the lowpass, so the
		// lowpass's loss at this frequency is put back, within the range a
		// mode may carry.
		response := model.ExcitationResponse(seeded.Parameters.FilterFrequency, frequency, 44100)
		amplitude := math.Pow(10, (seedLevel(partial)-strongest)/20)

		if response > 0 {
			amplitude /= response
		}

		seeded.Parameters.Modes[i] = model.ModeParams{
			Amplitude: math.Min(model.AmplitudeMax, amplitude),
			Frequency: frequency,
			DecayMs:   math.Min(model.AuthoredDecayMsMax(template.Note), math.Max(model.DecayMsMin, halfLife*ratio)),
		}
	}

	if err := preset.Validate(seeded); err != nil {
		return nil, err
	}

	return seeded, nil
}

// seedLevel is the level a seeded mode aims for: the attack level, or the
// averaged level where no decay line could be fitted.
func seedLevel(partial analysis.Partial) float64 {
	if math.IsNaN(partial.AttackDB) || math.IsInf(partial.AttackDB, 0) {
		return partial.AmplitudeDB
	}

	return partial.AttackDB
}
