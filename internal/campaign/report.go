package campaign

import (
	"fmt"
	"math"
	"os"
	"strings"
)

// The report's number formats. They are constants because the same score has
// to print identically in the arm table, the block table and the best-of
// table, or a reader comparing two tables sees a difference that is not there.
const (
	scoreFormat = "%.6f"
	gainFormat  = "%+.4f"
	tFormat     = "%+.2f"
	pFormat     = "%.5f"

	// hugeTFormat is what a t statistic prints as once two decimal places stop
	// saying anything. A paired contrast whose block differences are nearly
	// identical divides by a standard error close to zero, and the fixed-point
	// form then fills the column with digits. Three significant figures say the
	// same thing in the width of a table cell.
	hugeTFormat = "%+.3g"

	// hugeT is where the fixed-point form gives way. A million is already far
	// past any t anyone reads as a number rather than as "the difference is
	// larger than the noise".
	hugeT = 1e6
)

// contrastNotApplicable is what an arm that no registered contrast compares
// against this table's control prints in the contrast columns. It is not a
// blank, because a blank reads as a missing number rather than as a
// comparison that was never registered.
const contrastNotApplicable = "n/a"

// WriteReport writes the rendered Markdown to path.
func WriteReport(report *Report, path string) error {
	if err := os.WriteFile(path, []byte(RenderMarkdown(report)), 0o644); err != nil {
		return fmt.Errorf("write report %q: %w", path, err)
	}

	return nil
}

// RenderMarkdown renders the report.
//
// The tables are the ones MayFlyCircleFit's measurement harness prints, in the
// same order and with the same columns, so a reader who has seen one campaign
// report can read this one without relearning it. The order is an argument:
// the arm table says which arm won on average, the block table says whether it
// won everywhere or only on average, and the best-of table says whether the
// winner is reliable or lucky.
func RenderMarkdown(report *Report) string {
	var out strings.Builder

	out.WriteString(renderHeader(report))

	if !report.Design.Descriptive {
		out.WriteString(renderArmTables(report))
	}

	out.WriteString(renderBlockTable(report))
	out.WriteString(renderBestOfTable(report))
	out.WriteString(renderFooter(report))

	return out.String()
}

// renderHeader prints the one line that says what was compared, under which
// budget, and by which revision of the code.
//
// The revision rather than the binary: what the CSV carries is the source
// revision every row was produced at, and the binary's own digest is in the
// manifest, which is the frozen record of what ran.
func renderHeader(report *Report) string {
	design := report.Design

	var out strings.Builder

	fmt.Fprintf(&out, "design %s: %d blocks, budget %d evaluations, %s on %s, revision %s\n",
		design.Name, design.Blocks, design.Budget, design.Profile, design.Reference, report.Revision)

	if report.MixedRevisions {
		out.WriteString("\nwarning: the rows were produced by more than one binary, so the arms are not " +
			"strictly comparable.\n")
	}

	fmt.Fprintf(&out, "\n%s\n", describe(report))

	return out.String()
}

// describe is the line under the header. A design whose arms were read off the
// CSV rather than off the registry cannot use the registered description: it
// names the arms the design was written to run, and the CSV holds whichever
// arms were actually planned. Naming them instead is the only honest sentence
// available, and it is the one a reader needs.
func describe(report *Report) string {
	if !report.ArmsFromCSV {
		return report.Design.Description
	}

	names := make([]string, 0, len(report.Arms))
	for _, arm := range report.Arms {
		names = append(names, arm.Name)
	}

	return "arms from the CSV: " + strings.Join(names, ", ")
}

// controlOrder is the controls of the registered contrasts, in the order they
// were registered. A design that compares several candidates against one
// control gets one table; a design with two controls gets two, because a gain
// column can only be against one of them.
func controlOrder(report *Report) []string {
	seen := make(map[string]bool, len(report.Contrasts))
	order := make([]string, 0, len(report.Contrasts))

	for _, contrast := range report.Contrasts {
		if seen[contrast.Control] {
			continue
		}

		seen[contrast.Control] = true

		order = append(order, contrast.Control)
	}

	return order
}

// renderArmTables prints Table 1, once per control arm.
func renderArmTables(report *Report) string {
	var out strings.Builder

	for _, control := range controlOrder(report) {
		fmt.Fprintf(&out, "\n### Table 1: arms against %s\n\n", control)
		out.WriteString(renderArmTable(report, control))
	}

	return out.String()
}

// renderArmTable prints one arm table against one control.
func renderArmTable(report *Report, control string) string {
	against := make(map[string]ContrastResult, len(report.Contrasts))

	degrees := report.Design.Blocks - 1

	for _, contrast := range report.Contrasts {
		if contrast.Control == control {
			against[contrast.Candidate] = contrast
			degrees = contrast.N - 1
		}
	}

	var out strings.Builder

	fmt.Fprintf(&out, "| arm | mean | sd | median | best | gain vs %s | t (df=%d) | p | Holm | blocks won |\n",
		control, degrees)
	out.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")

	for _, arm := range report.Arms {
		cells := contrastCells(arm.Name, control, against)

		fmt.Fprintf(&out, "| %s | "+scoreFormat+" | "+scoreFormat+" | "+scoreFormat+" | "+scoreFormat+" | %s |\n",
			arm.Name, arm.Mean, arm.SD, arm.Median, arm.Best, strings.Join(cells, " | "))
	}

	return out.String()
}

// contrastCells is the five contrast columns of one arm's row: the control's
// own row has nothing to compare against itself, and an arm the design never
// registered against this control was not tested at all. They are returned
// one cell per element rather than pre-joined, so that the caller stays the
// only place that knows how a Markdown row is separated.
func contrastCells(arm, control string, against map[string]ContrastResult) []string {
	if arm == control {
		return []string{"control", "control", "control", "control", "control"}
	}

	contrast, ok := against[arm]
	if !ok {
		return []string{
			contrastNotApplicable, contrastNotApplicable, contrastNotApplicable,
			contrastNotApplicable, contrastNotApplicable,
		}
	}

	holm := "retain"
	if contrast.Rejected {
		holm = "reject"
	}

	return []string{
		fmt.Sprintf(gainFormat, contrast.Gain),
		formatT(contrast.T),
		fmt.Sprintf(pFormat, contrast.P),
		holm,
		fmt.Sprintf("%d/%d", contrast.Wins, contrast.N),
	}
}

// formatT renders a t statistic for the arm table.
func formatT(value float64) string {
	if math.Abs(value) < hugeT {
		return fmt.Sprintf(tFormat, value)
	}

	return fmt.Sprintf(hugeTFormat, value)
}

// renderBlockTable prints Table 2. The best score of each block is bold, and
// an exact tie bolds every arm that reached it, so the column of bold cells is
// a count of blocks won that the reader can check against the arm table.
func renderBlockTable(report *Report) string {
	var out strings.Builder

	out.WriteString("\n### Table 2: score by block\n\n| block | seed |")

	for _, arm := range report.Arms {
		fmt.Fprintf(&out, " %s |", arm.Name)
	}

	out.WriteString("\n| --- | --- |")

	for range report.Arms {
		out.WriteString(" --- |")
	}

	out.WriteString("\n")

	for _, block := range report.Blocks {
		best := math.Inf(1)

		for _, arm := range report.Arms {
			if score, ok := block.Scores[arm.Name]; ok && score < best {
				best = score
			}
		}

		fmt.Fprintf(&out, "| %d | %d |", block.Block, block.Seed)

		for _, arm := range report.Arms {
			score, ok := block.Scores[arm.Name]
			if !ok {
				out.WriteString(" |")

				continue
			}

			cell := fmt.Sprintf(scoreFormat, score)
			if score == best {
				cell = "**" + cell + "**"
			}

			fmt.Fprintf(&out, " %s |", cell)
		}

		out.WriteString("\n")
	}

	return out.String()
}

// renderBestOfTable prints Table 3: the best each arm reached and how much of
// the distribution sits near it.
func renderBestOfTable(report *Report) string {
	summaries := make(map[string]ArmSummary, len(report.Arms))
	for _, arm := range report.Arms {
		summaries[arm.Name] = arm
	}

	var out strings.Builder

	out.WriteString("\n### Table 3: best of each arm\n\n")
	out.WriteString("| arm | best | block | seed | within 5% of best | median | q25 | q75 | " +
		"mean evaluations | spent at best |\n")
	out.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")

	for _, entry := range report.BestOf {
		arm := summaries[entry.Arm]

		fmt.Fprintf(&out, "| %s | "+scoreFormat+" | %d | %d | %d/%d | "+scoreFormat+" | "+
			scoreFormat+" | "+scoreFormat+" | %.0f | %.1f%% |\n",
			entry.Arm, entry.Best, entry.Block, entry.Seed, entry.WithinMargin, report.Design.Blocks,
			entry.Median, entry.Q25, entry.Q75, arm.MeanEvaluations, 100*arm.SpentAtBestRatio)
	}

	return out.String()
}

// renderFooter says how the p-values were corrected and which comparison the
// design was registered to answer, so a reader cannot mistake a secondary
// contrast for the question.
func renderFooter(report *Report) string {
	var out strings.Builder

	out.WriteString("\n")

	if report.Design.Descriptive {
		out.WriteString("descriptive only, no inferential statistics\n")

		return out.String()
	}

	fmt.Fprintf(&out, "Holm step-down over %d paired contrasts at a family-wise alpha of %.2f.\n",
		len(report.Contrasts), FamilyAlpha)

	if primary, ok := report.PrimaryContrast(); ok {
		fmt.Fprintf(&out, "the registered primary contrast is %s against %s.\n", primary.Candidate, primary.Control)
	}

	return out.String()
}
