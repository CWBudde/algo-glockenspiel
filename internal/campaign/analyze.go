package campaign

import (
	"fmt"
	"sort"
)

// seedHuntName is the design whose arm names are derived at plan time from a
// winner that is not known until engine-shape has run. Its results file is the
// only record of what the arms were actually called, so the analysis takes the
// arm set from the CSV rather than from the registry.
const seedHuntName = "seed-hunt"

// ArmSummary is one arm's descriptive statistics over the blocks it ran.
type ArmSummary struct {
	Name   string  `json:"name"`
	Engine string  `json:"engine"`
	Mean   float64 `json:"mean"`
	SD     float64 `json:"sd"`
	Median float64 `json:"median"`
	Best   float64 `json:"best"`

	// Scores is the cap-matched score of every block the arm ran, keyed by
	// block number. It is what the paired contrasts are computed from.
	Scores map[int]float64 `json:"scores"`

	MeanEvaluations float64 `json:"mean_evaluations"`

	// SpentAtBestRatio is how much of the budget the arm had spent when it
	// reached the score it is scored on, as a ratio of totals rather than a
	// mean of per-block ratios: a block whose budget differed would otherwise
	// count as much as one that ran the full cap.
	SpentAtBestRatio float64 `json:"spent_at_best_ratio"`

	MeanRestarts float64 `json:"mean_restarts"`
}

// BlockSummary is one block: its seed and what every arm scored on it. Keeping
// the block table in the report is what lets a reader see whether an arm won
// on average or won everywhere.
type BlockSummary struct {
	Block  int                `json:"block"`
	Seed   int64              `json:"seed"`
	Scores map[string]float64 `json:"scores"`
}

// ContrastResult is one registered comparison, tested and corrected.
type ContrastResult struct {
	Control   string  `json:"control"`
	Candidate string  `json:"candidate"`
	Primary   bool    `json:"primary"`
	Gain      float64 `json:"gain"`
	T         float64 `json:"t"`
	P         float64 `json:"p"`
	Wins      int     `json:"wins"`
	N         int     `json:"n"`
	Rejected  bool    `json:"rejected"`
}

// Report is everything the Markdown tables are rendered from, and everything
// the analysis concluded. It is rebuilt from results.csv plus the registered
// design the rows name: the CSV carries the numbers, and the design carries
// the block count, the budget, the profile and the contrast family the numbers
// are read against. The manifest is not needed for that and is not consulted;
// it stays the frozen record of what actually ran.
type Report struct {
	Design   Design `json:"design"`
	Revision string `json:"revision"`

	// MixedRevisions marks a result set whose rows were produced by more than
	// one binary. The report still renders, because refusing would hide the
	// numbers that show the problem, but it says so.
	MixedRevisions bool `json:"mixed_revisions"`

	// ArmsFromCSV marks a design whose arm set was read off the CSV rather
	// than off the registry, which is seed-hunt: its arms are named after
	// whichever winner the campaign was planned for, so the registry holds a
	// default the rows need not match. The header then names the arms found
	// instead of repeating the registered description.
	ArmsFromCSV bool `json:"arms_from_csv"`

	Arms      []ArmSummary     `json:"arms"`
	Blocks    []BlockSummary   `json:"blocks"`
	Contrasts []ContrastResult `json:"contrasts"`
	BestOf    []BestOfEntry    `json:"best_of"`
}

// PrimaryContrast returns the registered primary contrast, if the design has
// one.
func (r *Report) PrimaryContrast() (ContrastResult, bool) {
	for _, contrast := range r.Contrasts {
		if contrast.Primary {
			return contrast, true
		}
	}

	return ContrastResult{}, false
}

// Analyze rebuilds a campaign's report from its results file and the design
// the rows name.
//
// The run directories are not read. They hold far more, but they are large and
// tied to the build that wrote them, whereas the CSV plus a design name is the
// thing worth keeping: the CSV holds every number, and the registered design
// holds the block count, budget, profile and contrast family that say how to
// read them. So the archive is one file, as long as the design is still
// registered under that name. The manifest is not consulted either; it remains
// the frozen record of what ran, which is a different question from what the
// numbers say.
func Analyze(csvPath string) (*Report, error) {
	rows, err := ReadResults(csvPath)
	if err != nil {
		return nil, err
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("results %q has no rows", csvPath)
	}

	design, err := designOf(rows)
	if err != nil {
		return nil, err
	}

	byArm, order, err := groupByArm(design, rows)
	if err != nil {
		return nil, err
	}

	report := &Report{Design: design, ArmsFromCSV: design.Name == seedHuntName}
	report.Revision, report.MixedRevisions = revisionOf(rows)

	for _, name := range order {
		report.Arms = append(report.Arms, summariseArm(name, byArm[name]))
		report.BestOf = append(report.BestOf, BestOf(byArm[name]))
	}

	report.Blocks = summariseBlocks(rows)

	if !design.Descriptive {
		report.Contrasts, err = testContrasts(design, report.Arms)
		if err != nil {
			return nil, err
		}
	}

	return report, nil
}

// designOf reads the design name out of the rows and looks it up. Every row
// must name the same design, because a results file holding two designs would
// be analysed as one campaign with twice the arms.
func designOf(rows []Row) (Design, error) {
	name := rows[0].Design

	for _, row := range rows {
		if row.Design != name {
			return Design{}, fmt.Errorf("results mix design %q and design %q", name, row.Design)
		}
	}

	return Lookup(name)
}

// revisionOf reports the binary revision the rows were produced by, and
// whether they disagree.
func revisionOf(rows []Row) (string, bool) {
	revision := rows[0].Revision

	for _, row := range rows {
		if row.Revision != revision {
			return "mixed", true
		}
	}

	return revision, false
}

// groupByArm splits the rows by arm and returns the order the report lists
// them in.
//
// For a registered design the arm names are part of the design, so a CSV whose
// arms differ is not a result set for that design and is refused. seed-hunt is
// the exception: its arm names are derived from a winner the registry only
// guesses at, so there the CSV defines the arm set and the registry supplies
// only the blocks, the budget and the (empty) contrast family.
func groupByArm(design Design, rows []Row) (map[string][]Row, []string, error) {
	byArm := make(map[string][]Row, len(design.Arms))
	order := make([]string, 0, len(design.Arms))

	for _, row := range rows {
		if _, seen := byArm[row.Arm]; !seen {
			order = append(order, row.Arm)
		}

		byArm[row.Arm] = append(byArm[row.Arm], row)
	}

	if design.Name != seedHuntName {
		registered := make([]string, 0, len(design.Arms))
		for _, arm := range design.Arms {
			registered = append(registered, arm.Name)
		}

		if err := sameArms(design, registered, order); err != nil {
			return nil, nil, err
		}

		order = registered
	}

	if err := checkBlocks(design, byArm, order); err != nil {
		return nil, nil, err
	}

	return byArm, order, nil
}

// sameArms compares the CSV's arm set with the design's.
func sameArms(design Design, registered, found []string) error {
	inCSV := make(map[string]bool, len(found))
	for _, name := range found {
		inCSV[name] = true
	}

	for _, name := range registered {
		if !inCSV[name] {
			return fmt.Errorf("design %q declares arm %q, but the results have no row for it", design.Name, name)
		}

		delete(inCSV, name)
	}

	for _, name := range found {
		if inCSV[name] {
			return fmt.Errorf("the results hold arm %q, which design %q does not declare", name, design.Name)
		}
	}

	return nil
}

// checkBlocks refuses a result set an arm did not finish. A missing block
// would silently shrink one arm's sample and leave the paired contrasts
// comparing different sets of seeds.
func checkBlocks(design Design, byArm map[string][]Row, order []string) error {
	for _, name := range order {
		rows := byArm[name]
		if len(rows) != design.Blocks {
			return fmt.Errorf("arm %q has %d rows, but design %q runs %d blocks",
				name, len(rows), design.Name, design.Blocks)
		}

		seen := make(map[int]bool, len(rows))

		for _, row := range rows {
			if seen[row.Block] {
				return fmt.Errorf("arm %q has two rows for block %d", name, row.Block)
			}

			seen[row.Block] = true
		}
	}

	return nil
}

// summariseArm reduces one arm's rows to the descriptive statistics the report
// prints.
func summariseArm(name string, rows []Row) ArmSummary {
	summary := ArmSummary{Name: name, Scores: make(map[int]float64, len(rows))}

	if len(rows) == 0 {
		return summary
	}

	summary.Engine = rows[0].Engine

	scores := make([]float64, 0, len(rows))
	best := rows[0].Score
	evaluations, scored, budget, restarts := 0, 0, 0, 0

	for _, row := range rows {
		summary.Scores[row.Block] = row.Score
		scores = append(scores, row.Score)

		if row.Score < best {
			best = row.Score
		}

		evaluations += row.Evaluations
		scored += row.ScoredEvaluations
		budget += row.Budget
		restarts += row.Restarts
	}

	summary.Mean, summary.SD = MeanSD(scores)
	summary.Median = Median(scores)
	summary.Best = best
	summary.MeanEvaluations = float64(evaluations) / float64(len(rows))
	summary.MeanRestarts = float64(restarts) / float64(len(rows))

	if budget > 0 {
		summary.SpentAtBestRatio = float64(scored) / float64(budget)
	}

	return summary
}

// summariseBlocks builds the per-block table, in block order.
func summariseBlocks(rows []Row) []BlockSummary {
	byBlock := make(map[int]*BlockSummary)
	numbers := make([]int, 0)

	for _, row := range rows {
		block, ok := byBlock[row.Block]
		if !ok {
			block = &BlockSummary{Block: row.Block, Seed: row.Seed, Scores: make(map[string]float64)}
			byBlock[row.Block] = block
			numbers = append(numbers, row.Block)
		}

		block.Scores[row.Arm] = row.Score
	}

	sort.Ints(numbers)

	blocks := make([]BlockSummary, 0, len(numbers))
	for _, number := range numbers {
		blocks = append(blocks, *byBlock[number])
	}

	return blocks
}

// testContrasts runs every registered contrast and corrects them together.
//
// The correction is over the whole family rather than per contrast because the
// design registered them together: three chances to find a difference at
// alpha 0.05 is not a test at alpha 0.05.
func testContrasts(design Design, arms []ArmSummary) ([]ContrastResult, error) {
	scores := make(map[string]map[int]float64, len(arms))
	for _, arm := range arms {
		scores[arm.Name] = arm.Scores
	}

	results := make([]ContrastResult, 0, len(design.Contrasts))
	pValues := make([]float64, 0, len(design.Contrasts))

	for _, contrast := range design.Contrasts {
		control, ok := scores[contrast.Control]
		if !ok {
			return nil, fmt.Errorf("contrast names control arm %q, which the results do not hold", contrast.Control)
		}

		candidate, ok := scores[contrast.Candidate]
		if !ok {
			return nil, fmt.Errorf("contrast names candidate arm %q, which the results do not hold", contrast.Candidate)
		}

		gain, t, wins, n, err := PairedGain(control, candidate)
		if err != nil {
			return nil, fmt.Errorf("contrast %q against %q: %w", contrast.Candidate, contrast.Control, err)
		}

		result := ContrastResult{
			Control:   contrast.Control,
			Candidate: contrast.Candidate,
			Primary:   contrast.Primary,
			Gain:      gain,
			T:         t,
			P:         TwoSidedP(t, n-1),
			Wins:      wins,
			N:         n,
		}

		results = append(results, result)
		pValues = append(pValues, result.P)
	}

	for index, rejected := range Holm(pValues, FamilyAlpha) {
		results[index].Rejected = rejected
	}

	return results, nil
}
