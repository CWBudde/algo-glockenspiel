package fitrun

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
)

// runConfig is config.json: everything needed to say what this run was, apart
// from what it found.
//
// It is a record of its own rather than the Spec marshalled directly, for two
// reasons. The Spec carries a whole template preset and a polish block with a
// callback in it, neither of which belongs in a description of the run, and
// the file is read by later tooling that should not move whenever a field is
// added to the Spec for the runner's own convenience.
type runConfig struct {
	Dir              string            `json:"dir"`
	ReferenceOptions loadOptionsRecord `json:"reference_options"`
	Template         templateRecord    `json:"template"`
	Modes            int               `json:"modes"`
	Note             int               `json:"note"`
	Velocity         int               `json:"velocity"`
	SampleRate       int               `json:"sample_rate"`
	Metric           optimizer.Metric  `json:"metric"`
	Engine           Engine            `json:"engine"`
	MaxIterations    int               `json:"max_iterations"`
	MaxEvaluations   int               `json:"max_evaluations"`
	TimeBudget       string            `json:"time_budget"`
	Seed             int64             `json:"seed"`
	Workers          int               `json:"workers"`
	ReportEvery      int               `json:"report_every"`
	CheckpointEvery  int               `json:"checkpoint_every"`
	Polish           *polishRecord     `json:"polish,omitempty"`
	GeneratedBy      string            `json:"generated_by"`
	Name             string            `json:"name,omitempty"`

	// Bounds, StrictBounds, Alignment and Gain are left out entirely when the
	// spec never touched them, so a campaign job's config.json (which never
	// sets any of the four) keeps the field set it wrote before this task:
	// see TestCampaignSpecStillWritesTheSameConfigFields. A served fit that
	// uploads its own box, turns alignment off, or asks for gain
	// normalisation writes them, which is the point of recording a served
	// run at all -- two fits differing only in one of these must not write
	// byte-identical config.json.
	Bounds       *boundsRecord `json:"bounds,omitempty"`
	StrictBounds bool          `json:"strict_bounds,omitempty"`
	Alignment    *string       `json:"alignment,omitempty"`
	Gain         string        `json:"gain,omitempty"`

	// SearchDecayKeytrack is written only when the run asked for it, so a
	// campaign job's config.json keeps the field set it wrote before the
	// exponent existed. Only a joint fit can ask.
	SearchDecayKeytrack bool `json:"search_decay_keytrack,omitempty"`

	Resolved Resolved `json:"resolved"`
	Identity Identity `json:"identity"`

	// Reference is written for a fit of a single recording and References for
	// a joint fit of several, never both. They are not one field carrying one
	// or many entries because the singular name is already in five archived
	// run directories and is read by name; a joint fit that wrote its lowest
	// note there would describe a twenty-note search with one file and look
	// entirely well-formed doing it.
	Reference  *referenceRecord      `json:"reference,omitempty"`
	References []noteReferenceRecord `json:"references,omitempty"`

	Started  time.Time  `json:"started"`
	Finished *time.Time `json:"finished,omitempty"`
}

// noteReferenceRecord is one recording of a joint fit, with the note it sounds.
type noteReferenceRecord struct {
	referenceRecord

	Note int `json:"note"`
}

// loadOptionsRecord is analysis.LoadOptions in the snake_case this file's
// other blocks use. The option struct itself carries no JSON tags, and a
// recorded run should not read half in Go field names.
//
// The values recorded are the resolved ones, not the ones the caller happened
// to leave blank. A campaign compares runs planned by different callers, and a
// blank downmix beside the reference block's "first" reads as a difference
// where there is none. A zero window and a false keep_level need no such
// resolution: they are the automatic cut and the peak normalisation, which is
// what the fields already say.
type loadOptionsRecord struct {
	Downmix   analysis.Downmix `json:"downmix"`
	WindowMS  int64            `json:"window_ms"`
	KeepLevel bool             `json:"keep_level"`
}

func newLoadOptionsRecord(options analysis.LoadOptions) loadOptionsRecord {
	downmix, err := analysis.ParseDownmix(string(options.Downmix))
	if err != nil {
		// An unsupported downmix never reaches here, because loading the
		// reference refused it long before the config was written. Recording
		// what was asked for is the honest fallback if it ever does.
		downmix = options.Downmix
	}

	return loadOptionsRecord{
		Downmix:   downmix,
		WindowMS:  options.Window.Milliseconds(),
		KeepLevel: options.KeepLevel,
	}
}

// boundsRecord is optimizer.ParamBounds in this file's snake_case, recorded
// only when a caller supplied its own box: the default box is a function of
// the measured reference, not a constant, so writing it here would just
// duplicate what config.json's reference block and resolved.frequency_bounds
// already say.
type boundsRecord struct {
	InputMix     rangeRecord `json:"input_mix"`
	FilterFreq   rangeRecord `json:"filter_freq"`
	Amplitude    rangeRecord `json:"amplitude"`
	Frequency    rangeRecord `json:"frequency"`
	DecayMs      rangeRecord `json:"decay_ms"`
	HarmonicGain rangeRecord `json:"harmonic_gain"`
}

// rangeRecord is optimizer.Range in this file's snake_case.
type rangeRecord struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

func newBoundsRecord(bounds optimizer.ParamBounds) boundsRecord {
	toRecord := func(r optimizer.Range) rangeRecord {
		return rangeRecord{Min: r.Min, Max: r.Max}
	}

	return boundsRecord{
		InputMix:     toRecord(bounds.InputMix),
		FilterFreq:   toRecord(bounds.FilterFreq),
		Amplitude:    toRecord(bounds.Amplitude),
		Frequency:    toRecord(bounds.Frequency),
		DecayMs:      toRecord(bounds.DecayMs),
		HarmonicGain: toRecord(bounds.HarmonicGain),
	}
}

// alignmentName names an alignment mode the way this file's other enums are
// named: lower snake_case, matching the CLI and server's own vocabulary.
func alignmentName(mode optimizer.AlignmentMode) string {
	if mode == optimizer.AlignOnsetCorrelation {
		return "onset_correlation"
	}

	return "none"
}

// gainName names a gain mode the same way. GainNone is the common case and is
// named "" so runConfig.Gain's omitempty drops it: a run that never asked for
// gain normalisation should not clutter config.json with a field saying so.
func gainName(mode optimizer.GainMode) string {
	if mode == optimizer.GainLeastSquares {
		return "least_squares"
	}

	return ""
}

// templateRecord names the starting preset. The parameters themselves are not
// recorded: the run's own preset.json holds a fitted vector in the same shape,
// and the seeding step rewrites most of the template anyway.
type templateRecord struct {
	Name string `json:"name"`
	Note int    `json:"note"`
}

// polishRecord is PolishOptions without its report callback, which cannot be
// marshalled and describes nothing about the run.
type polishRecord struct {
	Engine        string  `json:"engine"`
	Sigma         float64 `json:"sigma,omitempty"`
	MaxIterations int     `json:"max_iterations,omitempty"`
	TimeBudget    string  `json:"time_budget,omitempty"`
	Seed          int64   `json:"seed,omitempty"`
	MaxWorkers    int     `json:"max_workers,omitempty"`
}

// referenceRecord pins the recording: the path as given, its hash, and what
// the loader did to it.
type referenceRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`

	analysis.Reference
}

func newPolishRecord(options *optimizer.PolishOptions) *polishRecord {
	if options == nil {
		return nil
	}

	record := &polishRecord{
		Engine:        options.Engine,
		Sigma:         options.Sigma,
		MaxIterations: options.MaxIterations,
		Seed:          options.Seed,
		MaxWorkers:    options.MaxWorkers,
	}

	if options.TimeBudget > 0 {
		record.TimeBudget = options.TimeBudget.String()
	}

	return record
}

// writeJSONFile writes an indented JSON document with a trailing newline, the
// shape every other document this repository writes has.
func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %q: %w", path, err)
	}

	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory for %q: %w", path, err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}

	return nil
}

// setOutputGain writes the level the fitted preset renders at, and reports a
// clamp on the writer.
//
// This is the step that stops a fit from shipping at whatever level it happened
// to land on: the objective scores every candidate with the level solved in
// closed form and subtracted, so level is a flat ridge the search wanders
// along. See synth.ApplyOutputGain for what it measures and
// synth.PresetPeakTargetDBFS for why the target is not the reference's own
// level.
func setOutputGain(fitted *preset.Preset, out io.Writer) error {
	gainDB, clamped, err := synth.ApplyOutputGain(fitted)
	if err != nil {
		return err
	}

	if clamped {
		_, _ = fmt.Fprintf(out,
			"output gain: %+.2f dB, clamped at the bound; the fit renders far enough from the target "+
				"that the preset stays off it\n", gainDB)
	}

	return nil
}

// renderPreset writes the fitted preset rendered for as long as the reference
// lasts, so the two files can be compared sample for sample.
func renderPreset(path string, fitted *preset.Preset, sampleRate, note, velocity, referenceSamples int) error {
	engine, err := synth.NewSynthesizer(fitted, sampleRate)
	if err != nil {
		return fmt.Errorf("build synthesizer for the render: %w", err)
	}

	seconds := float64(referenceSamples) / float64(sampleRate)
	samples := engine.RenderNote(note, velocity, seconds)

	if err := wavio.WriteMono(path, sampleRate, samples); err != nil {
		return fmt.Errorf("write render: %w", err)
	}

	return nil
}

// provenanceFor builds the block the fitted preset carries. It is the summary
// in the fields a preset on its own can be judged by, which is why it repeats
// values result.json also holds: a preset copied out of a run directory has to
// answer for itself.
func provenanceFor(spec Spec, identity Identity, prepared *preparation, summary Summary, chosen Resolved) (*preset.Provenance, error) {
	reference := prepared.reference

	terms, err := json.Marshal(summary.Terms)
	if err != nil {
		return nil, fmt.Errorf("encode provenance terms: %w", err)
	}

	// Only the fields the engine actually has are filled in, so a mayfly block
	// carries a variant and a population and a CMA-ES block a covariance mode
	// and a lambda, rather than both carrying zeros for the other's settings.
	engine := preset.EngineProvenance{Name: spec.Engine.Name}

	switch spec.Engine.Name {
	case EngineMayfly:
		engine.Variant = chosen.Variant
		engine.Population = chosen.Population
		engine.Restarts = spec.Engine.Mayfly.Restarts
	case EngineCMAES:
		engine.Covariance = chosen.Covariance
		engine.Lambda = chosen.Lambda
		engine.Restarts = spec.Engine.CMAES.RestartLimit
	}

	return &preset.Provenance{
		GeneratedBy: spec.GeneratedBy,
		Version:     identity.Revision,
		Timestamp:   time.Now().UTC(),
		Reference:   preset.ReferenceProvenance{Path: reference.Path, SHA256: reference.SHA256},
		References:  noteReferenceProvenance(prepared, summary),
		Note:        spec.Note,
		Profile:     summary.Profile,
		Seed:        summary.Seed,
		Engine:      engine,
		Score:       summary.Score,
		Terms:       terms,
		Evaluations: summary.Evaluations,
		Libraries:   identity.Libraries,
	}, nil
}

// noteReferenceProvenance is the per-note block a joint fit's preset carries:
// every recording it was scored against and what it reached on each. Nil for a
// fit of a single recording, which Provenance.Reference already describes in
// full.
//
// The scores are looked up by note rather than taken positionally, because
// nothing forces the summary's per-note order and the preparation's to agree,
// and a table of scores silently attached to the wrong notes is the one error
// here that no reader would catch.
func noteReferenceProvenance(prepared *preparation, summary Summary) []preset.NoteReferenceProvenance {
	if len(summary.NoteTerms) == 0 {
		return nil
	}

	scores := make(map[int]float64, len(summary.NoteTerms))
	for _, note := range summary.NoteTerms {
		scores[note.Note] = note.Score
	}

	refs := make([]preset.NoteReferenceProvenance, 0, len(prepared.notes))

	for _, note := range prepared.notes {
		score, ok := scores[note.note]
		if !ok {
			score = math.NaN()
		}

		refs = append(refs, preset.NoteReferenceProvenance{
			Path:   note.record.Path,
			SHA256: note.record.SHA256,
			Note:   note.note,
			Score:  score,
		})
	}

	return refs
}
