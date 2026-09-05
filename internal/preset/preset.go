package preset

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cwbudde/algo-glockenspiel/model"
)

const (
	// VersionV1 is the original schema: exactly v1ModeCount modes, no per-mode
	// harmonics, and a Chebyshev shaper that always sits on the excitation.
	VersionV1 = "1.0"

	// VersionV2 adds a variable-length mode array, per-mode harmonic partials
	// and an explicit Chebyshev stage.
	VersionV2 = "2.0"

	// VersionV3 adds output_gain_db, the level the bar renders at.
	//
	// It is a version of its own rather than another v2 field because of what
	// an older reader does with it. A v2 reader accepts the document, ignores
	// the key it does not know, and renders at unity -- up to 60 dB from the
	// level the preset was calibrated to, with no error anywhere. A reader that
	// does not know v3 refuses the document instead, which is the only
	// behaviour that is safe when the unknown field decides how loud the
	// instrument is. The rule the ladder follows: a field a reader can ignore
	// without changing the sound may extend a version; a field it cannot must
	// start a new one.
	VersionV3 = "3.0"

	// VersionV4 adds decay_keytrack, the exponent transposition raises the
	// frequency ratio to before dividing a decay by it.
	//
	// It starts a version by the rule VersionV3 states, and the harm is the same
	// shape. A v3 reader accepts the document, ignores the key, and divides
	// every decay by the full ratio. It renders correctly at exactly one note --
	// the one the preset was authored at -- and diverges monotonically from
	// there: at the -0.24 a metallophone measures, a preset authored at note 93
	// wants 1.58 times its written decay at the bottom key and a v3 reader gives
	// it 2.65 times, with no error anywhere.
	VersionV4 = "4.0"

	// CurrentVersion is the newest version on the ladder: the most a reader in
	// this repo is expected to understand.
	//
	// It is deliberately not "the version new presets are written in". A writer
	// asks MinimumVersion what its document needs, so a preset carrying no v4
	// field is not a v4 document however new v4 is. Stamping this constant on
	// everything is what closed every calibrated preset to older readers the
	// moment v4 landed.
	CurrentVersion = VersionV4

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

	// Provenance records where a fitted preset came from. It is metadata and
	// not a schema field: Validate ignores it, and a v1 document that carries
	// one is still a v1 document, because nothing about it changes how the
	// parameters render.
	Provenance *Provenance `json:"provenance,omitempty"`
}

// Provenance is the record a fit leaves in the preset it writes: which
// reference it was fitted against, which engine found it, what it scored, and
// which build did the work. It is what makes a preset in a campaign directory
// answer "where did this come from" without the run directory beside it.
type Provenance struct {
	// GeneratedBy names the tool, "glockenspiel fit" or the campaign harness.
	GeneratedBy string `json:"generated_by"`

	// Version is the build's revision. What an unstamped build writes here
	// depends on the writer: the fit command writes its own version variable,
	// which is "dev" in a plain `go build`, while a campaign job writes the
	// build identity's revision, which is "unknown". Both mean the same thing,
	// that nothing named the commit this preset came from.
	Version string `json:"version"`

	Timestamp time.Time `json:"timestamp"`

	// Reference identifies the recording the fit was scored against. The hash
	// is what makes two presets comparable: the path alone is a name someone
	// can reuse for a different recording.
	//
	// A joint fit scored against several recordings fills References instead
	// and leaves this at the lowest of them, so a reader that knows only about
	// this field still names a real recording the fit used rather than a file
	// that was never opened.
	Reference ReferenceProvenance `json:"reference"`

	// References are every recording a joint fit was scored against, with the
	// note each one sounds and the score the shipped preset reached on it.
	// Empty for a fit of a single recording, which is what Reference alone
	// already describes.
	//
	// The per-note scores are here rather than only in the run directory
	// because they are the thing a single transposed preset cannot make
	// uniform: a preset whose mean score is good because it fits three notes
	// and abandons seventeen is a different object from one that fits all
	// twenty adequately, and nothing else the preset carries tells them apart.
	References []NoteReferenceProvenance `json:"references,omitempty"`

	Note    int    `json:"note"`
	Profile string `json:"profile"`
	Seed    int64  `json:"seed"`

	Engine EngineProvenance `json:"engine"`

	Score float64 `json:"score"`

	// Terms is the optimizer.Metrics JSON of the shipped vector. It is a raw
	// message so this package keeps no dependency on the optimizer, which
	// depends on it.
	Terms json.RawMessage `json:"terms"`

	Evaluations int `json:"evaluations"`

	// Libraries are the search library versions the run was built against,
	// keyed by short name, because a change in either moves the numbers.
	Libraries map[string]string `json:"libraries"`
}

// ReferenceProvenance identifies the recording a fit was scored against.
type ReferenceProvenance struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// NoteReferenceProvenance is one recording of a joint fit: the file, the note
// it sounds, and what the shipped preset scored against it.
type NoteReferenceProvenance struct {
	Path   string  `json:"path"`
	SHA256 string  `json:"sha256"`
	Note   int     `json:"note"`
	Score  float64 `json:"score"`
}

// EngineProvenance is the search that produced the preset, in the fields that
// differ between engines. The ones that do not apply are omitted rather than
// written as zero, so a mayfly block does not claim a population of nothing.
type EngineProvenance struct {
	Name       string `json:"name"`
	Covariance string `json:"covariance,omitempty"`
	Variant    string `json:"variant,omitempty"`
	Lambda     int    `json:"lambda,omitempty"`
	Population int    `json:"population,omitempty"`
	Restarts   int    `json:"restarts,omitempty"`
}

// Clone returns a deep copy of the preset.
func (p *Preset) Clone() *Preset {
	if p == nil {
		return nil
	}

	clone := *p
	clone.Parameters = p.Parameters.Clone()
	clone.Provenance = p.Provenance.Clone()

	return &clone
}

// Clone returns a deep copy of the provenance block.
func (p *Provenance) Clone() *Provenance {
	if p == nil {
		return nil
	}

	clone := *p

	if p.Terms != nil {
		clone.Terms = append(json.RawMessage(nil), p.Terms...)
	}

	if p.Libraries != nil {
		clone.Libraries = make(map[string]string, len(p.Libraries))
		for name, version := range p.Libraries {
			clone.Libraries[name] = version
		}
	}

	// References is a slice, so `clone := *p` copied only its header. Every
	// element is a value with no pointers of its own, so one copy of the
	// backing array is the whole of the deep copy.
	if p.References != nil {
		clone.References = append([]NoteReferenceProvenance(nil), p.References...)
	}

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

	if err := rejectNewerFields(data, preset.Version); err != nil {
		return nil, fmt.Errorf("validate preset %q: %w", source, err)
	}

	return &preset, nil
}

// rejectNewerFields reports a field that is present in a document older than
// the version that introduced it. The value checks in validateV1 and
// validateV2 cannot do this on their own: an explicit "stage": "",
// "harmonics": [] or "output_gain_db": 0 is indistinguishable from an omitted
// field once it has been decoded, so presence has to be read off the raw JSON.
func rejectNewerFields(data []byte, version string) error {
	var raw struct {
		Parameters struct {
			Modes []struct {
				Harmonics *json.RawMessage `json:"harmonics"`
			} `json:"modes"`
			Chebyshev struct {
				Stage *json.RawMessage `json:"stage"`
			} `json:"chebyshev"`
			// Not *json.RawMessage: the decoder resolves a JSON null to a
			// nil pointer before RawMessage's own Unmarshaler ever runs, so
			// the pointer form cannot tell `"x":null` from an absent key.
			// A bare RawMessage is left empty when the key is absent and
			// holds the four bytes of "null" when it is present, which is
			// the distinction this whole function exists to make.
			OutputGainDB  json.RawMessage `json:"output_gain_db"`
			DecayKeytrack json.RawMessage `json:"decay_keytrack"`
		} `json:"parameters"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("decode schema fields: %w", err)
	}

	if version == VersionV1 {
		for i, mode := range raw.Parameters.Modes {
			if mode.Harmonics != nil {
				return fmt.Errorf("modes[%d].harmonics needs version %s", i, VersionV2)
			}
		}

		if raw.Parameters.Chebyshev.Stage != nil {
			return fmt.Errorf("chebyshev.stage needs version %s", VersionV2)
		}
	}

	// Written as "older than the version that introduced it" rather than as
	// "not exactly that version". The latter is what stood here, and it was
	// correct only while v3 was the newest version: the moment v4 existed it
	// would have refused every calibrated preset a fit writes, since those
	// carry an output gain and are written in the current version.
	if OlderThan(version, VersionV3) && len(raw.Parameters.OutputGainDB) > 0 {
		return fmt.Errorf("output_gain_db needs version %s", VersionV3)
	}

	// The raw probe is needed even though BarParams.DecayKeytrack is a pointer,
	// because an explicit null decodes to a nil pointer there too and the
	// decoded value cannot tell that from an absent key.
	if OlderThan(version, VersionV4) && len(raw.Parameters.DecayKeytrack) > 0 {
		return fmt.Errorf("decay_keytrack needs version %s", VersionV4)
	}

	return nil
}

// MinimumVersion is the oldest schema version whose field set can carry these
// parameters: v4 once a keytrack is present, v3 once an output gain is, and v2
// otherwise.
//
// It is deliberately never v1. v1 is a shape, not just a field set -- exactly
// four modes, no per-mode harmonics, an implicit shaper stage -- and a writer
// choosing a version for a document it is about to save should not be able to
// pick a version that constrains the document's structure.
//
// The point of the function is that a document is stamped for what it holds
// rather than for whenever it was written. A preset carrying no keytrack
// renders identically under every reader from v3 onwards, so stamping it v4
// would lock out a v3 reader -- the external module that hand-rolls its own
// decode among them -- and buy nothing. The schema version a file claims is a
// statement about what a reader must understand, not a timestamp.
func MinimumVersion(params *model.BarParams) string {
	switch {
	case params.DecayKeytrack != nil:
		return VersionV4
	case params.OutputGainDB != 0:
		return VersionV3
	default:
		return VersionV2
	}
}

// Stamp sets a preset's version to the oldest one that can carry the fields it
// now holds, never lowering a version it already claims.
//
// It is what a writer calls after mutating a document, and the ordering is the
// point: Upgrade cannot know about a field set after it ran, so a caller that
// upgrades and then writes a field has produced a document whose version does
// not cover it. That sequence is exactly how "output_gain_db needs version 3.0"
// gets raised about a preset the caller had just upgraded.
func Stamp(p *Preset) {
	if minimum := MinimumVersion(&p.Parameters); OlderThan(p.Version, minimum) {
		p.Version = minimum
	}
}

// Upgrade returns an equivalent preset in the oldest schema version that can
// carry it, never below the version it already claims. The v1 defaults it makes
// explicit -- the excitation-stage shaper, no per-mode harmonics -- are exactly
// the ones the v1 loader applies, so the upgraded preset renders identically to
// the original.
//
// It does not stamp CurrentVersion. Restamping a document with a version whose
// fields it does not use costs it every older reader and gains it nothing, and
// the cost is real: model/ is imported by an external module that hand-rolls
// its own decode and cannot follow the ladder. So a v3 preset with no keytrack
// stays v3, and only a preset that actually carries a keytrack becomes v4.
func Upgrade(preset *Preset) (*Preset, error) {
	if err := Validate(preset); err != nil {
		return nil, err
	}

	upgraded := preset.Clone()
	Stamp(upgraded)

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
	case VersionV3:
		return validateV3(&preset.Parameters)
	case VersionV4:
		return validateV4(&preset.Parameters)
	default:
		return fmt.Errorf("unsupported version %q, want %q, %q, %q or %q",
			preset.Version, VersionV1, VersionV2, VersionV3, VersionV4)
	}
}

// versionRank orders the ladder, so a "needs version X" gate can be written as
// "this document is older than X" rather than as a chain of inequalities
// against each individual version.
//
// It exists because the ad-hoc form had already gone wrong once. The
// output_gain_db gate read `version != VersionV3`, which was correct while v3
// was the newest version and became a bug the moment v4 existed: every
// calibrated preset a fit writes carries an output gain, so a v4 document would
// have been refused for holding a field v3 introduced.
func versionRank(version string) int {
	switch version {
	case VersionV1:
		return 1
	case VersionV2:
		return 2
	case VersionV3:
		return 3
	case VersionV4:
		return 4
	default:
		return 0
	}
}

// OlderThan reports whether a document's version predates the version a field
// was introduced in. It is the one place the ladder's order is expressed, so a
// caller asking "can this document carry that field" never spells it as a
// string comparison against whichever version happened to be current when the
// caller was written -- the mistake that made every v4 preset unreadable the
// moment v4 existed.
func OlderThan(version, introducedIn string) bool {
	return versionRank(version) < versionRank(introducedIn)
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

	if params.OutputGainDB != 0 {
		return fmt.Errorf("output_gain_db needs version %s", VersionV3)
	}

	if params.DecayKeytrack != nil {
		return fmt.Errorf("decay_keytrack needs version %s", VersionV4)
	}

	return nil
}

func validateV2(params *model.BarParams) error {
	if len(params.Modes) == 0 {
		return errors.New("at least one mode is required")
	}

	if params.OutputGainDB != 0 {
		return fmt.Errorf("output_gain_db needs version %s", VersionV3)
	}

	if params.DecayKeytrack != nil {
		return fmt.Errorf("decay_keytrack needs version %s", VersionV4)
	}

	return nil
}

// validateV3 holds a v3 document to the v2 rules; the only thing v3 adds is
// output_gain_db, which every version validates for range in
// model.ValidateBarParams.
func validateV3(params *model.BarParams) error {
	if len(params.Modes) == 0 {
		return errors.New("at least one mode is required")
	}

	// Exact, unlike the output_gain_db gates above: the field is a pointer, so
	// absence is distinguishable from an explicit value and this check needs no
	// help from rejectNewerFields to tell "no keytrack" from "a keytrack of
	// exactly the default".
	if params.DecayKeytrack != nil {
		return fmt.Errorf("decay_keytrack needs version %s", VersionV4)
	}

	return nil
}

// validateV4 holds a v4 document to the v2 rules; the only thing v4 adds is
// decay_keytrack, whose range model.ValidateBarParams checks for every version.
func validateV4(params *model.BarParams) error {
	if len(params.Modes) == 0 {
		return errors.New("at least one mode is required")
	}

	return nil
}
