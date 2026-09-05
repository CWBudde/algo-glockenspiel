package pack

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"

	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

// FileMatrix is the transposition matrix a pack run's verification writes.
const FileMatrix = "pack-matrix.csv"

// Scored is one preset's score at every note of a pack.
type Scored struct {
	Name   string
	Note   int
	Scores map[int]float64
	Mean   float64

	// Joint marks a preset fitted against several recordings at once, read from
	// its provenance rather than from its name or its authored note.
	//
	// It has to be read from somewhere, because the joint preset is authored at
	// the median of the pack and therefore occupies a diagonal cell exactly as
	// a per-note preset does. A diagonal that quietly included it would be the
	// comparison scoring itself, and the number it produced -- the price of one
	// preset covering the range -- would be smaller than the truth by however
	// well the joint fit did at that one note.
	Joint bool
}

// Comparison is what the matrix was built to answer.
type Comparison struct {
	// DiagonalMean is the mean over notes of the score reached by the preset
	// fitted to that note. It is the unreachable floor: every note fitted to
	// itself, twenty presets doing the work of one.
	DiagonalMean float64
	DiagonalN    int

	// BestSingle is the lowest row mean among the per-note presets -- the best
	// any one bar's preset manages across the whole range.
	BestSingle     float64
	BestSingleName string

	// JointMean is the joint preset's row mean, NaN when none was scored, and
	// Price is JointMean - DiagonalMean: what one preset covering the range
	// costs against twenty covering it one note each.
	JointMean float64
	Price     float64
}

// Compare reduces the matrix to the numbers the phase turns on.
func Compare(rows []Scored) Comparison {
	out := Comparison{
		DiagonalMean: math.NaN(),
		BestSingle:   math.NaN(),
		JointMean:    math.NaN(),
		Price:        math.NaN(),
	}

	diagonal := 0.0

	for _, row := range rows {
		if row.Joint {
			out.JointMean = row.Mean

			continue
		}

		if score, ok := row.Scores[row.Note]; ok && !math.IsInf(score, 1) && !math.IsNaN(score) {
			diagonal += score
			out.DiagonalN++
		}

		if !math.IsNaN(row.Mean) && (math.IsNaN(out.BestSingle) || row.Mean < out.BestSingle) {
			out.BestSingle = row.Mean
			out.BestSingleName = row.Name
		}
	}

	if out.DiagonalN > 0 {
		out.DiagonalMean = diagonal / float64(out.DiagonalN)
	}

	out.Price = out.JointMean - out.DiagonalMean

	return out
}

// ScorePresets scores every preset against every note of a planned pack.
//
// This is the verification the whole phase turns on. A preset fitted to one bar
// and transposed across the range is what the instrument has always shipped;
// the question is what that costs against a preset fitted to the range at once,
// and the only way to answer it is to score both the same way. So every preset
// here goes through the objective the fit used, at the same notes, against the
// same recordings.
//
// The output gain is not solved first and does not need to be: the objective
// divides the level out of every term in closed form, which is what phase 8.10
// made true and 8.9's clamp depends on. A preset that renders 20 dB quiet
// scores exactly what the same preset at unity scores.
//
// StrictBounds stays false, and that is load-bearing rather than incidental.
// Evaluate scores the *decoded* vector, and DecodeParams clamps into the
// codec's box -- so a preset with a mode outside the default box would be
// scored as a different preset, quietly, with a plausible number. The
// non-strict codec widens its box until it contains the template, and the
// template here is the candidate itself, so nothing is ever clamped.
// TestTheMatrixScoresThePresetItWasGiven pins it.
func ScorePresets(dir string, paths []string, sampleRate, velocity int) ([]Scored, []int, error) {
	manifest, err := ReadManifest(dir)
	if err != nil {
		return nil, nil, err
	}

	if sampleRate == 0 {
		sampleRate = 44100
	}

	if velocity == 0 {
		velocity = 100
	}

	// Every recording is loaded once and scored against every preset, rather
	// than reloaded per preset: twenty presets times twenty notes is four
	// hundred loads of the same twenty files otherwise.
	type loadedNote struct {
		note    int
		samples []float32
	}

	loaded := make([]loadedNote, 0, len(manifest.Jobs))
	notes := make([]int, 0, len(manifest.Jobs))

	for _, job := range manifest.Jobs {
		reference, err := analysis.LoadReference(job.Reference.Path, analysis.LoadOptions{})
		if err != nil {
			return nil, nil, err
		}

		if reference.SampleRate != sampleRate {
			return nil, nil, fmt.Errorf("%s is at %d Hz, not the requested %d",
				job.Reference.Path, reference.SampleRate, sampleRate)
		}

		loaded = append(loaded, loadedNote{note: job.Note, samples: reference.Samples})
		notes = append(notes, job.Note)
	}

	rows := make([]Scored, 0, len(paths))

	for _, path := range paths {
		candidate, err := preset.Load(path)
		if err != nil {
			return nil, nil, err
		}

		row := Scored{
			Name:   filepath.Base(filepath.Dir(path)),
			Note:   candidate.Note,
			Scores: make(map[int]float64, len(loaded)),
			Joint:  candidate.Provenance != nil && len(candidate.Provenance.References) > 0,
		}

		total, counted := 0.0, 0

		for _, entry := range loaded {
			config := optimizer.DefaultObjectiveConfig(manifest.Profile)
			config.Bounds = optimizer.DefaultParamBounds

			objective, err := optimizer.NewObjectiveFunctionWithConfig(
				entry.samples, candidate, sampleRate, entry.note, velocity, config)
			if err != nil {
				return nil, nil, fmt.Errorf("%s at note %d: %w", path, entry.note, err)
			}

			encoded, err := objective.Codec().EncodeParams(&candidate.Parameters)
			if err != nil {
				return nil, nil, fmt.Errorf("%s at note %d: %w", path, entry.note, err)
			}

			score := objective.Evaluate(encoded)
			row.Scores[entry.note] = score

			if !math.IsInf(score, 1) && !math.IsNaN(score) {
				total += score
				counted++
			}
		}

		row.Mean = math.NaN()
		if counted == len(loaded) {
			row.Mean = total / float64(counted)
		}

		rows = append(rows, row)
	}

	return rows, notes, nil
}

// MatrixCSV renders the scored presets as a table: one row per preset, one
// column per note, plus the row mean.
//
// The mean is the number the comparison turns on, and the per-note columns are
// what stop it being the only one: a preset can carry a good mean while being
// useless at one end of the keyboard, and the mean alone would not say so.
func MatrixCSV(rows []Scored, notes []int) [][]string {
	head := []string{"preset", "authored_note", "mean"}
	for _, note := range notes {
		head = append(head, "n"+strconv.Itoa(note))
	}

	out := make([][]string, 0, len(rows)+1)
	out = append(out, head)

	for _, row := range rows {
		record := []string{row.Name, strconv.Itoa(row.Note), formatFloat(row.Mean)}
		for _, note := range notes {
			record = append(record, formatFloat(row.Scores[note]))
		}

		out = append(out, record)
	}

	return out
}
