package assets

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/cwbudde/glockenspiel/internal/preset"
)

//go:embed presets/default.json
var defaultPresetJSON []byte

// DefaultPreset loads the built-in web/CLI preset from embedded JSON.
func DefaultPreset() (*preset.Preset, error) {
	var cfg preset.Preset

	if err := json.Unmarshal(defaultPresetJSON, &cfg); err != nil {
		return nil, fmt.Errorf("decode embedded default preset: %w", err)
	}

	if err := preset.Validate(&cfg); err != nil {
		return nil, fmt.Errorf("validate embedded default preset: %w", err)
	}

	return &cfg, nil
}
