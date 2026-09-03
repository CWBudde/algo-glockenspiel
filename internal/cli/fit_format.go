// This file holds the resolve reports of the fit command: the one line each
// backend prints once it has settled every "choose one for me" input. They
// live apart from fit.go because that file is at the repository's file length
// limit, and a formatter with no state of its own is what moves most cleanly.

package cli

import (
	"fmt"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// formatResolvedMayfly renders what a run actually settled on.
//
// variant= and seed= stay first and keep their spelling: they were the whole
// line before the tuning surface existed, and a reader -- human or script --
// already looks for them there.
func formatResolvedMayfly(resolved optimizer.ResolvedMayfly) string {
	line := fmt.Sprintf("Mayfly: variant=%s seed=%d", resolved.Variant, resolved.Seed)

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
// follows the machine. None of them can be read off the command line.
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
