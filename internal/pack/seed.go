package pack

import (
	"fmt"
	"math"
	"sort"

	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/model"
)

// DefaultSeedCoverage is the share of a pack's notes a partial must be fitted
// at before it is seeded into the joint preset.
//
// Half, because a partial found at fewer notes than that is not a partial of
// the instrument: the pack's top notes hold two or three partials against the
// bottom's nine, so anything real in the upper structure appears across the
// lower half at least. Seeding a mode from three notes of twenty spends a
// dimension on a coincidence, and the joint fit has to carry that dimension at
// all twenty.
const DefaultSeedCoverage = 0.5

// PresetFromClusters builds the joint fit's starting preset from every note of
// the pack at once, rather than from the single recording at the authored note.
//
// This is what generalising the pack means. A partial's ratio to the
// fundamental is the same physical property of the bar at every note, so
// twenty fits of it are twenty observations of one number and their mean is a
// far better estimate than any one of them. Seeding from the authored note's
// analysis alone throws nineteen of those away, and does it at whichever note
// happens to sit in the middle.
//
// The cluster rows are fitted modes rather than measured partials, so they are
// already in the model's own coordinates and are used directly. Routing them
// through PresetFromAnalysis would put the excitation lowpass's loss back a
// second time, since the fitted amplitudes already have it.
//
// Decays are averaged geometrically after each note's decay is expressed at the
// authored note, which is the inverse of what TransposeToNote will do at render
// time. Geometrically because a decay is a ratio-scale quantity: the mean of
// 100 ms and 400 ms is 200 ms, not 250 ms, and an arithmetic mean would let one
// long-tailed bar drag the whole mode.
func PresetFromClusters(
	template *preset.Preset, clusters []Cluster, authoredNote, totalNotes int, coverage float64,
) (*preset.Preset, []Dropped, error) {
	if template == nil {
		return nil, nil, fmt.Errorf("template preset cannot be nil")
	}

	if totalNotes <= 0 {
		return nil, nil, fmt.Errorf("the pack has no notes to seed from")
	}

	if coverage <= 0 {
		coverage = DefaultSeedCoverage
	}

	seeded := template.Clone()
	if seeded.Version != preset.CurrentVersion {
		upgraded, err := preset.Upgrade(seeded)
		if err != nil {
			return nil, nil, err
		}

		seeded = upgraded
	}

	if authoredNote != 0 && authoredNote != seeded.Note {
		model.TransposeToNote(&seeded.Parameters, seeded.Note, authoredNote)
		seeded.Note = authoredNote
	}

	keytrack := seeded.Parameters.ResolvedDecayKeytrack()
	fundamental := seeded.Parameters.BaseFrequency
	needed := int(math.Ceil(coverage * float64(totalNotes)))

	modes := make([]model.ModeParams, 0, len(clusters))

	var dropped []Dropped

	for _, cluster := range clusters {
		ratio := meanRatio(cluster)

		if cluster.Notes < needed {
			dropped = append(dropped, Dropped{Ratio: ratio, Notes: cluster.Notes})

			continue
		}

		mode, ok := clusterMode(cluster, seeded.Note, fundamental, keytrack)
		if !ok {
			dropped = append(dropped, Dropped{Ratio: ratio, Notes: cluster.Notes})

			continue
		}

		modes = append(modes, mode)
	}

	if len(modes) == 0 {
		return nil, dropped, fmt.Errorf(
			"no partial was fitted at %d of the pack's %d notes, so there is nothing to seed from",
			needed, totalNotes)
	}

	sort.Slice(modes, func(i, j int) bool { return modes[i].Frequency < modes[j].Frequency })

	seeded.Parameters.Modes = modes

	if err := preset.Validate(seeded); err != nil {
		return nil, dropped, fmt.Errorf("the pooled seed does not validate: %w", err)
	}

	return seeded, dropped, nil
}

// Dropped is a cluster the seed left out, and how many notes held it.
//
// They are returned rather than counted because which partial was dropped is
// the interesting half. A pack whose fits place the fundamental at 1.001 at
// some notes and 1.09 at others splits it into two thin clusters and drops
// both, and a seed with no fundamental is wrong in a way a count of twelve
// does not convey.
type Dropped struct {
	Ratio float64
	Notes int
}

func meanRatio(cluster Cluster) float64 {
	total := 0.0
	for _, row := range cluster.Rows {
		total += math.Log2(row.Ratio)
	}

	if len(cluster.Rows) == 0 {
		return math.NaN()
	}

	return math.Exp2(total / float64(len(cluster.Rows)))
}

// clusterMode averages one cluster into a single mode at the authored note.
func clusterMode(cluster Cluster, authoredNote int, fundamental, keytrack float64) (model.ModeParams, bool) {
	var (
		ratios     float64
		logDecays  float64
		amplitudes float64
		counted    int
	)

	for _, row := range cluster.Rows {
		if !(row.Ratio > 0) || !(row.DecayMs > 0) {
			continue
		}

		// Each note's decay expressed at the authored note: the render-time
		// law divides by ratio^keytrack going up, so coming back divides the
		// other way.
		ratio := math.Pow(2, float64(row.Note-authoredNote)/12)
		atAuthored := row.DecayMs * math.Pow(ratio, keytrack)

		if !(atAuthored > 0) || math.IsInf(atAuthored, 0) {
			continue
		}

		ratios += math.Log2(row.Ratio)
		logDecays += math.Log2(atAuthored)
		amplitudes += row.Amplitude
		counted++
	}

	if counted == 0 {
		return model.ModeParams{}, false
	}

	frequency := fundamental * math.Exp2(ratios/float64(counted))
	decay := math.Exp2(logDecays / float64(counted))
	amplitude := amplitudes / float64(counted)

	// The averages are means of values that were each inside the model's range,
	// but a geometric mean of decays expressed at a distant note can still land
	// outside it, and a seed the model refuses is worse than a clamped one.
	frequency = math.Min(math.Max(frequency, model.FrequencyMinHz), model.FrequencyMaxHz)
	decay = math.Min(math.Max(decay, model.DecayMsMin), model.DecayMsValidationMax)
	amplitude = math.Min(math.Max(amplitude, model.AmplitudeMin), model.AmplitudeMax)

	return model.ModeParams{Frequency: frequency, DecayMs: decay, Amplitude: amplitude}, true
}
