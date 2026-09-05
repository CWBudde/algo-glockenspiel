package pack

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

// Collect reads every finished note of a pack run and writes the two tables the
// regression reads.
//
// There are two rather than one because the question has two shapes. The wide
// table is one row per note and is what a person reads; the long table is one
// row per (note, mode) and is what a regression of half-life or frequency ratio
// against pitch is actually run on. Deriving one from the other is a reshape
// either way, and the mode count varies from note to note here -- the hollandm
// pack measures between two and nine partials -- so a wide table would need as
// many mode columns as the richest note and leave the rest empty.
func Collect(dir string) (notes int, err error) {
	manifest, err := ReadManifest(dir)
	if err != nil {
		return 0, err
	}

	wide := make([][]string, 0, len(manifest.Jobs)+1)
	long := make([][]string, 0, len(manifest.Jobs)*8+1)

	wide = append(wide, wideHeader())
	long = append(long, longHeader())

	for _, job := range manifest.Jobs {
		summary, fitted, err := readJob(filepath.Join(dir, job.Dir))
		if err != nil {
			return 0, fmt.Errorf("note %s: %w", job.Name, err)
		}

		if summary == nil {
			return 0, fmt.Errorf(
				"note %s (MIDI %d) has not finished: %s is missing, so the table would be a table of a partial run",
				job.Name, job.Note, fitrun.FileResult)
		}

		wide = append(wide, wideRow(manifest, job, summary, fitted))
		long = append(long, longRows(job, fitted)...)

		notes++
	}

	if err := writeCSV(filepath.Join(dir, FileResults), wide); err != nil {
		return 0, err
	}

	if err := writeCSV(filepath.Join(dir, FileModeResults), long); err != nil {
		return 0, err
	}

	return notes, nil
}

// readJob loads a note's summary and the preset it produced. A missing summary
// is not an error here: Collect reports which note is unfinished, which is more
// use than a file-not-found on a path.
func readJob(dir string) (*fitrun.Summary, *preset.Preset, error) {
	raw, err := os.ReadFile(filepath.Join(dir, fitrun.FileResult))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}

		return nil, nil, err
	}

	var summary fitrun.Summary

	if err := json.Unmarshal(raw, &summary); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", fitrun.FileResult, err)
	}

	fitted, err := preset.Load(filepath.Join(dir, fitrun.FilePreset))
	if err != nil {
		return nil, nil, err
	}

	return &summary, fitted, nil
}

func wideHeader() []string {
	head := []string{
		"note", "note_name", "cents_off", "reference_sha256",
		"score", "measured_weight", "matched", "reference_partials", "seeded_modes",
		"dimension", "pinned", "output_gain_db",
		"evaluations", "iterations", "restarts", "stop_reason", "converged",
		"elapsed_s", "evals_per_second", "seed", "revision",
	}

	for _, term := range optimizer.Terms() {
		head = append(head, string(term))
	}

	return head
}

func wideRow(manifest *Manifest, job Job, summary *fitrun.Summary, fitted *preset.Preset) []string {
	perSecond := 0.0
	if summary.ElapsedSeconds > 0 {
		perSecond = float64(summary.Evaluations) / summary.ElapsedSeconds
	}

	profile, _ := optimizer.ProfileFor(manifest.Profile)

	row := []string{
		strconv.Itoa(job.Note),
		job.Name,
		formatFloat(job.Cents),
		job.Reference.SHA256,
		formatFloat(summary.Score),
		formatFloat(measuredWeight(summary.Terms, profile)),
		strconv.Itoa(summary.Matched),
		strconv.Itoa(summary.ReferencePartials),
		strconv.Itoa(summary.SeededModes),
		strconv.Itoa(summary.Dimension),
		strconv.Itoa(summary.Pinned),
		formatFloat(fitted.Parameters.OutputGainDB),
		strconv.Itoa(summary.Evaluations),
		strconv.Itoa(summary.Iterations),
		strconv.Itoa(summary.Restarts),
		summary.StopReason,
		strconv.FormatBool(summary.Converged),
		formatFloat(summary.ElapsedSeconds),
		formatFloat(perSecond),
		strconv.FormatInt(summary.Seed, 10),
		summary.Identity.Revision,
	}

	for _, term := range optimizer.Terms() {
		row = append(row, formatFloat(summary.Terms.Value(term)))
	}

	return row
}

func longHeader() []string {
	return []string{
		"note", "note_name", "mode_index", "frequency_hz", "ratio_to_fundamental",
		"decay_ms", "amplitude",
	}
}

// longRows renders one row per fitted mode.
//
// ratio_to_fundamental divides by the fitted preset's own lowest mode rather
// than by the recording's measured fundamental, so the column describes the
// model's modal structure rather than mixing the model's numerator with the
// measurement's denominator. The modes arrive sorted ascending by frequency --
// the codec holds them that way to kill the permutation symmetry -- so mode k
// is the k-th partial at every note, which is what makes a regression over the
// index meaningful.
func longRows(job Job, fitted *preset.Preset) [][]string {
	modes := fitted.Parameters.Modes
	if len(modes) == 0 {
		return nil
	}

	fundamental := modes[0].Frequency
	rows := make([][]string, 0, len(modes))

	for i, mode := range modes {
		ratio := math.NaN()
		if fundamental > 0 {
			ratio = mode.Frequency / fundamental
		}

		rows = append(rows, []string{
			strconv.Itoa(job.Note),
			job.Name,
			strconv.Itoa(i),
			formatFloat(mode.Frequency),
			formatFloat(ratio),
			formatFloat(mode.DecayMs),
			formatFloat(mode.Amplitude),
		})
	}

	return rows
}

// measuredWeight is the share of the profile's weight the terms this fit could
// actually measure account for.
//
// It is worth a column of its own because Score renormalises over measured
// terms only, so two notes can carry the same score while one of them was
// scored on eight terms and the other on eleven. Across a pack that difference
// is systematic rather than random -- the top notes hold two or three partials
// against the bottom's nine -- so a reader comparing scores down the table
// needs to see it.
func measuredWeight(terms optimizer.Metrics, profile optimizer.Profile) float64 {
	total := 0.0

	for _, term := range optimizer.Terms() {
		if math.IsNaN(terms.Value(term)) {
			continue
		}

		total += profile.Weights[term]
	}

	return total
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', 17, 64)
}

func writeCSV(path string, rows [][]string) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %q: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	writer := csv.NewWriter(file)

	if err := writer.WriteAll(rows); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}

	writer.Flush()

	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush %q: %w", path, err)
	}

	return file.Close()
}
