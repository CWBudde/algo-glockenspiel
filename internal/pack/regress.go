package pack

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ModeRow is one fitted mode of one note, read back from pack-modes.csv.
type ModeRow struct {
	Note      int
	Name      string
	Index     int
	Frequency float64
	Ratio     float64
	DecayMs   float64
	Amplitude float64
}

// Fit is a least-squares line through log2(y) against MIDI note, with the
// scatter it leaves behind.
type Fit struct {
	N         int
	Slope     float64 // per semitone, in log2 units
	Intercept float64
	R2        float64

	// ResidualSD is the root-mean-square residual in log2 units, i.e. in
	// octaves. It is the number that matters more than the slope: it is what no
	// key-tracking law can remove, because it is the difference between one bar
	// and the next rather than a function of pitch.
	ResidualSD float64

	// PinnedSD is the same scatter against the model's own law, slope -1/12 per
	// semitone, which is what transposition applies today. Comparing the two
	// says what a fitted exponent would actually buy.
	PinnedSD float64
}

// Exponent is the decay key-tracking exponent the slope implies: decay divides
// by ratio^beta under transposition, and the model hard-codes beta = 1.
func (f Fit) Exponent() float64 { return -12 * f.Slope }

// ReadModes loads a pack run's pack-modes.csv.
func ReadModes(path string) ([]ModeRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}

	if len(records) < 2 {
		return nil, fmt.Errorf("%q holds no rows", path)
	}

	column := make(map[string]int, len(records[0]))
	for i, name := range records[0] {
		column[name] = i
	}

	for _, want := range []string{"note", "note_name", "mode_index", "frequency_hz", "ratio_to_fundamental", "decay_ms", "amplitude"} {
		if _, ok := column[want]; !ok {
			return nil, fmt.Errorf("%q has no %s column", path, want)
		}
	}

	rows := make([]ModeRow, 0, len(records)-1)

	for line, record := range records[1:] {
		var row ModeRow

		var err error

		if row.Note, err = strconv.Atoi(record[column["note"]]); err != nil {
			return nil, fmt.Errorf("%q line %d: note: %w", path, line+2, err)
		}

		if row.Index, err = strconv.Atoi(record[column["mode_index"]]); err != nil {
			return nil, fmt.Errorf("%q line %d: mode_index: %w", path, line+2, err)
		}

		row.Name = record[column["note_name"]]

		for _, field := range []struct {
			name string
			into *float64
		}{
			{"frequency_hz", &row.Frequency},
			{"ratio_to_fundamental", &row.Ratio},
			{"decay_ms", &row.DecayMs},
			{"amplitude", &row.Amplitude},
		} {
			if *field.into, err = strconv.ParseFloat(record[column[field.name]], 64); err != nil {
				return nil, fmt.Errorf("%q line %d: %s: %w", path, line+2, field.name, err)
			}
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// FitLog2 regresses log2(y) on the MIDI note.
//
// Log2 because that is the unit transposition works in and the unit the
// objective's decay term is scored in: a residual of 0.5 here is half an octave
// of half-life, which is exactly one norm of partial_decay_octaves.
func FitLog2(notes []int, values []float64) (Fit, bool) {
	pitches := make([]float64, 0, len(notes))
	logs := make([]float64, 0, len(notes))

	for i := range notes {
		if values[i] > 0 && !math.IsInf(values[i], 0) && !math.IsNaN(values[i]) {
			pitches = append(pitches, float64(notes[i]))
			logs = append(logs, math.Log2(values[i]))
		}
	}

	// Three points is the fewest that can leave a residual at all; two define a
	// line exactly and would report a scatter of zero, which is a claim about
	// the fit rather than about the bars.
	if len(pitches) < 3 {
		return Fit{N: len(pitches)}, false
	}

	meanX, meanY := mean(pitches), mean(logs)
	sxx, sxy, syy := 0.0, 0.0, 0.0

	for i := range pitches {
		sxx += (pitches[i] - meanX) * (pitches[i] - meanX)
		sxy += (pitches[i] - meanX) * (logs[i] - meanY)
		syy += (logs[i] - meanY) * (logs[i] - meanY)
	}

	if sxx == 0 {
		return Fit{N: len(pitches)}, false
	}

	fit := Fit{N: len(pitches), Slope: sxy / sxx}
	fit.Intercept = meanY - fit.Slope*meanX

	residual, pinned := 0.0, 0.0

	// The model's own law expressed as a line: decay divides by the full
	// transposition ratio, so log2(decay) falls by exactly 1/12 per semitone.
	// Its intercept is the one that centres it on this data, so the comparison
	// is slope against slope rather than slope against an arbitrary offset.
	pinnedIntercept := meanY + meanX/12

	for i := range pitches {
		r := logs[i] - (fit.Slope*pitches[i] + fit.Intercept)
		residual += r * r

		p := logs[i] - (-pitches[i]/12 + pinnedIntercept)
		pinned += p * p
	}

	fit.ResidualSD = math.Sqrt(residual / float64(len(pitches)))
	fit.PinnedSD = math.Sqrt(pinned / float64(len(pitches)))

	if syy > 0 {
		fit.R2 = 1 - residual/syy
	}

	return fit, true
}

// ByModeIndex groups rows by their mode index, ascending.
func ByModeIndex(rows []ModeRow) ([]int, map[int][]ModeRow) {
	groups := make(map[int][]ModeRow)
	for _, row := range rows {
		groups[row.Index] = append(groups[row.Index], row)
	}

	indices := make([]int, 0, len(groups))
	for index := range groups {
		indices = append(indices, index)
	}

	sort.Ints(indices)

	return indices, groups
}

// Report renders the note-versus-partial regression as Markdown.
//
// Two questions, one table each. Does the decay follow the model's law -- and
// what would a fitted exponent buy over it. And is the modal structure a
// ratio-scale of one bar, which is what transposing a single preset assumes.
func Report(rows []ModeRow, packName string) string {
	var out strings.Builder

	indices, groups := ByModeIndex(rows)

	fmt.Fprintf(&out, "# Note versus partial, %s\n\n", packName)
	fmt.Fprintf(&out, "%d fitted modes across %d notes.\n\n", len(rows), countNotes(rows))

	out.WriteString("## Decay against pitch\n\n")
	out.WriteString("`beta` is the key-tracking exponent the slope implies: transposition divides a decay by\n")
	out.WriteString("`ratio^beta`, and the model hard-codes `beta = 1`. `fitted` and `pinned` are the residual\n")
	out.WriteString("scatter in octaves around the fitted line and around the model's own law; the objective\n")
	out.WriteString("scores `partial_decay_octaves` against a norm of 0.5, so an octave of scatter is two norms.\n\n")
	out.WriteString("| mode | n | beta | R2 | fitted sd (oct) | pinned sd (oct) | what beta buys |\n")
	out.WriteString("| --- | --- | --- | --- | --- | --- | --- |\n")

	for _, index := range indices {
		group := groups[index]
		notes, decays := columns(group, func(r ModeRow) float64 { return r.DecayMs })

		fit, ok := FitLog2(notes, decays)
		if !ok {
			fmt.Fprintf(&out, "| %d | %d | — | — | — | — | too few notes |\n", index, fit.N)

			continue
		}

		fmt.Fprintf(&out, "| %d | %d | %+.2f | %.3f | %.3f | %.3f | %.3f |\n",
			index, fit.N, fit.Exponent(), fit.R2, fit.ResidualSD, fit.PinnedSD,
			fit.PinnedSD-fit.ResidualSD)
	}

	out.WriteString("\n## Modal structure against pitch\n\n")
	out.WriteString("Each mode's ratio to its note's own fundamental. Transposing one preset across the\n")
	out.WriteString("keyboard assumes these are constant: every mode is a fixed multiple of the first, at\n")
	out.WriteString("every note. A ratio that drifts with pitch is structure no single preset can carry.\n\n")
	out.WriteString("| mode | n | mean ratio | sd | min | max | slope /octave |\n")
	out.WriteString("| --- | --- | --- | --- | --- | --- | --- |\n")

	for _, index := range indices {
		group := groups[index]
		notes, ratios := columns(group, func(r ModeRow) float64 { return r.Ratio })

		if len(ratios) < 2 {
			continue
		}

		fit, ok := FitLog2(notes, ratios)

		slope := "—"
		if ok {
			slope = fmt.Sprintf("%+.3f", fit.Slope*12)
		}

		fmt.Fprintf(&out, "| %d | %d | %.3f | %.3f | %.3f | %.3f | %s |\n",
			index, len(ratios), mean(ratios), stddev(ratios), minOf(ratios), maxOf(ratios), slope)
	}

	return out.String()
}

func columns(rows []ModeRow, pick func(ModeRow) float64) ([]int, []float64) {
	notes := make([]int, 0, len(rows))
	values := make([]float64, 0, len(rows))

	for _, row := range rows {
		notes = append(notes, row.Note)
		values = append(values, pick(row))
	}

	return notes, values
}

func countNotes(rows []ModeRow) int {
	seen := make(map[int]struct{}, len(rows))
	for _, row := range rows {
		seen[row.Note] = struct{}{}
	}

	return len(seen)
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}

	total := 0.0
	for _, value := range values {
		total += value
	}

	return total / float64(len(values))
}

func stddev(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}

	average := mean(values)
	total := 0.0

	for _, value := range values {
		total += (value - average) * (value - average)
	}

	return math.Sqrt(total / float64(len(values)))
}

func minOf(values []float64) float64 {
	out := math.Inf(1)
	for _, value := range values {
		out = math.Min(out, value)
	}

	return out
}

func maxOf(values []float64) float64 {
	out := math.Inf(-1)
	for _, value := range values {
		out = math.Max(out, value)
	}

	return out
}

// WriteReport writes the regression report beside the tables it read.
func WriteReport(dir, packName string) (string, error) {
	rows, err := ReadModes(filepath.Join(dir, FileModeResults))
	if err != nil {
		return "", err
	}

	body := Report(rows, packName)

	path := filepath.Join(dir, "regression.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", fmt.Errorf("write %q: %w", path, err)
	}

	return body, nil
}
