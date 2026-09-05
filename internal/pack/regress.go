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
//
// This is what the fit produced, note by note, and it is the wrong grouping to
// regress over. See ByRatioCluster.
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

// clusterGapLog2 is the gap in log2(ratio) that separates one partial from the
// next, 0.04 -- about 2.8%, or half a semitone.
//
// It is chosen from the measurement rather than picked round. Within one
// partial the ratio varies by about 1% across the pack: the free-free ratio
// reads 2.77, 2.77, 2.76, 2.76, 2.74 at the first five notes. Between adjacent
// partials the smallest real gap is much larger: c6's analysis holds 5.36 and
// 5.66, which are 5.5% apart. A threshold between 1% and 5.5% separates the two
// cases, and 2.8% sits in the middle of that window on a log scale.
const clusterGapLog2 = 0.04

// Cluster is one partial of the instrument, gathered across the notes it was
// fitted at.
type Cluster struct {
	Rows []ModeRow

	// Notes is how many distinct notes contributed a mode. A cluster present at
	// four notes of twenty is not a partial of the instrument, it is four fits
	// that happened to land near each other, and the report says so rather than
	// regressing it.
	Notes int
}

// ByRatioCluster groups rows by the partial they are, rather than by the
// position they occupy in their own note's mode list.
//
// The distinction is not pedantic and it is why this function exists. The fits
// hold between four and nine modes depending on how many partials the analysis
// found -- g6 came back with four, ds6 with nine -- so "mode 3" is the fourth
// strongest partial of whatever that note happened to have, and at two notes
// with different mode counts it is very often a different piece of the bar's
// physics. Regressing decay on pitch within a mode index therefore pools
// measurements of different partials and calls the mixture key tracking. The
// first five notes already show it: mode 0's ratio to the fundamental ranges
// from 1.001 to 3.531, because c6's fit abandoned its fundamental and the
// others did not.
//
// Clustering is single-linkage on log2(ratio) with a fixed gap, which is the
// right shape for this data rather than a simplification of k-means: the number
// of partials is not known in advance, the clusters are far narrower than the
// gaps between them, and a gap rule needs no seeding and gives the same answer
// every time. Returned in ascending ratio order.
func ByRatioCluster(rows []ModeRow) []Cluster {
	usable := make([]ModeRow, 0, len(rows))

	for _, row := range rows {
		if row.Ratio > 0 && !math.IsInf(row.Ratio, 0) && !math.IsNaN(row.Ratio) {
			usable = append(usable, row)
		}
	}

	if len(usable) == 0 {
		return nil
	}

	sort.Slice(usable, func(i, j int) bool { return usable[i].Ratio < usable[j].Ratio })

	clusters := []Cluster{{Rows: []ModeRow{usable[0]}}}

	for _, row := range usable[1:] {
		current := &clusters[len(clusters)-1]
		previous := current.Rows[len(current.Rows)-1]

		if math.Log2(row.Ratio)-math.Log2(previous.Ratio) > clusterGapLog2 {
			clusters = append(clusters, Cluster{Rows: []ModeRow{row}})

			continue
		}

		current.Rows = append(current.Rows, row)
	}

	for i := range clusters {
		clusters[i].Notes = countNotes(clusters[i].Rows)
	}

	return clusters
}

// Report renders the note-versus-partial regression as Markdown.
//
// Two questions, one table each. Does the decay follow the model's law -- and
// what would a fitted exponent buy over it. And is the modal structure a
// ratio-scale of one bar, which is what transposing a single preset assumes.
//
// Both tables group by ratio cluster rather than by mode index, for the reason
// ByRatioCluster gives: the fits hold different numbers of modes at different
// notes, so a mode index is a position in one note's list and not a partial of
// the instrument. The `notes` column is the one to read first -- a cluster
// found at a handful of the pack's notes is not a partial, and every number
// beside it is a coincidence rather than a measurement.
func Report(rows []ModeRow, packName string) string {
	var out strings.Builder

	clusters := ByRatioCluster(rows)
	total := countNotes(rows)

	fmt.Fprintf(&out, "# Note versus partial, %s\n\n", packName)
	fmt.Fprintf(&out, "%d fitted modes across %d notes, in %d ratio clusters.\n\n",
		len(rows), total, len(clusters))

	out.WriteString("Clusters are single-linkage on log2 of the ratio to the fundamental, split at a gap of\n")
	out.WriteString("0.04 (about half a semitone). A partial's ratio varies by about 1% across the pack and\n")
	out.WriteString("the closest real partials sit 5% apart, so the threshold falls between the two.\n\n")

	out.WriteString("## Decay against pitch\n\n")
	out.WriteString("`beta` is the key-tracking exponent the slope implies: transposition divides a decay by\n")
	out.WriteString("`ratio^beta`, and the model hard-codes `beta = 1`. `fitted` and `pinned` are the residual\n")
	out.WriteString("scatter in octaves around the fitted line and around the model's own law; the objective\n")
	out.WriteString("scores `partial_decay_octaves` against a norm of 0.5, so an octave of scatter is two norms.\n\n")
	out.WriteString("| ratio | notes | of | beta | R2 | fitted sd (oct) | pinned sd (oct) | what beta buys |\n")
	out.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")

	for _, cluster := range clusters {
		notes, decays := columns(cluster.Rows, func(r ModeRow) float64 { return r.DecayMs })
		ratios := ratiosOf(cluster.Rows)

		fit, ok := FitLog2(notes, decays)
		if !ok {
			fmt.Fprintf(&out, "| %.2f | %d | %d | — | — | — | — | too few notes |\n",
				mean(ratios), cluster.Notes, total)

			continue
		}

		fmt.Fprintf(&out, "| %.2f | %d | %d | %+.2f | %.3f | %.3f | %.3f | %.3f |\n",
			mean(ratios), cluster.Notes, total, fit.Exponent(), fit.R2,
			fit.ResidualSD, fit.PinnedSD, fit.PinnedSD-fit.ResidualSD)
	}

	out.WriteString("\n## Modal structure against pitch\n\n")
	out.WriteString("Each cluster's ratio to its note's own fundamental. Transposing one preset across the\n")
	out.WriteString("keyboard assumes these are constant: every partial is a fixed multiple of the\n")
	out.WriteString("fundamental, at every note. A ratio that drifts with pitch is structure no single preset\n")
	out.WriteString("can carry, and a cluster that appears at only some notes is structure it cannot even\n")
	out.WriteString("see.\n\n")
	out.WriteString("| ratio | notes | of | sd | min | max | slope /octave |\n")
	out.WriteString("| --- | --- | --- | --- | --- | --- | --- |\n")

	for _, cluster := range clusters {
		notes, ratios := columns(cluster.Rows, func(r ModeRow) float64 { return r.Ratio })

		slope := "—"
		if fit, ok := FitLog2(notes, ratios); ok {
			slope = fmt.Sprintf("%+.3f", fit.Slope*12)
		}

		fmt.Fprintf(&out, "| %.3f | %d | %d | %.3f | %.3f | %.3f | %s |\n",
			mean(ratios), cluster.Notes, total,
			stddev(ratios), minOf(ratios), maxOf(ratios), slope)
	}

	return out.String()
}

func ratiosOf(rows []ModeRow) []float64 {
	out := make([]float64, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Ratio)
	}

	return out
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
