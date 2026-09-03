package fitrun

import (
	"encoding/json"
	"fmt"
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

	Resolved  resolved        `json:"resolved"`
	Identity  Identity        `json:"identity"`
	Reference referenceRecord `json:"reference"`
	Started   time.Time       `json:"started"`
	Finished  *time.Time      `json:"finished,omitempty"`
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
func provenanceFor(spec Spec, identity Identity, reference referenceRecord, summary Summary, chosen resolved) (*preset.Provenance, error) {
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
