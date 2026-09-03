package campaign

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// Row is one job's line of results.csv, one field per column in column order.
//
// The column set is a contract between collect and analyze: analyze refuses a
// file whose header differs, because a silently renamed or reordered column
// would turn one term's numbers into another's without anything failing.
type Row struct {
	Design            string
	Arm               string
	Block             int
	Seed              int64
	Job               string
	Engine            string
	Covariance        string
	Lambda            int
	Population        int
	RestartsPlanned   int
	Budget            int
	Score             float64
	ScoredEvaluations int
	FinalScore        float64
	Evaluations       int
	Iterations        int
	Restarts          int
	StopReason        string
	Converged         bool
	ElapsedS          float64
	Pinned            int
	Dimension         int
	Matched           int
	Terms             map[optimizer.Term]float64
	MayflyVersion     string
	CMAESVersion      string
	Revision          string
}

// Header is results.csv's header, in the order the columns are written.
func Header() []string {
	header := []string{
		"design", "arm", "block", "seed", "job", "engine", "covariance", "lambda", "population",
		"restarts_planned", "budget", "score", "scored_evaluations", "final_score", "evaluations",
		"iterations", "restarts", "stop_reason", "converged", "elapsed_s", "pinned", "dimension", "matched",
	}

	for _, term := range optimizer.Terms() {
		header = append(header, string(term))
	}

	return append(header, "mayfly_version", "cmaes_version", "revision")
}

// formatFloat writes a float with enough digits to read back unchanged. A
// campaign's numbers are compared against numbers collected months earlier, so
// a rounded column would make two identical runs look different.
func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', 17, 64)
}

// record renders one row. The engine-specific columns are left empty rather
// than written as zero, because a zero population is a claim about a run and
// an empty cell is the absence of one.
func (r Row) record() []string {
	lambda, population := "", ""

	if r.Engine == fitrun.EngineCMAES {
		lambda = strconv.Itoa(r.Lambda)
	}

	if r.Engine == fitrun.EngineMayfly {
		population = strconv.Itoa(r.Population)
	}

	out := []string{
		r.Design, r.Arm, strconv.Itoa(r.Block), strconv.FormatInt(r.Seed, 10), r.Job, r.Engine,
		r.Covariance, lambda, population, strconv.Itoa(r.RestartsPlanned), strconv.Itoa(r.Budget),
		formatFloat(r.Score), strconv.Itoa(r.ScoredEvaluations), formatFloat(r.FinalScore),
		strconv.Itoa(r.Evaluations), strconv.Itoa(r.Iterations), strconv.Itoa(r.Restarts),
		r.StopReason, strconv.FormatBool(r.Converged), formatFloat(r.ElapsedS),
		strconv.Itoa(r.Pinned), strconv.Itoa(r.Dimension), strconv.Itoa(r.Matched),
	}

	for _, term := range optimizer.Terms() {
		out = append(out, formatFloat(r.Terms[term]))
	}

	return append(out, r.MayflyVersion, r.CMAESVersion, r.Revision)
}

// WriteResults writes results.csv. It is the inverse of ReadResults.
func WriteResults(path string, rows []Row) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create results %q: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	writer := csv.NewWriter(file)

	if err := writer.Write(Header()); err != nil {
		return fmt.Errorf("write results header: %w", err)
	}

	for _, row := range rows {
		if err := writer.Write(row.record()); err != nil {
			return fmt.Errorf("write results row %q: %w", row.Job, err)
		}
	}

	writer.Flush()

	if err := writer.Error(); err != nil {
		return fmt.Errorf("write results %q: %w", path, err)
	}

	return file.Close()
}

// ReadResults parses results.csv back into rows.
//
// It refuses a header that is not exactly the contract's, and it latches the
// first conversion failure rather than substituting a zero: a hand-edited file
// that parses as zeros would be analysed as a set of perfect scores.
func ReadResults(path string) ([]Row, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open results %q: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = len(Header())

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse results %q: %w", path, err)
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("results %q is empty, want the header row", path)
	}

	if err := checkHeader(path, records[0]); err != nil {
		return nil, err
	}

	rows := make([]Row, 0, len(records)-1)

	for index, record := range records[1:] {
		row, err := parseRow(record)
		if err != nil {
			return nil, fmt.Errorf("results %q line %d: %w", path, index+2, err)
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// checkHeader compares the file's header against the contract's.
func checkHeader(path string, record []string) error {
	want := Header()

	if len(record) != len(want) {
		return fmt.Errorf("results %q has %d columns, want %d", path, len(record), len(want))
	}

	for index, name := range want {
		if record[index] != name {
			return fmt.Errorf("results %q column %d is %q, want %q", path, index+1, record[index], name)
		}
	}

	return nil
}

// rowParser converts one record, remembering the first failure. Reporting only
// the first keeps the message about the cell that actually broke instead of
// the cascade behind it.
type rowParser struct {
	record []string
	err    error
}

func (p *rowParser) text(index int) string {
	return p.record[index]
}

func (p *rowParser) fail(index int, err error) {
	if p.err == nil {
		p.err = fmt.Errorf("column %q (%d) is %q: %w", Header()[index], index+1, p.record[index], err)
	}
}

func (p *rowParser) integer(index int) int {
	text := p.record[index]
	if text == "" {
		return 0
	}

	value, err := strconv.Atoi(text)
	if err != nil {
		p.fail(index, err)

		return 0
	}

	return value
}

func (p *rowParser) integer64(index int) int64 {
	text := p.record[index]
	if text == "" {
		return 0
	}

	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		p.fail(index, err)

		return 0
	}

	return value
}

func (p *rowParser) float(index int) float64 {
	text := p.record[index]
	if text == "" {
		return 0
	}

	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		p.fail(index, err)

		return 0
	}

	return value
}

func (p *rowParser) boolean(index int) bool {
	value, err := strconv.ParseBool(p.record[index])
	if err != nil {
		p.fail(index, err)

		return false
	}

	return value
}

// parseRow is the inverse of Row.record.
func parseRow(record []string) (Row, error) {
	parser := &rowParser{record: record}

	row := Row{
		Design:            parser.text(0),
		Arm:               parser.text(1),
		Block:             parser.integer(2),
		Seed:              parser.integer64(3),
		Job:               parser.text(4),
		Engine:            parser.text(5),
		Covariance:        parser.text(6),
		Lambda:            parser.integer(7),
		Population:        parser.integer(8),
		RestartsPlanned:   parser.integer(9),
		Budget:            parser.integer(10),
		Score:             parser.float(11),
		ScoredEvaluations: parser.integer(12),
		FinalScore:        parser.float(13),
		Evaluations:       parser.integer(14),
		Iterations:        parser.integer(15),
		Restarts:          parser.integer(16),
		StopReason:        parser.text(17),
		Converged:         parser.boolean(18),
		ElapsedS:          parser.float(19),
		Pinned:            parser.integer(20),
		Dimension:         parser.integer(21),
		Matched:           parser.integer(22),
		Terms:             make(map[optimizer.Term]float64, len(optimizer.Terms())),
	}

	const firstTerm = 23

	terms := optimizer.Terms()
	for offset, term := range terms {
		row.Terms[term] = parser.float(firstTerm + offset)
	}

	row.MayflyVersion = parser.text(firstTerm + len(terms))
	row.CMAESVersion = parser.text(firstTerm + len(terms) + 1)
	row.Revision = parser.text(firstTerm + len(terms) + 2)

	if parser.err != nil {
		return Row{}, parser.err
	}

	return row, nil
}
