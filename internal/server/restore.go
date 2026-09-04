package server

// Restart recovery and live following. A run directory is the whole record of
// a fit -- what was asked for, what the backend chose, what it found -- so the
// job history does not have to live only in the process that made it. The work
// directory is read back at startup and then again on a timer, and every run in
// it becomes a job the read endpoints answer for: a finished one is terminal
// from the moment it is read, while one that is still being written is a
// running job this server follows by tailing its trace.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// interruptedFailure is what a run whose own record cannot be read comes back
// as. It is a failure rather than a cancellation because nobody asked for it:
// the file that says how the search ended is unreadable, which is a different
// thing from a user pressing stop, and a client that sees the two as one would
// count a broken record as a decision.
const interruptedFailure = "the fit's record could not be read: %v"

// stopReasonInterrupted names that state in the vocabulary the other stop
// reasons are written in.
const stopReasonInterrupted = "interrupted"

// followInterval is how often the work directory is read again.
//
// A second is short enough that a fit started in a terminal shows up in the
// browser while the person who started it is still looking at it, and that a
// followed run's cost curve moves at about the rate a served fit's does -- the
// SSE stream carries a report per trace line either way, and a report every few
// hundred milliseconds is already faster than a cost curve changes visibly. It
// is also long enough that the cost is nothing: a tick reads one directory
// listing and, per followed run, the bytes its trace has actually gained.
const followInterval = time.Second

// followedResultAttempts is how many consecutive ticks a result.json may fail
// to parse before the run is called broken rather than half-written. Five ticks
// is five seconds at the default interval, which is far longer than the gap
// between the create and the write of a file a few hundred bytes long, and far
// shorter than anybody watching would take to notice.
const followedResultAttempts = 5

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

// scanRuns reads the work directory and rebuilds every run in it, oldest
// first, so that the history reads the same way it did before the restart,
// then advances every run this server is following.
//
// It runs once from New, before the first request, and again on every tick of
// followRuns. The two are the same pass on purpose: a run directory that
// appears while the server is up -- a `glockenspiel fit` in another terminal, a
// campaign writing into the same work directory -- is the same thing as one
// that was already there when the server started, and reading it twice as two
// different kinds of thing is how the two would drift apart.
//
// Nothing here is fatal. A work directory that does not exist yet is the
// ordinary first start; one that cannot be read costs the history and not the
// server, which still has an app to serve and fits to run.
func (s *Server) scanRuns() {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()

	s.adoptNewRunsLocked()
	s.advanceFollowedLocked()
}

// adoptNewRunsLocked turns every run directory this server has not seen yet
// into a job. Caller holds scanMu.
func (s *Server) adoptNewRunsLocked() {
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

	adopted := 0

	for _, name := range names {
		// A directory is considered exactly once. The scanned set is what
		// makes that true even after the history has trimmed a job out of
		// itself: without it the oldest directory on disk would be adopted
		// again on the next tick, push the next-oldest out of the history, and
		// the two would take turns forever. The manager is asked as well,
		// because every job this server started has a directory here too and
		// none of them was ever scanned.
		if _, seen := s.scanned[name]; seen || s.jobs.lookup(name) != nil {
			continue
		}

		dir := filepath.Join(s.config.WorkDir, name)

		job, follow := restoreJob(dir, name)
		if job == nil {
			// Not a run directory -- or not yet one: a fit creates its
			// directory before it writes config.json into it, so this is a
			// directory that may well be a run by the next tick. It is
			// deliberately not marked as scanned.
			continue
		}

		s.jobs.adopt(job)

		if follow != nil {
			s.following = append(s.following, follow)
		}

		if s.scanned == nil {
			s.scanned = make(map[string]struct{})
		}

		s.scanned[name] = struct{}{}

		adopted++
	}

	if adopted > 0 {
		s.logf("picked up %d fit(s) from %s", adopted, s.config.WorkDir)
	}
}

// advanceFollowedLocked reads what every followed run has written since the
// last tick and retires the ones that have finished. Caller holds scanMu.
func (s *Server) advanceFollowedLocked() {
	live := s.following[:0]

	for _, follow := range s.following {
		// The trace first, then the result: a run writes its last trace line
		// before it writes result.json, so reading the other way round would
		// drop that line on the floor for a run that finished between two
		// ticks.
		follow.readTrace()

		if follow.finished() {
			continue
		}

		live = append(live, follow)
	}

	// The retired entries are cleared rather than merely left past the new
	// length, so a finished run's job -- and the whole preset, trace and
	// progress it holds -- is not kept alive by the backing array.
	for i := len(live); i < len(s.following); i++ {
		s.following[i] = nil
	}

	s.following = live
}

// followRuns rescans the work directory until ctx is cancelled.
//
// This is what retired the freshness window the startup scan used to guess
// with. A directory holding a config.json and no result.json used to be read as
// either "died with its process" or "probably still going", decided by how
// recently config.json had been written, because a single pass over the
// directory has nothing else to go on. A server that keeps looking does not
// have to guess: the run is running until result.json lands, and that is an
// observation rather than an estimate. The trade is the other direction --
// a run that really did die with its process now stays "running" in this
// server's history until somebody removes its directory -- and it is the
// better half of the two, because the live case is the common one and the dead
// case is visibly stalled.
func (s *Server) followRuns(ctx context.Context) {
	interval := s.followInterval
	if interval <= 0 {
		interval = followInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scanRuns()
		}
	}
}

// followedRun is one run directory this server is watching but did not start.
//
// The tail offset lives here rather than on the job because only the scan
// owns it: the job is read by every request goroutine, while this is touched
// by whichever goroutine holds scanMu and by nothing else.
type followedRun struct {
	job *fitJob

	// offset is how far into trace.jsonl the whole lines end. It never
	// advances past a newline, so a line the run is halfway through writing is
	// left alone and read again, complete, on the next tick.
	offset int64

	// malformed counts consecutive ticks on which result.json was there and
	// would not parse; see finished.
	malformed int

	// progress carries the last line's numbers forward. A trace line writes
	// null for a cost that is not a number, and a report that dropped the best
	// cost back to zero because this line could not name it would be a cost
	// curve that jumps rather than one number missing.
	progress optimizer.Progress
}

// traceLine is one line of trace.jsonl, in the fields a live snapshot needs.
//
// The costs are pointers because the file writes null for a value JSON cannot
// carry: an unmeasurable objective returns +Inf, and a run's very first line
// can have no best cost at all.
type traceLine struct {
	Iteration           int                `json:"iteration"`
	OptimizerIterations int                `json:"optimizer_iterations"`
	Restart             int                `json:"restart"`
	Lambda              int                `json:"lambda"`
	Evaluations         int                `json:"evaluations"`
	ElapsedMS           int64              `json:"elapsed_ms"`
	Current             *float64           `json:"current"`
	Best                *float64           `json:"best"`
	Terms               *optimizer.Metrics `json:"terms"`
}

// readTrace consumes every whole line trace.jsonl has gained since the last
// tick and reports each one into the job.
//
// Only whole lines are consumed. A trace is flushed per line, but a flush is
// still a write of several hundred bytes and a reader that arrives in the
// middle of one would find a fragment that is not JSON; worse, consuming it
// would put the offset inside a line and every line after it would be
// garbage. So the read stops at the last newline in what was gained, and the
// bytes past it are read again next time.
func (f *followedRun) readTrace() {
	path := filepath.Join(f.job.dir, fitrun.FileTrace)

	file, err := os.Open(path)
	if err != nil {
		return
	}

	defer func() {
		_ = file.Close()
	}()

	// A file shorter than the offset is not the file that was being read: a
	// run restarted into the same directory truncates its trace. Starting over
	// re-reports lines the job has already seen, which costs nothing -- a
	// report is a sample, not an increment.
	if info, statErr := file.Stat(); statErr == nil && info.Size() < f.offset {
		f.offset = 0
	}

	if _, err := file.Seek(f.offset, io.SeekStart); err != nil {
		return
	}

	gained, err := io.ReadAll(file)
	if err != nil {
		return
	}

	end := bytes.LastIndexByte(gained, '\n')
	if end < 0 {
		return
	}

	complete := gained[:end+1]
	f.offset += int64(len(complete))

	for _, line := range bytes.Split(complete, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		f.report(line)
	}
}

// report turns one trace line into a progress report on the job.
func (f *followedRun) report(line []byte) {
	var recorded traceLine

	// A line that does not parse is skipped rather than fatal. It is a file
	// another process is writing, and one bad line is not a reason to stop
	// following the run that wrote the good ones.
	if err := json.Unmarshal(line, &recorded); err != nil {
		return
	}

	f.progress.Iteration = recorded.Iteration
	f.progress.OptimizerIterations = recorded.OptimizerIterations
	f.progress.Restart = recorded.Restart
	f.progress.Lambda = recorded.Lambda
	f.progress.Evaluations = recorded.Evaluations
	f.progress.Elapsed = time.Duration(recorded.ElapsedMS) * time.Millisecond

	if recorded.Current != nil {
		f.progress.CurrentCost = *recorded.Current
	}

	if recorded.Best != nil {
		f.progress.BestCost = *recorded.Best
	}

	f.job.report(f.progress, recorded.Terms)
}

// finished reports whether the run has written its result, and moves the job
// to its terminal state when it has.
//
// result.json is what ends a followed run, exactly as it is what
// internal/campaign/status.go reads a campaign's directories by. A run whose
// trace has stopped growing is still running as far as this server is
// concerned: nothing on disk says otherwise, and guessing from a quiet trace
// is the guess the freshness window used to make.
func (f *followedRun) finished() bool {
	summary, err := readRestoredSummary(f.job.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}

	failure := ""

	if err != nil {
		// fitrun writes result.json with a plain os.WriteFile, so a scan that
		// lands between the create and the write finds a file that exists and
		// does not parse. That is a moment, not a state: it is read again on
		// the next tick, and only a file that has failed to parse for several
		// ticks running is taken at face value as a record that cannot be
		// read.
		f.malformed++
		if f.malformed < followedResultAttempts {
			return false
		}

		failure = fmt.Sprintf(interruptedFailure, err)
	}

	config, configErr := readRestoredConfig(f.job.dir)
	if configErr != nil {
		config = &restoredConfig{Started: f.job.startedAt}
	}

	f.job.mu.Lock()

	if failure != "" {
		// The run ended -- result.json is there -- but its record cannot be
		// read, so what it found is unknown. That is a failure rather than a
		// success with missing fields.
		f.job.state = fitFailed
		f.job.stopReason = stopReasonInterrupted
		f.job.failure = failure
	} else {
		applySummaryLocked(f.job, summary)
	}

	f.job.finishedAt = restoredFinish(f.job.dir, config)
	f.job.presetOnDisk = fileExists(filepath.Join(f.job.dir, fitrun.FilePreset))
	f.job.notifyLocked()
	f.job.mu.Unlock()

	// Everything waiting on the job -- the SSE stream chief among them -- is
	// released here, which is what makes the browser see the run end rather
	// than merely stop moving.
	close(f.job.done)

	return true
}

// restoreJob rebuilds one run directory as a job, returning nil when the
// directory is not one. The second return is non-nil when the run is still
// being written and this server should follow it.
//
// A directory is a run exactly when it holds a config.json, which fitrun
// writes before the search starts. Whether the run finished is then decided by
// result.json, which fitrun writes after it: that is the rule
// internal/campaign/status.go already reads a campaign's directories by, and
// it has to read the same way here because these are the same directories.
func restoreJob(dir, id string) (*fitJob, *followedRun) {
	config, err := readRestoredConfig(dir)
	if err != nil {
		return nil, nil
	}

	job := &fitJob{
		id:               id,
		dir:              dir,
		request:          restoredRequest(config),
		bounds:           restoredBounds(config),
		startedAt:        config.Started,
		sampleRate:       config.SampleRate,
		referenceSeconds: config.Reference.Seconds,
		// A job read off disk has no goroutine to stop, so its cancel is a
		// no-op rather than nil: everything that stops a job calls it
		// unconditionally and a nil here would be a panic on shutdown.
		cancel:       func() {},
		done:         make(chan struct{}),
		subscribers:  make(map[chan struct{}]struct{}),
		resolved:     config.Resolved,
		presetOnDisk: fileExists(filepath.Join(dir, fitrun.FilePreset)),
		// Nothing in this process started this run, whatever state it is in,
		// and the stop control must not pretend otherwise.
		followed: true,
	}

	summary, err := readRestoredSummary(dir)

	if errors.Is(err, fs.ErrNotExist) {
		// No result.json: the run has not finished. It is restored as running
		// and followed, and the trace it has written so far is read at once so
		// that a run picked up halfway through arrives with its cost curve
		// rather than at zero.
		//
		// The state is set without the lock, and it has to be: readTrace
		// reports through the job's own report, which takes that lock. Nothing
		// else can see this job yet either way -- it is returned to the caller
		// that will adopt it.
		job.state = fitRunning

		follow := &followedRun{job: job}
		follow.readTrace()

		return job, follow
	}

	// The lock is a formality here too, and it is taken anyway, because the
	// fields below are the ones a followed run writes while the browser is
	// reading them and both paths go through the same code to write them.
	job.mu.Lock()

	if err != nil {
		// result.json is there but unreadable, so the run ended and what it
		// found cannot be recovered.
		job.state = fitFailed
		job.stopReason = stopReasonInterrupted
		job.failure = fmt.Sprintf(interruptedFailure, err)
	} else {
		applySummaryLocked(job, summary)
	}

	job.finishedAt = restoredFinish(dir, config)
	job.mu.Unlock()

	// The job is terminal from the moment it exists, so everything that waits
	// on a job -- the cancel endpoint, the event stream -- is satisfied at once
	// rather than blocking on a goroutine that will never run.
	close(job.done)

	return job, nil
}

// applySummaryLocked fills a job read off disk in from what the run recorded.
//
// The caller holds the job's lock. For a directory restored as terminal that
// is a formality -- nothing else has a pointer to the job yet -- but a
// followed run is filled in while the browser is reading it, and the two go
// through the same function so that a restored job and a followed one that
// finishes cannot describe the same result differently.
func applySummaryLocked(job *fitJob, summary *fitrun.Summary) {
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
