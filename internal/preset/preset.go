package preset

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cwbudde/glockenspiel/model"
)

const (
	// VersionV1 is the original schema: exactly v1ModeCount modes, no per-mode
	// harmonics, and a Chebyshev shaper that always sits on the excitation.
	VersionV1 = "1.0"

	// VersionV2 adds a variable-length mode array, per-mode harmonic partials
	// and an explicit Chebyshev stage.
	VersionV2 = "2.0"

	// CurrentVersion is the schema new presets are written in.
	CurrentVersion = VersionV2

	// v1ModeCount is the fixed mode count a v1 document carries. It belongs to
	// this compatibility layer, not to the model: the bank sizes itself at
	// runtime and has no fixed count to export.
	v1ModeCount = 4
)

// Preset describes a stored parameter configuration.
type Preset struct {
	Version    string          `json:"version"`
	Name       string          `json:"name"`
	Note       int             `json:"note"`
	Parameters model.BarParams `json:"parameters"`
}

// Clone returns a deep copy of the preset.
func (p *Preset) Clone() *Preset {
	if p == nil {
		return nil
	}

	clone := *p
	clone.Parameters = p.Parameters.Clone()

	return &clone
}

// Load parses and validates a preset from JSON.
func Load(path string) (*Preset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read preset %q: %w", path, err)
	}

	return Decode(data, path)
}

// Decode parses and validates preset JSON. source names the origin for error
// messages. Both schema versions are accepted; a v1 document is held to the v1
// rules so a file that quietly grew v2 fields is reported instead of rendering
// differently than its version claims.
func Decode(data []byte, source string) (*Preset, error) {
	var preset Preset

	if err := json.Unmarshal(data, &preset); err != nil {
		return nil, fmt.Errorf("decode preset %q: %w", source, err)
	}

	if err := Validate(&preset); err != nil {
		return nil, fmt.Errorf("validate preset %q: %w", source, err)
	}

	if preset.Version == VersionV1 {
		if err := rejectV2Fields(data); err != nil {
			return nil, fmt.Errorf("validate preset %q: %w", source, err)
		}
	}

	return &preset, nil
}

// rejectV2Fields reports a v2-only field that is present in a v1 document. The
// value checks in validateV1 cannot do this on their own: an explicit
// "stage": "" or "harmonics": [] is indistinguishable from an omitted field
// once it has been decoded, so presence has to be read off the raw JSON.
func rejectV2Fields(data []byte) error {
	var raw struct {
		Parameters struct {
			Modes []struct {
				Harmonics *json.RawMessage `json:"harmonics"`
			} `json:"modes"`
			Chebyshev struct {
				Stage *json.RawMessage `json:"stage"`
			} `json:"chebyshev"`
		} `json:"parameters"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("decode schema fields: %w", err)
	}

	for i, mode := range raw.Parameters.Modes {
		if mode.Harmonics != nil {
			return fmt.Errorf("modes[%d].harmonics needs version %s", i, VersionV2)
		}
	}

	if raw.Parameters.Chebyshev.Stage != nil {
		return fmt.Errorf("chebyshev.stage needs version %s", VersionV2)
	}

	return nil
}

// Upgrade returns an equivalent preset in the current schema version. The v1
// defaults it makes explicit -- the excitation-stage shaper, no per-mode
// harmonics -- are exactly the ones the v1 loader applies, so the upgraded
// preset renders identically to the original.
func Upgrade(preset *Preset) (*Preset, error) {
	if err := Validate(preset); err != nil {
		return nil, err
	}

	upgraded := preset.Clone()
	upgraded.Version = CurrentVersion

	if upgraded.Parameters.Chebyshev.Stage == "" {
		upgraded.Parameters.Chebyshev.Stage = model.ChebyshevStageExcitation
	}

	return upgraded, nil
}

// Save validates and writes a preset to JSON, keeping the preset's own schema
// version so a round trip through Load and Save is lossless.
func Save(p *Preset, path string) error {
	if err := Validate(p); err != nil {
		return fmt.Errorf("validate preset before save: %w", err)
	}

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("encode preset: %w", err)
	}

	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create preset directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write preset %q: %w", path, err)
	}

	return nil
}

// Validate checks preset metadata and model parameter validity.
func Validate(preset *Preset) error {
	if preset == nil {
		return errors.New("preset cannot be nil")
	}

	if preset.Version == "" {
		return errors.New("version cannot be empty")
	}

	if preset.Name == "" {
		return errors.New("name cannot be empty")
	}

	if preset.Note < 0 || preset.Note > 127 {
		return fmt.Errorf("note out of MIDI range [0,127]: %d", preset.Note)
	}

	if err := validateSchema(preset); err != nil {
		return err
	}

	// Validated against the preset's own base note, not on its own: a preset is
	// only well-formed if it is still buildable at every note the keyboard can
	// send, and how far transposition stretches its decays depends on where that
	// base note sits. See model.ValidateAuthoredBarParams.
	if err := model.ValidateAuthoredBarParams(&preset.Parameters, preset.Note); err != nil {
		return fmt.Errorf("parameters: %w", err)
	}

	return nil
}

func validateSchema(preset *Preset) error {
	switch preset.Version {
	case VersionV1:
		return validateV1(&preset.Parameters)
	case VersionV2:
		return validateV2(&preset.Parameters)
	default:
		return fmt.Errorf("unsupported version %q, want %q or %q", preset.Version, VersionV1, VersionV2)
	}
}

func validateV1(params *model.BarParams) error {
	if len(params.Modes) != v1ModeCount {
		return fmt.Errorf("version %s requires exactly %d modes, got %d; use version %s for a variable mode count",
			VersionV1, v1ModeCount, len(params.Modes), VersionV2)
	}

	for i, mode := range params.Modes {
		if len(mode.Harmonics) > 0 {
			return fmt.Errorf("modes[%d].harmonics needs version %s", i, VersionV2)
		}
	}

	if params.Chebyshev.Stage != "" {
		return fmt.Errorf("chebyshev.stage needs version %s", VersionV2)
	}

	return nil
}

func validateV2(params *model.BarParams) error {
	if len(params.Modes) == 0 {
		return errors.New("at least one mode is required")
	}

	return nil
}
