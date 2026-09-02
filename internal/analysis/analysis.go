package analysis

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// GeneratedBy is the marker an analysis document carries, so a reader can
// tell one from any other JSON file with a partials field.
const GeneratedBy = "glockenspiel analyze"

// Analysis is the document written as analysis.json: the reference as it was
// cut, and the partials measured on that cut. Phase 8.2's objective and Phase
// 8.3's codec read it; nothing in it needs the audio to be interpreted.
type Analysis struct {
	// GeneratedBy is GeneratedBy.
	GeneratedBy string `json:"generated_by"`

	// Source is the path the reference was read from, as given.
	Source string `json:"source"`

	// Reference records the cut: onset, end, rule, downmix, gain.
	Reference Reference `json:"reference"`

	// Measurement is what was found on the cut.
	Measurement
}

// Analyze reads a reference and measures it under the two option sets.
func Analyze(path string, load LoadOptions, partials PartialOptions) (*Analysis, error) {
	reference, err := LoadReference(path, load)
	if err != nil {
		return nil, err
	}

	return AnalyzeReference(path, reference, partials)
}

// AnalyzeReference measures an already loaded reference. source names it in
// the document.
func AnalyzeReference(source string, reference *Reference, partials PartialOptions) (*Analysis, error) {
	measurement, err := Measure(reference.Samples, reference.SampleRate, partials)
	if err != nil {
		return nil, err
	}

	return &Analysis{
		GeneratedBy: GeneratedBy,
		Source:      source,
		Reference:   *reference,
		Measurement: *measurement,
	}, nil
}

// Write encodes the document as indented JSON.
func (a *Analysis) Write(writer io.Writer) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(a); err != nil {
		return fmt.Errorf("encode analysis: %w", err)
	}

	return nil
}

// WriteFile writes the document to path, creating the directory if needed.
func (a *Analysis) WriteFile(path string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create analysis directory: %w", err)
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create analysis %q: %w", path, err)
	}

	if err := a.Write(file); err != nil {
		_ = file.Close()

		return err
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("close analysis %q: %w", path, err)
	}

	return nil
}

// Read decodes a document. A file that does not carry the GeneratedBy marker
// is refused, since a partials field alone proves nothing about the rest.
func Read(reader io.Reader) (*Analysis, error) {
	var document Analysis
	if err := json.NewDecoder(reader).Decode(&document); err != nil {
		return nil, fmt.Errorf("decode analysis: %w", err)
	}

	if document.GeneratedBy != GeneratedBy {
		return nil, fmt.Errorf("not an analysis document: generated_by is %q, want %q", document.GeneratedBy, GeneratedBy)
	}

	return &document, nil
}

// ReadFile decodes the document at path.
func ReadFile(path string) (*Analysis, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open analysis %q: %w", path, err)
	}

	defer func() {
		_ = file.Close()
	}()

	document, err := Read(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return document, nil
}
