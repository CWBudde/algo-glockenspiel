package cli

import (
	"strings"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// boundsFlagHelp documents the --bounds JSON shape in `fit --help`. The parser
// itself lives in internal/optimizer, which owns ParamBounds and now serves the
// fit API's `bounds` field from the same code; only the flag wording is local.
var boundsFlagHelp = "Path to a JSON file narrowing the search bounds; keys " +
	strings.Join(optimizer.BoundsKeys, ", ") +
	" each hold a [min, max] pair, and omitted keys keep the default bound"

// mayflyTuningFlagHelp documents the --mayfly-tuning JSON shape in `fit --help`.
// Like the bounds help it names the keys off the table internal/optimizer owns,
// so a knob that moves upstream is renamed in one place rather than two.
var mayflyTuningFlagHelp = "Path to a JSON file tuning the Mayfly optimizer; keys " +
	strings.Join(optimizer.MayflyTuningKeys, ", ") +
	" are accepted, the last three inside a \"schedule\" block and the four before them " +
	"inside a \"convergence\" block, omitted keys keep the variant's own value, and " +
	"every key the file sets wins over the matching --mayfly-* flag"
