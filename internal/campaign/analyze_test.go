package campaign_test

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/campaign"
	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
)

// The scores of the hand-written smoke results. They are exact in binary, so
// every statistic below is exact too and the test can compare without slack
// where the arithmetic allows it.
//
// The control is mayfly-single, the candidate is sep-cmaes-r, and the
// candidate wins both blocks: the differences are 0.125 and 0.1875.
const (
	mayflyBlock1 = 0.5
	mayflyBlock2 = 0.25
	cmaesBlock1  = 0.375
	cmaesBlock2  = 0.0625
)

// smokeResultRows is a complete two-arm, two-block result set for the
// registered smoke design.
func smokeResultRows() []campaign.Row {
	return []campaign.Row{
		{
			Design: "smoke", Arm: "sep-cmaes-r", Block: 1, Seed: 120001, Job: "b01-sep-cmaes-r",
			Engine: fitrun.EngineCMAES, Covariance: "separable", Lambda: 12, Budget: 1200,
			Score: cmaesBlock1, ScoredEvaluations: 600, FinalScore: cmaesBlock1, Evaluations: 1200,
			Iterations: 25, Restarts: 2, StopReason: "max_evaluations", ElapsedS: 1.5, Revision: "abc1234",
		},
		{
			Design: "smoke", Arm: "mayfly-single", Block: 1, Seed: 120001, Job: "b01-mayfly-single",
			Engine: fitrun.EngineMayfly, Population: 10, RestartsPlanned: 1, Budget: 1200,
			Score: mayflyBlock1, ScoredEvaluations: 1200, FinalScore: mayflyBlock1, Evaluations: 1220,
			Iterations: 15, Restarts: 0, StopReason: "max_evaluations", ElapsedS: 1.25, Revision: "abc1234",
		},
		{
			Design: "smoke", Arm: "sep-cmaes-r", Block: 2, Seed: 120002, Job: "b02-sep-cmaes-r",
			Engine: fitrun.EngineCMAES, Covariance: "separable", Lambda: 12, Budget: 1200,
			Score: cmaesBlock2, ScoredEvaluations: 1200, FinalScore: cmaesBlock2, Evaluations: 1208,
			Iterations: 25, Restarts: 3, StopReason: "max_evaluations", ElapsedS: 1.5, Revision: "abc1234",
		},
		{
			Design: "smoke", Arm: "mayfly-single", Block: 2, Seed: 120002, Job: "b02-mayfly-single",
			Engine: fitrun.EngineMayfly, Population: 10, RestartsPlanned: 1, Budget: 1200,
			Score: mayflyBlock2, ScoredEvaluations: 1160, FinalScore: mayflyBlock2, Evaluations: 1204,
			Iterations: 15, Restarts: 0, StopReason: "max_evaluations", ElapsedS: 1.25, Revision: "abc1234",
		},
	}
}

// writeResults writes rows to a fresh results.csv and returns its path.
func writeResults(t *testing.T, rows []campaign.Row) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "results.csv")
	if err := campaign.WriteResults(path, rows); err != nil {
		t.Fatalf("write results: %v", err)
	}

	return path
}

// armByName finds one arm's summary in a report.
func armByName(t *testing.T, report *campaign.Report, name string) campaign.ArmSummary {
	t.Helper()

	for _, arm := range report.Arms {
		if arm.Name == name {
			return arm
		}
	}

	t.Fatalf("the report has no arm %q", name)

	return campaign.ArmSummary{}
}

func TestAnalyzeReproducesATableFromTheCSVAlone(t *testing.T) {
	report, err := campaign.Analyze(writeResults(t, smokeResultRows()))
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	if report.Design.Name != "smoke" || report.Revision != "abc1234" || report.MixedRevisions {
		t.Fatalf("the report is for design %q at revision %q (mixed %v)",
			report.Design.Name, report.Revision, report.MixedRevisions)
	}

	// The arms are listed in registration order, which is the design's, not
	// the CSV's.
	if len(report.Arms) != 2 || report.Arms[0].Name != "sep-cmaes-r" || report.Arms[1].Name != "mayfly-single" {
		t.Fatalf("the report lists arms %v", report.Arms)
	}

	checkSmokeArms(t, report)
	checkSmokeContrast(t, report)
	checkSmokeMarkdown(t, report)
}

// checkSmokeArms checks the descriptive statistics of both arms.
func checkSmokeArms(t *testing.T, report *campaign.Report) {
	t.Helper()

	mayfly := armByName(t, report, "mayfly-single")

	// (0.5+0.25)/2 = 0.375, and the deviations of +-0.125 give a sample sd of
	// 0.125*sqrt(2) = 0.1767767.
	if !nearly(mayfly.Mean, 0.375, 1e-12) || !nearly(mayfly.SD, 0.1767766953, 1e-9) {
		t.Errorf("mayfly-single has mean %g and sd %g, want 0.375 and 0.176777", mayfly.Mean, mayfly.SD)
	}

	if !nearly(mayfly.Median, 0.375, 1e-12) || mayfly.Best != mayflyBlock2 {
		t.Errorf("mayfly-single has median %g and best %g, want 0.375 and 0.25", mayfly.Median, mayfly.Best)
	}

	// (1220+1204)/2 = 1212, and (1200+1160)/(1200+1200) = 0.9833333.
	if !nearly(mayfly.MeanEvaluations, 1212, 1e-12) || !nearly(mayfly.SpentAtBestRatio, 0.98333333333, 1e-9) {
		t.Errorf("mayfly-single spent %g evaluations on average and %g of its budget at its best",
			mayfly.MeanEvaluations, mayfly.SpentAtBestRatio)
	}

	if mayfly.MeanRestarts != 0 {
		t.Errorf("mayfly-single restarted %g times on average, want 0", mayfly.MeanRestarts)
	}

	cmaes := armByName(t, report, "sep-cmaes-r")

	// (0.375+0.0625)/2 = 0.21875, and the deviations of +-0.15625 give a
	// sample sd of 0.15625*sqrt(2) = 0.2209709.
	if !nearly(cmaes.Mean, 0.21875, 1e-12) || !nearly(cmaes.SD, 0.2209708691, 1e-9) {
		t.Errorf("sep-cmaes-r has mean %g and sd %g, want 0.21875 and 0.220971", cmaes.Mean, cmaes.SD)
	}

	// (600+1200)/(1200+1200) = 0.75, and (2+3)/2 = 2.5.
	if !nearly(cmaes.SpentAtBestRatio, 0.75, 1e-12) || !nearly(cmaes.MeanRestarts, 2.5, 1e-12) {
		t.Errorf("sep-cmaes-r spent %g of its budget at its best over %g restarts on average",
			cmaes.SpentAtBestRatio, cmaes.MeanRestarts)
	}
}

// checkSmokeContrast checks the one registered contrast.
func checkSmokeContrast(t *testing.T, report *campaign.Report) {
	t.Helper()

	if len(report.Contrasts) != 1 {
		t.Fatalf("the report holds %d contrasts, want 1", len(report.Contrasts))
	}

	contrast := report.Contrasts[0]

	if contrast.Control != "mayfly-single" || contrast.Candidate != "sep-cmaes-r" || !contrast.Primary {
		t.Fatalf("the contrast is %+v", contrast)
	}

	// The differences are 0.125 and 0.1875, so the mean gain is 0.15625, the
	// deviations are +-0.03125, the sample sd is 0.03125*sqrt(2) and t is
	// 0.15625/0.03125 = 5.
	if !nearly(contrast.Gain, 0.15625, 1e-12) || !nearly(contrast.T, 5, 1e-9) {
		t.Errorf("the contrast gained %g at t=%g, want 0.15625 at t=5", contrast.Gain, contrast.T)
	}

	if contrast.Wins != 2 || contrast.N != 2 {
		t.Errorf("the candidate won %d of %d blocks, want 2 of 2", contrast.Wins, contrast.N)
	}

	// At one degree of freedom Student's t is Cauchy, so the two-sided p is
	// 1 - 2*atan(5)/pi = 0.125666.
	want := 1 - 2*math.Atan(5)/math.Pi
	if !nearly(contrast.P, want, 1e-9) || !nearly(contrast.P, 0.1256659, 1e-6) {
		t.Errorf("p is %g, want %g", contrast.P, want)
	}

	if contrast.Rejected {
		t.Errorf("p=%g was rejected at a family-wise alpha of %g", contrast.P, campaign.FamilyAlpha)
	}
}

// checkSmokeMarkdown checks the rendered report, which is the artefact a
// reader actually sees.
func checkSmokeMarkdown(t *testing.T, report *campaign.Report) {
	t.Helper()

	markdown := campaign.RenderMarkdown(report)

	wants := []string{
		"design smoke: 2 blocks, budget 1200 evaluations, balanced on " +
			"testdata/reference/legacy_synth_a4.wav, revision abc1234",
		"| arm | mean | sd | median | best | gain vs mayfly-single | t (df=1) | p | Holm | blocks won |",
		"| sep-cmaes-r | 0.218750 | 0.220971 | 0.218750 | 0.062500 | +0.1562 | +5.00 | 0.12567 | retain | 2/2 |",
		"| mayfly-single | 0.375000 | 0.176777 | 0.375000 | 0.250000 | control | control | control | control | control |",
		"| block | seed | sep-cmaes-r | mayfly-single |",
		"| 1 | 120001 | **0.375000** | 0.500000 |",
		"| 2 | 120002 | **0.062500** | 0.250000 |",
		"| arm | best | block | seed | within 5% of best | median | q25 | q75 | mean evaluations | spent at best |",
		"| sep-cmaes-r | 0.062500 | 2 | 120002 | 1/2 | 0.218750 | 0.140625 | 0.296875 | 1204 | 75.0% |",
		"| mayfly-single | 0.250000 | 2 | 120002 | 1/2 | 0.375000 | 0.312500 | 0.437500 | 1212 | 98.3% |",
		"Holm step-down over 1 paired contrasts at a family-wise alpha of 0.05.",
		"the registered primary contrast is sep-cmaes-r against mayfly-single.",
	}

	for _, want := range wants {
		if !strings.Contains(markdown, want) {
			t.Errorf("the report does not contain\n%s\ngot:\n%s", want, markdown)
		}
	}
}

func TestAnalyzeWritesTheReportItRendered(t *testing.T) {
	report, err := campaign.Analyze(writeResults(t, smokeResultRows()))
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	path := filepath.Join(t.TempDir(), "report.md")
	if err := campaign.WriteReport(report, path); err != nil {
		t.Fatalf("write report: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}

	if string(written) != campaign.RenderMarkdown(report) {
		t.Errorf("the written report differs from the rendered one:\n%s", written)
	}
}

func TestAnalyzeRefusesAHeaderThatIsNotTheContract(t *testing.T) {
	path := writeResults(t, smokeResultRows())

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read results: %v", err)
	}

	renamed := strings.Replace(string(raw), "final_score", "last_score", 1)
	if err := os.WriteFile(path, []byte(renamed), 0o644); err != nil {
		t.Fatalf("write results: %v", err)
	}

	if _, err := campaign.Analyze(path); err == nil {
		t.Fatal("analyzing a file with a renamed column succeeded")
	} else if !strings.Contains(err.Error(), "last_score") {
		t.Errorf("error %q does not name the offending column", err)
	}
}

func TestAnalyzeRefusesAnIncompleteDesign(t *testing.T) {
	rows := smokeResultRows()

	report, err := campaign.Analyze(writeResults(t, rows[:len(rows)-1]))
	if err == nil {
		t.Fatalf("analyzing a design with a missing block succeeded: %+v", report)
	}

	if !strings.Contains(err.Error(), "mayfly-single") {
		t.Errorf("error %q does not name the incomplete arm", err)
	}
}

func TestAnalyzeRefusesAnArmTheDesignDoesNotDeclare(t *testing.T) {
	rows := smokeResultRows()
	for index := range rows {
		if rows[index].Arm == "sep-cmaes-r" {
			rows[index].Arm = "sep-cmaes-typo"
		}
	}

	if _, err := campaign.Analyze(writeResults(t, rows)); err == nil {
		t.Fatal("analyzing a result set with an unknown arm succeeded")
	}
}

func TestAnalyzeReportsMixedRevisions(t *testing.T) {
	rows := smokeResultRows()
	rows[0].Revision = "def5678"

	report, err := campaign.Analyze(writeResults(t, rows))
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	if !report.MixedRevisions || report.Revision != "mixed" {
		t.Fatalf("the report claims revision %q (mixed %v)", report.Revision, report.MixedRevisions)
	}

	if !strings.Contains(campaign.RenderMarkdown(report), "more than one binary") {
		t.Error("the report does not warn about the mixed revisions")
	}
}

func TestAnalyzePrintsNoStatisticsForADescriptiveDesign(t *testing.T) {
	design, err := campaign.Lookup("seed-hunt")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	// seed-hunt's arm names are derived from a winner that engine-shape only
	// names at run time, so the results file defines the arm set and the
	// analysis must accept whatever it holds.
	rows := make([]campaign.Row, 0, design.Blocks*2)

	for block := 1; block <= design.Blocks; block++ {
		for index, arm := range []string{"blk-cmaes-r-l0007", "blk-cmaes-r-l0014"} {
			rows = append(rows, campaign.Row{
				Design: "seed-hunt", Arm: arm, Block: block, Seed: int64(122000 + block),
				Engine: fitrun.EngineCMAES, Budget: design.Budget,
				Score:       0.1 + 0.001*float64(block) + 0.01*float64(index),
				Evaluations: design.Budget, ScoredEvaluations: design.Budget, Revision: "abc1234",
			})
		}
	}

	report, err := campaign.Analyze(writeResults(t, rows))
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	if len(report.Contrasts) != 0 {
		t.Errorf("a descriptive design produced %d contrasts", len(report.Contrasts))
	}

	markdown := campaign.RenderMarkdown(report)

	if !strings.Contains(markdown, "descriptive only, no inferential statistics") {
		t.Errorf("the report does not say it is descriptive:\n%s", markdown)
	}

	if strings.Contains(markdown, "Holm step-down") || strings.Contains(markdown, "gain vs") {
		t.Errorf("the report of a descriptive design holds inferential statistics:\n%s", markdown)
	}

	if !strings.Contains(markdown, "| block | seed | blk-cmaes-r-l0007 | blk-cmaes-r-l0014 |") {
		t.Errorf("the block table does not use the arm names from the results:\n%s", markdown)
	}
}

// TestAnalyzeNamesTheArmsOfASeedHuntFromTheCSV is the regression test for a
// report that described a campaign nobody ran. seed-hunt's arms are named
// after whichever engine-shape arm won, and the registry holds a default; a
// header that printed the registered description would therefore name the
// default winner over a table of some other arm's numbers.
func TestAnalyzeNamesTheArmsOfASeedHuntFromTheCSV(t *testing.T) {
	design, err := campaign.Lookup("seed-hunt")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	// Arms of the separable winner, which is not the arm the registry's
	// default seed-hunt is built around.
	arms := []string{"sep-cmaes-r-l14", "sep-cmaes-r-l28"}

	rows := make([]campaign.Row, 0, design.Blocks*len(arms))

	for block := 1; block <= design.Blocks; block++ {
		for index, arm := range arms {
			rows = append(rows, campaign.Row{
				Design: "seed-hunt", Arm: arm, Block: block, Seed: int64(122000 + block),
				Engine: fitrun.EngineCMAES, Budget: design.Budget,
				Score:       0.1 + 0.001*float64(block) + 0.01*float64(index),
				Evaluations: design.Budget, ScoredEvaluations: design.Budget, Revision: "abc1234",
			})
		}
	}

	report, err := campaign.Analyze(writeResults(t, rows))
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	if !report.ArmsFromCSV {
		t.Error("the report does not record that its arms came from the CSV")
	}

	markdown := campaign.RenderMarkdown(report)

	if !strings.Contains(markdown, "arms from the CSV: sep-cmaes-r-l14, sep-cmaes-r-l28") {
		t.Errorf("the header does not name the arms found:\n%s", markdown)
	}

	if strings.Contains(markdown, "blk-cmaes-r") {
		t.Errorf("the header names the registry's default winner:\n%s", markdown)
	}
}

// engineShapeRows builds a complete result set for the registered engine-shape
// design: every arm in every block, with scores that separate the arms by a
// fixed amount so the contrasts are deterministic and non-degenerate.
func engineShapeRows(t *testing.T) []campaign.Row {
	t.Helper()

	design, err := campaign.Lookup("engine-shape")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	rows := make([]campaign.Row, 0, design.Blocks*len(design.Arms))

	for block := 1; block <= design.Blocks; block++ {
		for index, arm := range design.Arms {
			// A block term shared by every arm, an arm term the contrasts
			// measure, and a small deterministic wobble so that the paired
			// differences vary and the t statistics are finite.
			score := 1 + 0.01*float64(block) - 0.1*float64(index) +
				0.001*float64((block*7+index*3)%5)

			rows = append(rows, campaign.Row{
				Design: design.Name, Arm: arm.Name, Block: block,
				Seed: design.SeedBase + int64(block), Job: arm.Name,
				Engine: arm.Engine.Name, Budget: design.Budget,
				Score: score, ScoredEvaluations: design.Budget, FinalScore: score,
				Evaluations: design.Budget, StopReason: "max_evaluations", Revision: "abc1234",
			})
		}
	}

	return rows
}

// TestAnalyzeRendersOneTablePerControl pins the shape of a report for a design
// with more than one control. A gain column can only be against one arm, so a
// second registered control gets a table of its own, and in each table the
// arms no contrast compares against that control print n/a rather than a
// blank. The whole contrast family is corrected together whatever the tables.
func TestAnalyzeRendersOneTablePerControl(t *testing.T) {
	report, err := campaign.Analyze(writeResults(t, engineShapeRows(t)))
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	if len(report.Contrasts) != 3 {
		t.Fatalf("the report holds %d contrasts, want the design's 3", len(report.Contrasts))
	}

	markdown := campaign.RenderMarkdown(report)

	for _, control := range []string{"mayfly-r16", "mayfly-single"} {
		if !strings.Contains(markdown, "### Table 1: arms against "+control) {
			t.Errorf("the report has no arm table against %q:\n%s", control, markdown)
		}
	}

	if count := strings.Count(markdown, "### Table 1:"); count != 2 {
		t.Errorf("the report renders %d Table 1 headers, want one per control", count)
	}

	// The table against mayfly-single registers only mayfly-r16, so the three
	// remaining arms of that table have nothing to compare.
	// Two arms are untested in the table against mayfly-r16 and three in the
	// table against mayfly-single, because a contrast is registered only where
	// the design asks a question.
	if count := strings.Count(markdown, "| n/a | n/a | n/a | n/a | n/a |"); count != 5 {
		t.Errorf("the report holds %d rows of n/a cells, want 5:\n%s", count, markdown)
	}

	// One family, one correction: Holm over three contrasts whichever table
	// each one is printed in.
	if !strings.Contains(markdown, "Holm step-down over 3 paired contrasts") {
		t.Errorf("the footer does not correct the whole family together:\n%s", markdown)
	}
}

// TestAnalyzeRefusesTwoRowsForOneBlock covers the duplicate-block branch: a
// results file that holds one arm twice in a block would be averaged as if the
// campaign had run more blocks than it did.
func TestAnalyzeRefusesTwoRowsForOneBlock(t *testing.T) {
	rows := smokeResultRows()
	// Block 2's rows repeated as block 1, which keeps the row count right and
	// makes only the block numbers wrong.
	rows[2].Block, rows[3].Block = 1, 1

	_, err := campaign.Analyze(writeResults(t, rows))
	if err == nil {
		t.Fatal("analyzing a results file with a duplicated block succeeded")
	}

	if !strings.Contains(err.Error(), "two rows for block 1") {
		t.Errorf("error %q does not name the duplicated block", err)
	}
}

// TestAnalyzeRefusesResultsThatMixDesigns covers the designOf branch: two
// designs in one file would be analysed as one campaign with twice the arms.
func TestAnalyzeRefusesResultsThatMixDesigns(t *testing.T) {
	rows := smokeResultRows()
	rows[3].Design = "engine-shape"

	_, err := campaign.Analyze(writeResults(t, rows))
	if err == nil {
		t.Fatal("analyzing a results file that mixes designs succeeded")
	}

	if !strings.Contains(err.Error(), "engine-shape") {
		t.Errorf("error %q does not name the second design", err)
	}
}

// TestReportPrintsAHugeTInThreeFigures pins the t column's fallback. A paired
// contrast whose block differences are nearly identical divides by a standard
// error close to zero, and two decimal places of that fill the cell with
// digits that say nothing.
func TestReportPrintsAHugeTInThreeFigures(t *testing.T) {
	for _, testCase := range []struct {
		value float64
		want  string
	}{
		{value: 5, want: "+5.00"},
		{value: -12.345, want: "-12.35"},
		{value: 999999, want: "+999999.00"},
		{value: 1e6, want: "+1e+06"},
		{value: -4.2e12, want: "-4.2e+12"},
	} {
		if got := campaign.FormatT(testCase.value); got != testCase.want {
			t.Errorf("FormatT(%g) = %q, want %q", testCase.value, got, testCase.want)
		}
	}
}
