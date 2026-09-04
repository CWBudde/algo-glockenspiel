package fitschema

import "github.com/cwbudde/algo-glockenspiel/internal/optimizer"

// The name lists below used to live only in web/src/api/types.ts, hand
// transcribed from the Go packages that actually own each vocabulary:
// internal/server's validateFitBackend switch, optimizer.ParseMetric,
// CMAESOptimizer's covariance modes and the mayfly dialects, presets and
// selection strategies. Neither internal/server nor internal/browserfit
// validated most of them against a list of their own -- both packages pass
// the mayfly variant, preset and selection straight through and let the
// mayfly library refuse an unknown one -- so there was nothing in Go to
// generate the browser's copy from. This file is that missing source: the
// browser's dropdowns and its client-side check both read the generated
// mirror of it, and a name added to a dialect here is a name the form offers
// without anyone touching web/src.

// OptimizerNames lists the backends this project runs, in every
// implementation of the name: internal/server's validateFitBackend,
// internal/browserfit's selectOptimizer and internal/fitrun/engine.go.
func OptimizerNames() []string {
	return []string{"simple", "mayfly", "cmaes"}
}

// MetricNames lists optimizer.ParseMetric's vocabulary, composite profiles
// first, exactly as optimizer.MetricNames already orders it.
func MetricNames() []string {
	return optimizer.MetricNames()
}

// CMAESCovariances lists CMAESOptimizer's covariance modes.
func CMAESCovariances() []string {
	return []string{"separable", "block"}
}

// MayflyVariants lists MayflyOptimizer's dialects, in the order the mayfly
// paper introduces them: the base algorithm first, then each variant in
// publication order. mayfly.ListVariants ranges over a map and is unordered,
// which is why this list is spelled out here rather than read from it.
func MayflyVariants() []string {
	return []string{"ma", "desma", "olce", "eobbma", "gsasma", "hmma", "mpma", "aoblmoa"}
}

// MayflyPresets lists mayfly.ConfigPreset's vocabulary, from
// config_loader.go upstream, in the order the presets are documented there:
// roughly simplest problem shape to hardest. mayfly.ListPresets ranges over
// a map and is unordered, which is why this list is spelled out here rather
// than read from it.
func MayflyPresets() []string {
	return []string{
		"unimodal", "multimodal", "highly_multimodal", "deceptive",
		"narrow_valley", "high_dimensional", "fast_convergence",
		"stable_convergence", "multi_objective",
	}
}

// MayflySelections lists the `selection` knob's vocabulary: how crossover
// pairs its parents.
func MayflySelections() []string {
	return []string{"rank", "tournament"}
}
