// Package assets holds the preset documents that ship inside the binary.
//
// The presets are embedded as a directory rather than one named file so that
// adding a sound is adding a file. What a preset is called in a UI comes from
// the document's own "name" field and what it is addressed by comes from its
// filename, so neither label lives in a second place that could drift from the
// thing it names.
package assets

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

// DefaultID is the preset every front end falls back to when it is given no
// choice. It is a filename stem, so renaming presets/default.json renames this.
const DefaultID = "default"

//go:embed presets/*.json
var presetFS embed.FS

const presetDir = "presets"

// Builtin describes one embedded preset without decoding it.
//
// Note travels alongside the label because a caller listing the presets is
// usually about to say which one is playing and at what pitch, and re-decoding
// the document to answer that would defeat the point of a listing.
type Builtin struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Note int    `json:"note"`
}

// ids caches the sorted filename stems. The embedded FS cannot change at
// runtime, so the directory is read once; the sync.OnceValues pair keeps a
// malformed embed reportable rather than turning it into a panic at init time.
var ids = sync.OnceValues(func() ([]string, error) {
	entries, err := fs.ReadDir(presetFS, presetDir)
	if err != nil {
		return nil, fmt.Errorf("read embedded presets: %w", err)
	}

	names := make([]string, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		names = append(names, strings.TrimSuffix(entry.Name(), ".json"))
	}

	slices.Sort(names)

	return names, nil
})

// IDs returns the identifiers of every embedded preset, sorted.
//
// Sorting is not cosmetic: fs.ReadDir's order is the embed tool's, and a
// listing that reorders itself between builds would reorder a menu with it.
func IDs() ([]string, error) {
	names, err := ids()
	if err != nil {
		return nil, err
	}

	return slices.Clone(names), nil
}

// Preset decodes the embedded preset with the given id.
//
// An empty id is the default, so a caller threading an optional choice through
// -- a JS argument, a flag, a form field -- does not need its own empty check.
func Preset(presetID string) (*preset.Preset, error) {
	if presetID == "" {
		presetID = DefaultID
	}

	data, err := Document(presetID)
	if err != nil {
		return nil, err
	}

	return preset.Decode(data, "embedded preset "+presetID)
}

// Document returns the raw JSON of the embedded preset with the given id.
//
// It is Preset without the decode, for the callers that want the document
// itself rather than what it means: the code generator that mirrors the
// presets into the browser bundle hands them on as the starting preset of a
// fit, which is parsed at the far end by the same decoder. Handing on a
// re-encoding of a decoded preset instead would let a document lose anything
// the decoder does not carry.
//
// An empty id is the default, exactly as in Preset.
func Document(presetID string) ([]byte, error) {
	if presetID == "" {
		presetID = DefaultID
	}

	// The id becomes a path element, so it has to be a bare filename stem. Both
	// checks matter: path.Base would silently accept "../../etc/passwd" by
	// turning it into "passwd", and embed.FS would then answer for a preset the
	// caller did not ask for instead of reporting an unknown name.
	if presetID != path.Base(presetID) || strings.ContainsRune(presetID, '/') {
		return nil, fmt.Errorf("preset %q: not a preset name", presetID)
	}

	data, err := presetFS.ReadFile(path.Join(presetDir, presetID+".json"))
	if err != nil {
		known, listErr := IDs()
		if listErr != nil {
			return nil, listErr
		}

		return nil, fmt.Errorf("unknown preset %q, have %s", presetID, strings.Join(known, ", "))
	}

	return data, nil
}

// List describes every embedded preset, in id order.
//
// It decodes each document, because Name and Note are in them and nowhere else.
// That is a handful of small JSON parses at startup or at code-generation time,
// never on the audio thread.
func List() ([]Builtin, error) {
	names, err := IDs()
	if err != nil {
		return nil, err
	}

	listed := make([]Builtin, 0, len(names))

	for _, id := range names {
		decoded, err := Preset(id)
		if err != nil {
			return nil, err
		}

		listed = append(listed, Builtin{ID: id, Name: decoded.Name, Note: decoded.Note})
	}

	if len(listed) == 0 {
		return nil, errors.New("no embedded presets")
	}

	return listed, nil
}

// DefaultPreset loads the built-in web/CLI preset from embedded JSON.
func DefaultPreset() (*preset.Preset, error) {
	return Preset(DefaultID)
}
