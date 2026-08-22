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
