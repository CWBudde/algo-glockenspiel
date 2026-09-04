package server

// Restart recovery. A run directory is the whole record of a fit -- what was
// asked for, what the backend chose, what it found -- so the job history does
// not have to live only in the process that made it. On startup the work
// directory is read back and every run in it becomes a terminal job the read
// endpoints still answer for.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// interruptedFailure is what a run that never finished records. It is a
// failure rather than a cancellation because nobody asked for it: the process
// went away mid-search, which is a different thing from a user pressing stop,
// and a client that sees the two as one would count a crash as a decision.
const interruptedFailure = "the fit did not finish: the server running it stopped before the run was written"

// stopReasonInterrupted names that state in the vocabulary the other stop
// reasons are written in.
const stopReasonInterrupted = "interrupted"

// restoredConfig is the part of fitrun's config.json a rebuilt job needs.
//
// It is a reader of its own rather than fitrun's record, which is unexported
// on purpose -- config.json is written for later tooling, and this is later
// tooling. Only the fields the snapshot reports are read, so a config.json
// that grows a field is still read by an older binary.
type restoredConfig struct {
	Note          int    `json:"note"`
	Velocity      int    `json:"velocity"`
	SampleRate    int    `json:"sample_rate"`
	Metric        string `json:"metric"`
	Modes         int    `json:"modes"`
	MaxIterations int    `json:"max_iterations"`
	ReportEvery   int    `json:"report_every"`
	Seed          int64  `json:"seed"`

	// TimeBudget is a Go duration as time.Duration.String wrote it, which is
	// what config.json records; the snapshot echoes milliseconds, so it is
	// parsed back rather than passed through.
	TimeBudget string `json:"time_budget"`

	// Alignment is a pointer because its absence is not "none": a run that
	// never named an alignment took fitrun's default, which is the onset
	// correlation, and every campaign job's config.json leaves it out.
	Alignment *string `json:"alignment,omitempty"`
	Gain      string  `json:"gain,omitempty"`

	ReferenceOptions struct {
		Downmix  string `json:"downmix"`
		WindowMS int64  `json:"window_ms"`
	} `json:"reference_options"`

	// Engine is fitrun's own record rather than a name alone, so a rebuilt
	// job echoes the backend settings the run actually used.
	Engine fitrun.Engine `json:"engine"`

	Bounds *struct {
		InputMix     restoredRange `json:"input_mix"`
		FilterFreq   restoredRange `json:"filter_freq"`
		Amplitude    restoredRange `json:"amplitude"`
		Frequency    restoredRange `json:"frequency"`
		DecayMs      restoredRange `json:"decay_ms"`
		HarmonicGain restoredRange `json:"harmonic_gain"`
	} `json:"bounds,omitempty"`

	Reference struct {
		Seconds float64 `json:"seconds"`
	} `json:"reference"`

	Resolved fitrun.Resolved `json:"resolved"`
	Started  time.Time       `json:"started"`
	Finished *time.Time      `json:"finished,omitempty"`
}

// restoredRange is one dimension of a recorded search box.
type restoredRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// alignmentOnsetCorrelation and gainLeastSquares are how config.json spells
// the two modes the server's own request fields select. They are read here
// rather than exported from fitrun because they are values of a file, and a
// file's vocabulary is the reader's problem.
const (
	alignmentOnsetCorrelation = "onset_correlation"
	gainLeastSquares          = "least_squares"
)

// restoredRequest rebuilds the request a recorded run was started from.
//
// It is not the request itself, which nothing writes down: it is config.json
// read back into the shape the snapshot echoes, so a job the server did not
// run reports the same provenance as one it did. The mayfly form fields --
// the stagnation rule, the selection strategy, the crossover knobs -- are
// missing on purpose: they were folded into the tuning document before the
// run wrote anything, and the document is what config.json kept.
func restoredRequest(config *restoredConfig) fitRequest {
	settings := fitRequest{
		Note:          config.Note,
		Velocity:      config.Velocity,
		Modes:         config.Modes,
		Optimizer:     config.Engine.Name,
		Metric:        config.Metric,
		MaxIterations: config.MaxIterations,
		ReportEvery:   config.ReportEvery,
		Align:         config.Alignment == nil || *config.Alignment == alignmentOnsetCorrelation,
		NormalizeGain: config.Gain == gainLeastSquares,
		Downmix:       config.ReferenceOptions.Downmix,
		WindowMS:      config.ReferenceOptions.WindowMS,

		MayflyVariant:    config.Engine.Mayfly.Variant,
		MayflyPreset:     config.Engine.Mayfly.Preset,
		MayflyPopulation: config.Engine.Mayfly.Population,
		MayflyEpochs:     config.Engine.Mayfly.Epochs,
		MayflyRestarts:   config.Engine.Mayfly.Restarts,
		MayflyTuning:     config.Engine.Mayfly.Tuning,

		CmaesCovariance: config.Engine.CMAES.Covariance,
		CmaesLambda:     config.Engine.CMAES.Lambda,
		CmaesSigma:      config.Engine.CMAES.Sigma,
		CmaesRestarts:   config.Engine.CMAES.RestartLimit,
	}

	// One seed was recorded, because a run has one stream; which request
	// field it belongs in is which backend read it.
	if config.Engine.Name == cmaesOptimizerName {
		settings.CmaesSeed = config.Seed
	} else {
		settings.MayflySeed = config.Seed
	}

	if budget, err := time.ParseDuration(config.TimeBudget); err == nil {
		settings.TimeBudgetMS = budget.Milliseconds()
	}

	return settings
}

// restoredBounds rebuilds the search box a run recorded, or nil when it used
// the default one.
func restoredBounds(config *restoredConfig) *optimizer.ParamBounds {
	if config.Bounds == nil {
		return nil
	}

	asRange := func(recorded restoredRange) optimizer.Range {
		return optimizer.Range{Min: recorded.Min, Max: recorded.Max}
	}

	return &optimizer.ParamBounds{
		InputMix:     asRange(config.Bounds.InputMix),
		FilterFreq:   asRange(config.Bounds.FilterFreq),
		Amplitude:    asRange(config.Bounds.Amplitude),
		Frequency:    asRange(config.Bounds.Frequency),
		DecayMs:      asRange(config.Bounds.DecayMs),
		HarmonicGain: asRange(config.Bounds.HarmonicGain),
	}
}

// restoreJobs reads the work directory and rebuilds every run in it, oldest
// first, so that the history reads the same way it did before the restart.
//
// Nothing here is fatal. A work directory that does not exist yet is the
// ordinary first start; one that cannot be read costs the history and not the
// server, which still has an app to serve and fits to run.
func (s *Server) restoreJobs() {
	entries, err := os.ReadDir(s.config.WorkDir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.logf("the work directory %s could not be read: %v", s.config.WorkDir, err)
		}

		return
	}

	names := make([]string, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}

	// A run directory's name is a fixed-width UTC timestamp followed by a
	// fixed-width counter, so sorting the names is sorting the runs by when
	// they started, and the history comes back in the order it was made in.
	sort.Strings(names)

	// Only the newest are read. The cap is on the list a process holds, and
	// reading a thousand directories to throw nine hundred away would make
	// startup slow for nothing.
	if len(names) > maxStoredJobs {
		names = names[len(names)-maxStoredJobs:]
	}

	restored := 0

	for _, name := range names {
		job := restoreJob(filepath.Join(s.config.WorkDir, name), name)
		if job == nil {
			continue
		}

		s.jobs.adopt(job)

		restored++
	}

	if restored > 0 {
		s.logf("restored %d fit(s) from %s", restored, s.config.WorkDir)
	}
}

// restoreJob rebuilds one run directory as a terminal job, or returns nil when
// the directory is not one.
//
// A directory is a run exactly when it holds a config.json, which fitrun
// writes before the search starts. Whether the run finished is then decided by
// result.json, which fitrun writes after it: that is the rule
// internal/campaign/status.go already reads a campaign's directories by, and
// it has to read the same way here because these are the same directories.
func restoreJob(dir, id string) *fitJob {
	config, err := readRestoredConfig(dir)
	if err != nil {
		return nil
	}

	job := &fitJob{
		id:               id,
		dir:              dir,
		request:          restoredRequest(config),
		bounds:           restoredBounds(config),
		startedAt:        config.Started,
		sampleRate:       config.SampleRate,
		referenceSeconds: config.Reference.Seconds,
		// A rebuilt job has no goroutine to stop, so its cancel is a no-op
		// rather than nil: everything that stops a job calls it unconditionally
		// and a nil here would be a panic on shutdown.
		cancel:       func() {},
		done:         make(chan struct{}),
		subscribers:  make(map[chan struct{}]struct{}),
		resolved:     config.Resolved,
		presetOnDisk: fileExists(filepath.Join(dir, fitrun.FilePreset)),
	}

	summary, err := readRestoredSummary(dir)
	if err != nil {
		// A missing result.json is a run that died with the process that owned
		// it; an unreadable one is a run whose record cannot be trusted. Both
		// are failures rather than successes, and neither is a cancellation:
		// the difference is only what the message says.
		job.state = fitFailed
		job.stopReason = stopReasonInterrupted
		job.failure = interruptedFailure

		if !errors.Is(err, fs.ErrNotExist) {
			job.failure = fmt.Sprintf("the fit's own summary could not be read: %v", err)
		}
	} else {
		applySummary(job, summary)
	}

	job.finishedAt = restoredFinish(dir, config)

	// The job is terminal from the moment it exists, so everything that waits
	// on a job -- the cancel endpoint, the event stream -- is satisfied at once
	// rather than blocking on a goroutine that will never run.
	close(job.done)

	return job
}

// applySummary fills a rebuilt job in from what the run recorded.
func applySummary(job *fitJob, summary *fitrun.Summary) {
	job.state = fitSucceeded
	if summary.StopReason == fitrun.StopReasonCanceled {
		job.state = fitCanceled
	}

	terms := summary.Terms

	job.summary = summary
	job.metrics = &terms
	job.converged = summary.Converged
	job.stopReason = summary.StopReason
	job.seededModes = summary.SeededModes
	job.progress = optimizer.Progress{
		OptimizerIterations: summary.Iterations,
		Evaluations:         summary.Evaluations,
		BestCost:            summary.Score,
		// A finished run has no candidate under evaluation any more, so its
		// current position is the best one it found. It is the same rule
		// fitJob.finish applies to a run this process watched.
		CurrentCost: summary.Score,
		Restart:     summary.Restarts,
		Elapsed:     time.Duration(summary.ElapsedSeconds * float64(time.Second)),
	}
}

// restoredFinish decides when a rebuilt job ended.
//
// config.json's own finished stamp is the honest answer when there is one:
// fitrun writes it in the same pass that writes the summary. A run that never
// got that far has result.json's modification time at best, and its start at
// worst -- a finished job with no end time at all would read as one that is
// somehow still going.
func restoredFinish(dir string, config *restoredConfig) time.Time {
	if config.Finished != nil {
		return *config.Finished
	}

	if info, err := os.Stat(filepath.Join(dir, fitrun.FileResult)); err == nil {
		return info.ModTime()
	}

	return config.Started
}

// readRestoredConfig reads a run directory's config.json.
func readRestoredConfig(dir string) (*restoredConfig, error) {
	raw, err := os.ReadFile(filepath.Join(dir, fitrun.FileConfig))
	if err != nil {
		return nil, err
	}

	var config restoredConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("decode %s: %w", fitrun.FileConfig, err)
	}

	return &config, nil
}

// readRestoredSummary reads a run directory's result.json.
func readRestoredSummary(dir string) (*fitrun.Summary, error) {
	raw, err := os.ReadFile(filepath.Join(dir, fitrun.FileResult))
	if err != nil {
		return nil, err
	}

	var summary fitrun.Summary
	if err := json.Unmarshal(raw, &summary); err != nil {
		return nil, fmt.Errorf("decode %s: %w", fitrun.FileResult, err)
	}

	return &summary, nil
}

// fileExists reports whether a regular file is there to be read.
func fileExists(path string) bool {
	info, err := os.Stat(path)

	return err == nil && !info.IsDir()
}
