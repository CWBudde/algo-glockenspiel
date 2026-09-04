// This file holds the lines a run says about itself while it runs: what it
// seeded its modes from, and what each backend settled on once every "choose
// one for me" input was resolved. They are written to the run's log.txt and,
// for a caller that passed one, to its own writer -- which for `glockenspiel
// fit` is the terminal, so these are the lines the command prints. They live
// here rather than in the command because the run is what knows them, and a
// campaign job's log deserves the same detail an operator gets.

package fitrun

import (
	"fmt"
	"io"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

// writeSeededModes says where the starting modes came from.
//
// The three cases are worth telling apart because they look identical in the
// result and are not: a run that seeded fewer modes than were asked for was
// limited by the analysis, and one that seeded none is fitting the template's
// own modes, which is a different search from the one the caller may think it
// started.
func writeSeededModes(out io.Writer, starting *preset.Preset, seeded, requested int) {
	switch {
	case seeded > 0 && requested > seeded:
		_, _ = fmt.Fprintf(out, "modes: %d seeded from the reference's partials (asked for %d, the analysis lists %d)\n",
			seeded, requested, seeded)
	case seeded > 0:
		_, _ = fmt.Fprintf(out, "modes: %d seeded from the reference's partials\n", seeded)
	case requested >= 0:
		_, _ = fmt.Fprintf(out, "modes: keeping the preset's %d (the analysis lists no partials to seed from)\n",
			len(starting.Parameters.Modes))
	default:
		_, _ = fmt.Fprintf(out, "modes: keeping the preset's %d\n", len(starting.Parameters.Modes))
	}
}

// formatResolvedMayfly renders what a mayfly run actually settled on.
//
// variant= and seed= stay first and keep their spelling: they were the whole
// line before the tuning surface existed, and a reader -- human or script --
// already looks for them there. The preset is named only when one was chosen,
// because a preset selects a dialect of its own and is the only way to tell
// which one ran.
func formatResolvedMayfly(resolved optimizer.ResolvedMayfly) string {
	line := fmt.Sprintf("mayfly: variant=%s seed=%d", resolved.Variant, resolved.Seed)

	if resolved.Preset != "" {
		line += " preset=" + resolved.Preset
	}

	line += fmt.Sprintf(" rounds=%dx%d workers=%d", resolved.Rounds, resolved.IterationsPerRound, resolved.Workers)

	return line
}

// formatResolvedCMAES renders what a CMA-ES run actually settled on.
//
// Every field of it is a "choose one for me" input the run resolved: the
// covariance mode is lower-cased, the population and the step size may be the
// library's defaults, the seed may have been drawn, and the worker count
// follows the machine. None of them can be read off the spec that asked for
// the run.
func formatResolvedCMAES(resolved optimizer.ResolvedCMAES) string {
	line := fmt.Sprintf("cmaes: covariance=%s lambda=%d sigma=%g seed=%d workers=%d",
		resolved.Covariance, resolved.Lambda, resolved.Sigma, resolved.Seed, resolved.Workers)

	if resolved.RunEvaluations > 0 {
		line += fmt.Sprintf(" run-evals=%d", resolved.RunEvaluations)
	}

	// A fixed population is reported as a growth of one, which is the shape
	// every run had before the ladder existed and says nothing worth a column.
	if resolved.LambdaGrowth > 1 {
		line += fmt.Sprintf(" lambda-growth=%g", resolved.LambdaGrowth)
	}

	return line
}
