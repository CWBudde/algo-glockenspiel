package assets

import (
	_ "embed"

	"github.com/cwbudde/glockenspiel/internal/preset"
)

//go:embed presets/default.json
var defaultPresetJSON []byte

// DefaultPreset loads the built-in web/CLI preset from embedded JSON.
func DefaultPreset() (*preset.Preset, error) {
	return preset.Decode(defaultPresetJSON, "embedded default preset")
}
