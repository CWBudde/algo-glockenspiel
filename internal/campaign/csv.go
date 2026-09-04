package campaign

import (
	"encoding/csv"
	"fmt"
	"math"
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

// firstTermColumn is where the block of per-term columns starts.
const firstTermColumn = 23

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

	// The header decides the record width, not the contract, so that a file
	// written before a term existed still reads. checkHeader is what refuses
	// a header the contract does not allow.
	reader := csv.NewReader(file)

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse results %q: %w", path, err)
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("results %q is empty, want the header row", path)
	}

	layout, err := checkHeader(path, records[0])
	if err != nil {
		return nil, err
	}

	rows := make([]Row, 0, len(records)-1)

	for index, record := range records[1:] {
		row, err := parseRow(record, layout)
		if err != nil {
			return nil, fmt.Errorf("results %q line %d: %w", path, index+2, err)
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// checkHeader compares the file's header against the contract's and returns
// which term each of the file's term columns holds.
//
// The fixed columns on either side of the term block must match the contract
// exactly, so a renamed or reordered column is still refused. The term block
// itself only has to be the contract's terms in the contract's order with some
// left out: a results file written before a term existed is readable, and that
// term reads back as unmeasured rather than as zero, which is what it was. A
// term the contract does not know, a duplicate, or two terms out of order all
// fail, so this is not a licence to write any header at all.
func checkHeader(path string, record []string) ([]optimizer.Term, error) {
	want := Header()
	head, tail := firstTermColumn, len(want)-firstTermColumn-len(optimizer.Terms())

	if len(record) < head+tail {
		return nil, fmt.Errorf("results %q has %d columns, want at least %d", path, len(record), head+tail)
	}

	for index := range head {
		if record[index] != want[index] {
			return nil, fmt.Errorf("results %q column %d is %q, want %q", path, index+1, record[index], want[index])
		}
	}

	for offset := range tail {
		index, source := len(record)-tail+offset, len(want)-tail+offset
		if record[index] != want[source] {
			return nil, fmt.Errorf("results %q column %d is %q, want %q", path, index+1, record[index], want[source])
		}
	}

	terms := optimizer.Terms()
	layout := make([]optimizer.Term, 0, len(record)-head-tail)
	next := 0

	for index := head; index < len(record)-tail; index++ {
		found := -1

		for offset := next; offset < len(terms); offset++ {
			if string(terms[offset]) == record[index] {
				found = offset

				break
			}
		}

		if found < 0 {
			return nil, fmt.Errorf("results %q column %d is %q, which is not a term of the objective in the order the contract writes them",
				path, index+1, record[index])
		}

		layout = append(layout, terms[found])
		next = found + 1
	}

	return layout, nil
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
func parseRow(record []string, layout []optimizer.Term) (Row, error) {
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

	// A term the file does not carry stays out of the map, which is how a
	// Metrics reports a term it could not measure.
	for _, term := range optimizer.Terms() {
		row.Terms[term] = math.NaN()
	}

	for offset, term := range layout {
		row.Terms[term] = parser.float(firstTermColumn + offset)
	}

	row.MayflyVersion = parser.text(firstTermColumn + len(layout))
	row.CMAESVersion = parser.text(firstTermColumn + len(layout) + 1)
	row.Revision = parser.text(firstTermColumn + len(layout) + 2)

	if parser.err != nil {
		return Row{}, parser.err
	}

	return row, nil
}
