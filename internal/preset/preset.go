package preset

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cwbudde/glockenspiel/internal/model"
)

// Preset describes a stored parameter configuration.
type Preset struct {
	Version    string          `json:"version"`
	Name       string          `json:"name"`
	Note       int             `json:"note"`
	Parameters model.BarParams `json:"parameters"`
}

// Load parses and validates a preset from JSON.
func Load(path string) (*Preset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read preset %q: %w", path, err)
	}

	var p Preset
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("decode preset %q: %w", path, err)
	}

	if err := Validate(&p); err != nil {
		return nil, fmt.Errorf("validate preset %q: %w", path, err)
	}

	return &p, nil
}

// Save validates and writes a preset to JSON.
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
func Validate(p *Preset) error {
	if p == nil {
		return errors.New("preset cannot be nil")
	}
	if p.Version == "" {
		return errors.New("version cannot be empty")
	}
	if p.Name == "" {
		return errors.New("name cannot be empty")
	}
	if p.Note < 0 || p.Note > 127 {
		return fmt.Errorf("note out of MIDI range [0,127]: %d", p.Note)
	}
	if err := p.Parameters.Validate(); err != nil {
		return fmt.Errorf("parameters: %w", err)
	}
	return nil
}
