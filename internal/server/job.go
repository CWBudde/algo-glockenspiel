package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

// fitState names where a job is. The strings are part of the HTTP API, so they
// are spelled once here and never formatted at a call site.
type fitState string

const (
	// fitQueued is a job that has been accepted but not begun. The server
	// still runs exactly one search at a time -- fitting is CPU-bound and
	// evaluates candidates on every core, so a second concurrent fit would not
	// do twice the work, it would do the same work half as fast in each -- but
	// a start request no longer has to be refused for it. It waits.
	fitQueued    fitState = "queued"
	fitRunning   fitState = "running"
	fitSucceeded fitState = "succeeded"
	fitFailed    fitState = "failed"
	fitCanceled  fitState = "canceled"
)

// terminal reports whether a state is one a job never leaves.
func (s fitState) terminal() bool {
	return s != fitRunning && s != fitQueued
}

// stopReasonQueuedCancel is what a job that was cancelled before it ever ran
// records. It is a stop reason of this package's own because no backend
// produced it: nothing was started, so no backend had a chance to say why it
// stopped.
const stopReasonQueuedCancel = "canceled_while_queued"

// errNoFit is returned by the read endpoints when nothing has been started yet.
var errNoFit = errors.New("no fit has been started")

// errServerStopped is what start refuses a request with once stopAll has run.
// stopAll drains the queue and cancels whatever is running exactly once; a fit
// that slipped past that drain and got recorded afterwards would never have
// cancel or cancelQueued applied to it at all.
var errServerStopped = errors.New("the server is shutting down")

// fitJob is one run: its settings, its live progress, and its outcome.
//
// Everything mutable sits behind mu, because the optimizer's Report callback
// runs on whichever goroutine the backend evaluates on while HTTP handlers read
// the same fields from their own. Callers outside this file only ever see a
// fitSnapshot, which is a value copy taken under the lock; nothing hands out a
// pointer into the mutable state.
type fitJob struct {
	id         string
	request    fitRequest
	startedAt  time.Time
	sampleRate int

	// dir is the run directory this job writes, named exactly as the job is.
	// It is created before the job starts, so everything the run produces --
	// the uploaded reference, the trace, the fitted preset -- has somewhere to
	// go from the first line.
	dir string

	// referenceSeconds is the length of the uploaded reference, which is also
	// the default render length for the audio endpoint: a fitted preset is only
	// meaningfully comparable over the span it was fitted on.
	referenceSeconds float64

	// cancel ends the run. It is the context the optimizer already takes, so
	// cancellation needs no new plumbing inside internal/optimizer.
	cancel context.CancelFunc

	// done is closed exactly once, by finish, after the state has been moved to
	// a terminal one. Waiting on it is how the cancel endpoint can promise that
	// the slot is free by the time it answers.
	done chan struct{}

	// bounds is the search box the client uploaded, or nil for fitrun's own.
	// It is kept only to echo it back: the provenance of a fit includes the
	// box it was allowed to search, and a box that only lived in the request
	// that started the job could never be read again.
	bounds *optimizer.ParamBounds

	// presetOnDisk says that preset.json is in the run directory even though
	// no result is held in memory. It is how a job rebuilt at startup can
	// answer the preset and audio endpoints: the fitted preset outlived the
	// process that found it, and the file is read when it is asked for rather
	// than held for every job in the history.
	presetOnDisk bool

	mu          sync.Mutex
	state       fitState
	progress    optimizer.Progress
	metrics     *optimizer.Metrics
	pinned      []optimizer.PinnedDimension
	seededModes int
	stopReason  string
	failure     string

	// converged is the backend's own verdict, from optimizer.Result: the run
	// stopped because a convergence criterion fired rather than because it
	// ran out of budget. It is a different question from the state, which
	// only says whether the run finished at all.
	converged bool

	finishedAt  time.Time
	result      *preset.Preset
	subscribers map[chan struct{}]struct{}

	// summary is what the run recorded in result.json, once it wrote one. It
	// is what the job listing reports as the run's score: a live job's best
	// cost still moves, while this is the number the run shipped.
	summary *fitrun.Summary

	// resolved is what the backend settled on once every "choose one for me"
	// input had been resolved. It is recorded from fitrun's OnResolve
	// callback, which runs on the fit goroutine, so it lives behind mu like
	// every other mutable field.
	resolved fitrun.Resolved
}

// fitSnapshot is the wire form of a job's state, and the only thing that leaves
// the lock. Both the status endpoint and every SSE event are one of these, so a
// client that reconnects mid-run reads the same shape it was streaming.
type fitSnapshot struct {
	JobID string   `json:"jobId"`
	State fitState `json:"state"`

	// Iteration counts progress reports rather than optimizer iterations; see
	// optimizer.Progress. OptimizerIterations is the backend's own count and is
	// the one comparable with MaxIterations.
	Iteration           int `json:"iteration"`
	OptimizerIterations int `json:"optimizerIterations"`
	Evaluations         int `json:"evaluations"`

	// Restart is the zero-based index of the search in progress: CMA-ES stops
	// a run that has converged and starts another from a wider sigma, and
	// this counts them. It is omitted for every other backend, which never
	// moves it off zero.
	//
	// Epoch is the mayfly backend's round index, which is the same field of
	// optimizer.Progress read under the other backend's meaning: a mayfly
	// round is a fresh population, not a restart of a converged search. The
	// two are separate wire fields because a reader must not have to know
	// which backend ran to know what the number counts, and exactly one of
	// them is ever present.
	Restart int `json:"restart,omitempty"`
	Epoch   int `json:"epoch,omitempty"`

	// EvaluationsPerSecond is evaluations over elapsed wall time, which is
	// the throughput a client compares two backends by. Zero before the clock
	// has moved.
	EvaluationsPerSecond float64 `json:"evaluationsPerSecond"`

	// BudgetFraction is how much of the tightest binding budget the run has
	// spent: the largest of iterations over the iteration cap and elapsed
	// over the time budget, over the budgets that are actually set, and zero
	// when none is.
	//
	// It is not an ETA. A run stops at the first budget that binds, so this
	// is a lower bound on how far along the run is and nothing more: a search
	// that converges, or one whose backend stops itself, ends well below one.
	BudgetFraction float64 `json:"budgetFraction"`

	// Converged is the backend's own verdict: the run stopped on a
	// convergence criterion rather than on its budget. False while a run is
	// still going, and false for one that was cancelled.
	Converged bool `json:"converged"`

	CurrentCost float64 `json:"currentCost"`
	BestCost    float64 `json:"bestCost"`
	ElapsedMS   int64   `json:"elapsedMs"`

	StopReason string `json:"stopReason,omitempty"`
	Error      string `json:"error,omitempty"`

	SampleRate       int     `json:"sampleRate"`
	ReferenceSeconds float64 `json:"referenceSeconds"`
	Note             int     `json:"note"`
	Velocity         int     `json:"velocity"`
	Optimizer        string  `json:"optimizer"`
	Metric           string  `json:"metric"`

	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`

	// HasPreset says whether /api/fit/preset and /api/fit/audio will answer.
	// It is not simply state == succeeded: a run cancelled after its first
	// report still leaves the best parameters found so far.
	HasPreset bool `json:"hasPreset"`

	// MayflyVariant is the dialect the run actually uses, which is not always
	// the one that was asked for: a preset selects one of its own. Without this
	// echo a client that named a preset could never learn what ran.
	MayflyVariant string `json:"mayflyVariant,omitempty"`

	// MayflySeed is a string because it is an int64 and this is JSON: a seed
	// past 2^53 loses its low bits on the way through a JavaScript Number, and
	// a seed that cannot be sent back verbatim cannot reproduce its run. The
	// request side spells it the same way for the same reason.
	MayflySeed string `json:"mayflySeed,omitempty"`

	// Metrics is the breakdown of the best point so far: every term of the
	// composite objective, whatever metric the run scores by. Absent until
	// the first report.
	Metrics *optimizer.Metrics `json:"metrics,omitempty"`

	// SeededModes is how many of the starting modes came from the reference's
	// partials; zero means the starting preset's own modes were kept.
	SeededModes int `json:"seededModes"`

	// Pinned lists the dimensions of the result that sit on a bound of the
	// search box, once there is a result. A pinned dimension is one the
	// search wanted to push past the box.
	Pinned []optimizer.PinnedDimension `json:"pinned,omitempty"`

	// Request is the whole request as it was resolved, defaults included. It
	// is the provenance a results view reads: two fits differing in one
	// setting are told apart by it, and a fit whose settings are not in the
	// snapshot can only be described by whatever the client happens to
	// remember sending.
	Request fitRequestEcho `json:"request"`

	// Profile is how the run's metric weighs the terms Metrics reports.
	// Absent for the single-term legacy metrics, which have no profile.
	//
	// The weights and norms are sent rather than left for the client to
	// carry, because a per-term display built from a second copy of
	// optimizer.DefaultNorms disagrees with the score it sits beside the
	// moment a norm here changes.
	Profile *fitProfile `json:"profile,omitempty"`
}

// fitRequestEcho is every setting a fit ran under, including the ones the
// client never sent and the ones the backend resolved for itself.
//
// It is a struct of its own rather than the fitRequest itself, because two of
// its fields have to be spelled differently on the wire: the seeds are int64
// and a JavaScript Number loses the low bits of one past 2^53, so they are
// decimal strings here, exactly as the snapshot's MayflySeed is. Neither seed
// is omitted when it is the backend's default, unlike the rest of the mayfly
// and CMA-ES block: a formatted int64 is never the empty string, so an
// omitempty on either would be a tag that can never fire and a client type
// that says optional about a field that is always there. The uploaded
// documents are not echoed as documents either: the bounds are, because they
// are six ranges and they are the constraint the result has to be read
// against, while the mayfly tuning is reported only as present.
//
// A job rebuilt from a run directory fills in what config.json recorded,
// which is most of this but not the mayfly form fields: those were folded
// into the tuning document before the run wrote anything, so a restored job
// echoes them at their zero values.
type fitRequestEcho struct {
	Note          int    `json:"note"`
	Velocity      int    `json:"velocity"`
	Modes         int    `json:"modes"`
	Optimizer     string `json:"optimizer"`
	Metric        string `json:"metric"`
	MaxIterations int    `json:"maxIterations"`
	TimeBudgetMS  int64  `json:"timeBudgetMs"`
	ReportEvery   int    `json:"reportEvery"`
	Align         bool   `json:"align"`
	NormalizeGain bool   `json:"normalizeGain"`
	Downmix       string `json:"downmix"`
	WindowMS      int64  `json:"windowMs"`

	MayflyVariant    string `json:"mayflyVariant,omitempty"`
	MayflyPreset     string `json:"mayflyPreset,omitempty"`
	MayflyPopulation int    `json:"mayflyPopulation,omitempty"`
	MayflySeed       string `json:"mayflySeed"`
	MayflyEpochs     int    `json:"mayflyEpochs,omitempty"`
	MayflyRestarts   int    `json:"mayflyRestarts,omitempty"`
	MayflyStagnation int    `json:"mayflyStagnation,omitempty"`
	MayflySelection  string `json:"mayflySelection,omitempty"`

	MayflyTargetCost *float64 `json:"mayflyTargetCost,omitempty"`
	MayflyNC         *int     `json:"mayflyNc,omitempty"`
	MayflyNCRatio    *float64 `json:"mayflyNcRatio,omitempty"`

	CmaesCovariance string  `json:"cmaesCovariance,omitempty"`
	CmaesLambda     int     `json:"cmaesLambda,omitempty"`
	CmaesSigma      float64 `json:"cmaesSigma,omitempty"`
	CmaesSeed       string  `json:"cmaesSeed"`
	CmaesRestarts   int     `json:"cmaesRestarts,omitempty"`

	// MayflyTuning says a tuning document was uploaded. The document itself
	// is not echoed: it is a whole configuration file, it is the client's own
	// upload, and the run directory's config.json already records it.
	MayflyTuning bool `json:"mayflyTuning"`

	// Seed and Workers are what the backend resolved: the seed it actually
	// drew, which is what makes a run repeatable, and the worker count it
	// sized to the machine. Workers is zero until the backend has resolved
	// itself, which is one report before the first progress line.
	Seed    string `json:"seed"`
	Workers int    `json:"workers"`

	// Bounds is the box the client uploaded, absent when the run used the
	// default one. The default is not echoed in its place because it is not a
	// constant: it is drawn from the reference's own measured fundamental,
	// so writing it here would state a box nobody asked for as though they
	// had.
	Bounds *fitBoundsEcho `json:"bounds,omitempty"`
}

// fitBoundsEcho is optimizer.ParamBounds on the wire, each dimension a
// [min, max] pair, which is the shape the uploaded bounds document itself
// uses.
type fitBoundsEcho struct {
	InputMix     [2]float64 `json:"inputMix"`
	FilterFreq   [2]float64 `json:"filterFreq"`
	Amplitude    [2]float64 `json:"amplitude"`
	Frequency    [2]float64 `json:"frequency"`
	DecayMs      [2]float64 `json:"decayMs"`
	HarmonicGain [2]float64 `json:"harmonicGain"`
}

// fitProfile is the active metric's profile: the weight and the norm of every
// term it scores by, in optimizer.Terms' reporting order.
type fitProfile struct {
	Name  string           `json:"name"`
	Terms []fitProfileTerm `json:"terms"`
}

// fitProfileTerm is one weighted term. Norm is the value at which the term
// scores one half, so a display can show a raw metric against the scale the
// score judged it on.
type fitProfileTerm struct {
	Term   string  `json:"term"`
	Weight float64 `json:"weight"`
	Norm   float64 `json:"norm"`
	Unit   string  `json:"unit,omitempty"`
}

func newFitJob(id, dir string, request fitRequest, sampleRate int, referenceSeconds float64, cancel context.CancelFunc) *fitJob {
	return &fitJob{
		id:               id,
		dir:              dir,
		request:          request,
		startedAt:        time.Now(),
		sampleRate:       sampleRate,
		referenceSeconds: referenceSeconds,
		cancel:           cancel,
		done:             make(chan struct{}),
		state:            fitQueued,
		subscribers:      make(map[chan struct{}]struct{}),
	}
}

// markRunning moves a queued job to running, which is the worker's first act
// once it picks the job up.
//
// The clock is restarted here rather than kept from the moment the request was
// accepted, because elapsed is what the search has spent. Time a job spent
// waiting behind another one is not the search's, and reporting it as such
// would make the first report of a queued job look like a stall.
//
// It is called twice for a job that never actually waited: once by start,
// which knows the worker will take it immediately, and once by the worker. The
// guard makes the second call a no-op, so such a job is running from the
// moment its start request is answered and a client that starts one fit never
// sees a queued state at all.
func (j *fitJob) markRunning() {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.state != fitQueued {
		return
	}

	j.state = fitRunning
	j.startedAt = time.Now()
	j.notifyLocked()
}

// snapshot copies the job's state out from under the lock.
func (j *fitJob) snapshot() fitSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()

	snapshot := fitSnapshot{
		JobID:               j.id,
		State:               j.state,
		Iteration:           j.progress.Iteration,
		OptimizerIterations: j.progress.OptimizerIterations,
		Evaluations:         j.progress.Evaluations,
		CurrentCost:         j.progress.CurrentCost,
		BestCost:            j.progress.BestCost,
		ElapsedMS:           j.progress.Elapsed.Milliseconds(),
		StopReason:          j.stopReason,
		Error:               j.failure,
		SampleRate:          j.sampleRate,
		ReferenceSeconds:    j.referenceSeconds,
		Note:                j.request.Note,
		Velocity:            j.request.Velocity,
		Optimizer:           j.request.Optimizer,
		Metric:              j.request.Metric,
		StartedAt:           j.startedAt,
		HasPreset:           j.result != nil || j.presetOnDisk,
		SeededModes:         j.seededModes,
		Converged:           j.converged,
		Request:             j.requestEchoLocked(),
		Profile:             profileEchoFor(j.request.Metric),
	}

	// One field of optimizer.Progress, two meanings, so the snapshot spells
	// which one this backend meant rather than leaving the reader to guess.
	if j.request.Optimizer == mayflyOptimizerName {
		snapshot.Epoch = j.progress.Restart
	} else {
		snapshot.Restart = j.progress.Restart
	}

	if j.metrics != nil {
		metrics := *j.metrics
		snapshot.Metrics = &metrics
	}

	if len(j.pinned) > 0 {
		snapshot.Pinned = append([]optimizer.PinnedDimension(nil), j.pinned...)
	}

	if resolved := j.resolved; resolved.Variant != "" {
		snapshot.MayflyVariant = resolved.Variant
		snapshot.MayflySeed = strconv.FormatInt(resolved.Seed, 10)
	}

	if !j.finishedAt.IsZero() {
		finished := j.finishedAt
		snapshot.FinishedAt = &finished
	}

	// While the run is live the optimizer's own Elapsed only advances at a
	// report, so a long gap between reports would look like a stalled clock in
	// the browser. Wall time is what the caller is actually asking about. A
	// queued job has spent nothing: its clock starts at markRunning.
	switch j.state {
	case fitRunning:
		snapshot.ElapsedMS = time.Since(j.startedAt).Milliseconds()
	case fitQueued:
		snapshot.ElapsedMS = 0
	case fitSucceeded, fitFailed, fitCanceled:
	}

	// Both are derived from the elapsed time the snapshot reports rather than
	// from the optimizer's own, so a live job's throughput falls while it is
	// between reports instead of standing still at whatever the last report
	// measured.
	elapsed := time.Duration(snapshot.ElapsedMS) * time.Millisecond
	snapshot.EvaluationsPerSecond = evaluationsPerSecond(snapshot.Evaluations, elapsed)
	snapshot.BudgetFraction = budgetFraction(snapshot.OptimizerIterations, elapsed,
		j.request.MaxIterations, j.request.timeBudget())

	return snapshot
}

// evaluationsPerSecond is throughput, or zero before the clock has moved.
func evaluationsPerSecond(evaluations int, elapsed time.Duration) float64 {
	if evaluations <= 0 || elapsed <= 0 {
		return 0
	}

	return float64(evaluations) / elapsed.Seconds()
}

// budgetFraction is how much of the tightest binding budget a run has spent.
//
// The largest fraction is the answer because a run stops at the first budget
// that binds: a fit with an hour and a hundred iterations that has done fifty
// of them in a minute is half done, not a sixtieth. Budgets that are not set
// contribute nothing, so a run with neither reports zero rather than a
// fraction of a budget it does not have.
func budgetFraction(iterations int, elapsed time.Duration, maxIterations int, budget time.Duration) float64 {
	fraction := 0.0

	if maxIterations > 0 {
		fraction = math.Max(fraction, float64(iterations)/float64(maxIterations))
	}

	if budget > 0 {
		fraction = math.Max(fraction, elapsed.Seconds()/budget.Seconds())
	}

	// A backend that overshoots its cap by an iteration, or a wall clock read
	// a millisecond after the budget expired, would otherwise report a run
	// that is more than finished.
	return math.Min(fraction, 1)
}

// requestEchoLocked describes the settings this job ran under. Caller holds
// the job's lock: it reads the resolved values, which the fit goroutine
// writes.
func (j *fitJob) requestEchoLocked() fitRequestEcho {
	settings := j.request

	echo := fitRequestEcho{
		Note:             settings.Note,
		Velocity:         settings.Velocity,
		Modes:            settings.Modes,
		Optimizer:        settings.Optimizer,
		Metric:           settings.Metric,
		MaxIterations:    settings.MaxIterations,
		TimeBudgetMS:     settings.TimeBudgetMS,
		ReportEvery:      settings.ReportEvery,
		Align:            settings.Align,
		NormalizeGain:    settings.NormalizeGain,
		Downmix:          settings.Downmix,
		WindowMS:         settings.WindowMS,
		MayflyVariant:    settings.MayflyVariant,
		MayflyPreset:     settings.MayflyPreset,
		MayflyPopulation: settings.MayflyPopulation,
		MayflySeed:       strconv.FormatInt(settings.MayflySeed, 10),
		MayflyEpochs:     settings.MayflyEpochs,
		MayflyRestarts:   settings.MayflyRestarts,
		MayflyStagnation: settings.MayflyStagnation,
		MayflySelection:  settings.MayflySelection,
		MayflyTargetCost: settings.MayflyTargetCost,
		MayflyNC:         settings.MayflyNC,
		MayflyNCRatio:    settings.MayflyNCRatio,
		CmaesCovariance:  settings.CmaesCovariance,
		CmaesLambda:      settings.CmaesLambda,
		CmaesSigma:       settings.CmaesSigma,
		CmaesSeed:        strconv.FormatInt(settings.CmaesSeed, 10),
		CmaesRestarts:    settings.CmaesRestarts,
		MayflyTuning:     settings.MayflyTuning != nil,
		Seed:             strconv.FormatInt(seedFor(settings), 10),
		Workers:          j.resolved.Workers,
		Bounds:           boundsEchoFor(j.bounds),
	}

	// Once the backend has resolved itself the drawn seed is the one that
	// matters: a request that asked for a seed of zero was asking the backend
	// to pick, and echoing the zero back would describe a run nobody could
	// repeat. Workers is what says the resolution has happened, because a
	// resolved run always has at least one worker while a drawn seed may be
	// any number at all.
	if j.resolved.Workers > 0 {
		echo.Seed = strconv.FormatInt(j.resolved.Seed, 10)
	}

	return echo
}

// boundsEchoFor renders an uploaded search box, or nothing when the run used
// the default one.
func boundsEchoFor(bounds *optimizer.ParamBounds) *fitBoundsEcho {
	if bounds == nil {
		return nil
	}

	return &fitBoundsEcho{
		InputMix:     [2]float64{bounds.InputMix.Min, bounds.InputMix.Max},
		FilterFreq:   [2]float64{bounds.FilterFreq.Min, bounds.FilterFreq.Max},
		Amplitude:    [2]float64{bounds.Amplitude.Min, bounds.Amplitude.Max},
		Frequency:    [2]float64{bounds.Frequency.Min, bounds.Frequency.Max},
		DecayMs:      [2]float64{bounds.DecayMs.Min, bounds.DecayMs.Max},
		HarmonicGain: [2]float64{bounds.HarmonicGain.Min, bounds.HarmonicGain.Max},
	}
}

// profileEchoFor describes how a metric weighs the terms, or reports nothing
// for a single-term legacy metric, which has no profile to describe.
//
// Only the weighted terms are listed. A term of weight zero is not part of
// the score at all, and listing it with a norm would invite a display to
// draw a bar the score never counted.
func profileEchoFor(metric string) *fitProfile {
	profile, ok := optimizer.ProfileFor(optimizer.Metric(metric))
	if !ok {
		return nil
	}

	echo := &fitProfile{Name: profile.Name}

	for _, term := range optimizer.Terms() {
		weight := profile.Weights[term]
		if weight <= 0 {
			continue
		}

		echo.Terms = append(echo.Terms, fitProfileTerm{
			Term:   string(term),
			Weight: weight,
			Norm:   profile.Norm(term),
			Unit:   term.Unit(),
		})
	}

	return echo
}

// recordResolved stores what the backend resolved for itself. It is passed as
// fitrun.Spec.OnResolve and runs once, before the search starts, on the
// goroutine that owns the run.
func (j *fitJob) recordResolved(resolved fitrun.Resolved) {
	j.mu.Lock()
	j.resolved = resolved
	j.notifyLocked()
	j.mu.Unlock()
}

// report records one optimizer progress callback and wakes every subscriber.
//
// This is the whole of the SSE plumbing on the optimizer's side: it is passed
// as OptimizeOptions.Report, the same hook the CLI uses for checkpointing, so
// internal/optimizer needed no change at all for streaming to exist.
func (j *fitJob) report(progress optimizer.Progress, metrics *optimizer.Metrics) {
	j.mu.Lock()
	// BestParams is a slice the backend keeps mutating after the callback
	// returns, and it is the only reference-typed field in Progress. Copying it
	// is what keeps a snapshot taken later from describing a vector the
	// optimizer has since moved.
	progress.BestParams = append([]float64(nil), progress.BestParams...)
	j.progress = progress

	if metrics != nil {
		j.metrics = metrics
	}

	j.notifyLocked()
	j.mu.Unlock()
}

// finish moves the job to a terminal state and releases everyone waiting on it.
// It is called exactly once, by the goroutine running the fit, as the last
// thing that goroutine does -- which is what makes "done is closed" mean "the
// job slot is free" rather than "the job asked to stop".
func (j *fitJob) finish(state fitState, result *preset.Preset, final *optimizer.Result, metrics *optimizer.Metrics, pinned []optimizer.PinnedDimension, cause error) {
	j.mu.Lock()

	j.state = state
	j.result = result
	j.pinned = pinned
	j.finishedAt = time.Now()

	if metrics != nil {
		j.metrics = metrics
	}

	// The periodic Report callback is the only other thing that advances
	// j.progress, and reportEvery is allowed to be zero, so a terminal snapshot
	// that did not fold the backend's own Result in would state the numbers of
	// the last report -- or, with no report at all, a finished run that claims
	// zero evaluations and a best cost of zero.
	if final != nil {
		j.stopReason = final.StopReason
		j.converged = final.Converged
		j.progress.OptimizerIterations = final.Iterations
		j.progress.Evaluations = final.Evaluations
		j.progress.BestCost = final.BestCost
		// A finished run has no candidate under evaluation any more, so its
		// current position is the best one it found.
		j.progress.CurrentCost = final.BestCost
		j.progress.Elapsed = final.Elapsed
		j.progress.BestParams = append([]float64(nil), final.BestParams...)
	}

	if cause != nil {
		j.failure = cause.Error()
	}

	j.notifyLocked()
	j.mu.Unlock()

	close(j.done)
}

// notifyLocked pokes every subscriber. The channels carry no payload and hold
// one slot, so a subscriber that has not caught up simply finds its wake-up
// already pending: progress is a sampled quantity and the newest snapshot is
// the only one worth delivering. Nothing is ever dropped that matters, because
// a subscriber also selects on done and reads a final snapshot from there.
func (j *fitJob) notifyLocked() {
	for wake := range j.subscribers {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// subscribe registers a wake-up channel and returns it together with its
// release. The channel is buffered so notifyLocked never blocks the optimizer's
// reporting goroutine on a slow reader.
func (j *fitJob) subscribe() (<-chan struct{}, func()) {
	wake := make(chan struct{}, 1)

	j.mu.Lock()
	j.subscribers[wake] = struct{}{}
	j.mu.Unlock()

	return wake, func() {
		j.mu.Lock()
		delete(j.subscribers, wake)
		j.mu.Unlock()
	}
}

// presetCopy returns the best preset found so far, or nil. It is a deep copy:
// BarParams carries Modes as a slice, so handing out the stored pointer would
// let a renderer's mutation reach the next reader of the same job.
func (j *fitJob) presetCopy() *preset.Preset {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.result.Clone()
}

// fittedPreset returns the preset this job produced, from memory for a job
// this process ran and from the run directory for one rebuilt at startup.
//
// The two are the same document: runFit's result is what fitrun had already
// written to preset.json, so a client cannot tell which half answered it.
func (j *fitJob) fittedPreset() (*preset.Preset, error) {
	if fitted := j.presetCopy(); fitted != nil {
		return fitted, nil
	}

	if !j.presetOnDisk {
		return nil, fmt.Errorf("fit %s has produced no preset yet", j.id)
	}

	fitted, err := preset.Load(filepath.Join(j.dir, fitrun.FilePreset))
	if err != nil {
		return nil, fmt.Errorf("the preset of fit %s could not be read: %w", j.id, err)
	}

	return fitted, nil
}

// recordSummary keeps what the run wrote to result.json. It runs on the fit
// goroutine, just before finish, so it lives behind mu like everything else
// the snapshot and the listing read.
func (j *fitJob) recordSummary(summary fitrun.Summary) {
	j.mu.Lock()
	j.summary = &summary
	j.mu.Unlock()
}

// cancelQueued ends a job that never left the queue. It is finish with a stop
// reason set first, because finish only records one when a backend produced a
// result and this job had no backend at all.
//
// It also releases the job's own CancelFunc, which a queued job otherwise
// never would: runOne is what calls it for a job the worker picks up, but a
// job dropped from the queue is never handed to runOne, so nothing else ever
// releases the context start built for it.
func (j *fitJob) cancelQueued() {
	defer j.cancel()

	j.mu.Lock()
	j.stopReason = stopReasonQueuedCancel
	j.mu.Unlock()

	j.finish(fitCanceled, nil, nil, nil, nil, nil)
}

// running reports whether the goroutine that owns this job is still working.
func (j *fitJob) running() bool {
	select {
	case <-j.done:
		return false
	default:
		return true
	}
}

// maxStoredJobs caps the in-memory history.
//
// Every run directory stays on disk for good: it is the record the campaign
// tooling reads, and deleting it is the user's business, not the server's.
// Only the list a running process holds is bounded, so a server left open for
// a week does not grow without limit. A job past the cap is still on disk and
// still comes back if the server is restarted over the same work directory.
const maxStoredJobs = 200

// jobManager owns the job history and the queue that feeds the one fit slot.
//
// There is still exactly one search at a time. What changed is what a second
// start request means: it used to be refused, and it is now a job waiting its
// turn, so a client can line up a handful of fits and walk away.
//
// Everything below is guarded by mu. mu is held across calls that take a
// job's own lock -- start's call to markRunning chief among them -- but the
// two are never taken the other way round: nothing that holds a fitJob's lock
// ever reaches back for mu. The order is therefore always jobManager then
// fitJob, which is what keeps that nesting deadlock-free, not the absence of
// nesting itself.
type jobManager struct {
	mu sync.Mutex

	// jobs is the history, oldest first, and byID is the same set keyed by id.
	// The two are written together and trimmed together.
	jobs []*fitJob
	byID map[string]*fitJob

	// queue holds the jobs waiting for the worker, oldest first. A job is in
	// it from the moment its start request is accepted until the worker takes
	// it or a cancel drops it, and in exactly one of those ways: both happen
	// under mu, so a job is either run or dropped and never both.
	queue []*queuedFit

	// working says a worker goroutine is alive. It is set when the queue goes
	// from empty to non-empty and cleared by the worker itself when it finds
	// the queue empty, both under mu, so there is never a second worker and
	// never a queue with no worker to drain it.
	working bool

	// stopped says stopAll has already run. It is set under mu, in the same
	// critical section that drains the queue, and checked by start under the
	// same mu before a job is recorded and enqueued -- so a start that is
	// mid-setup when shutdown drains the queue still finds out, once it
	// reaches that section, that there is no worker left to run it and no
	// drain still to come that would otherwise cancel it for it.
	stopped bool

	counter uint64
}

// queuedFit is one job waiting its turn, together with the work it is waiting
// to do. The context is made when the job is accepted rather than when it
// starts, so cancelling a queued job has something to cancel from the first
// moment the client has a job id.
type queuedFit struct {
	job *fitJob
	ctx context.Context
	run func(ctx context.Context, job *fitJob)
}

// jobDetails is everything a job knows about itself before it runs: the
// request it came from, and what the uploaded reference turned out to be.
type jobDetails struct {
	settings         fitRequest
	sampleRate       int
	referenceSeconds float64
	seededModes      int

	// bounds is the box the client uploaded, or nil for the default one.
	bounds *optimizer.ParamBounds
}

// runDirTimeLayout stamps a run directory with a UTC instant that sorts
// chronologically as text and is safe as a path segment.
const runDirTimeLayout = "20060102T150405"

// runDirAttempts bounds the collision retry. A collision needs a directory
// from an earlier process in the same second under the same counter, so one
// retry would almost always do; the bound only keeps a directory that cannot
// be created for some other reason from looping.
const runDirAttempts = 32

// start accepts a fit and puts it at the back of the queue. It never refuses
// for want of a free slot; the job it returns is queued, and it becomes
// running when every job ahead of it has finished.
//
// The context handed to run is rooted in context.Background rather than in the
// HTTP request: the request that starts a fit returns immediately, so a
// request-scoped context would cancel the search the moment the client had its
// job id. Cancellation therefore has to be explicit -- through the cancel
// endpoint, or through the server's shutdown -- and both go through the
// CancelFunc stored on the job.
//
// setup runs synchronously, in the run directory, before the job is recorded
// and before anything is started. It is where the uploaded reference is
// written, so a disk that cannot take it is an answer to the start request
// rather than a job that fails a moment later, and a failed setup leaves
// neither a directory nor a job behind.
//
// mu is held only for the two things that actually need it: reserving the id
// (makeRunDir takes it internally, per attempt, to serialize the counter) and
// recording the finished job at the end. The Mkdir and setup happen with the
// lock released, because setup can write an entire uploaded WAV to disk, and
// holding mu across that would stall every other status read, cancel and
// queue pop for as long as the write takes. Nothing here assumed those steps
// were atomic with recording: the id is claimed by an exclusive Mkdir before
// mu is ever touched again, so no other start can claim the same directory
// while this one is still setting it up.
//
// That unlocked window is also why the final section checks m.stopped before
// it commits to anything: stopAll can run its entire drain while this start
// is off writing the upload, find the queue empty and the job not yet
// recorded, and drain nothing. Without the check this start would go on to
// record and enqueue a job that no drain will ever come back for, and start
// its search after the server has already declared itself stopped. The check
// runs in the same critical section as the record, so there is no gap after
// it for stopAll to slip through either.
func (m *jobManager) start(
	details jobDetails,
	workDir string,
	setup func(dir string) error,
	run func(ctx context.Context, job *fitJob),
) (*fitJob, error) {
	id, dir, err := m.makeRunDir(workDir)
	if err != nil {
		return nil, err
	}

	if setup != nil {
		if err := setup(dir); err != nil {
			_ = os.RemoveAll(dir)

			return nil, err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	job := newFitJob(id, dir, details.settings, details.sampleRate, details.referenceSeconds, cancel)
	job.seededModes = details.seededModes
	job.bounds = details.bounds

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stopped {
		// Nothing will ever run or cancel this job, so it must never exist:
		// recording it here would leave a "queued" or "running" entry in the
		// history that stopAll already believes it drained.
		cancel()

		_ = os.RemoveAll(dir)

		return nil, errServerStopped
	}

	m.recordLocked(job)
	m.queue = append(m.queue, &queuedFit{job: job, ctx: ctx, run: run})

	if !m.working {
		m.working = true

		// Nothing is ahead of this job, so it is running rather than waiting.
		// Saying so before the request is answered is what keeps a client that
		// starts one fit reading exactly what it read before there was a queue.
		job.markRunning()

		go m.work()
	}

	return job, nil
}

// work drains the queue one job at a time. It is the whole of the "one fit at
// a time" rule: there is at most one of these goroutines, so there is at most
// one search.
func (m *jobManager) work() {
	for {
		next := m.take()
		if next == nil {
			return
		}

		m.runOne(next)
	}
}

// take pops the next job, or retires the worker when there is nothing left.
// Clearing working under the same lock that pops is what keeps start from
// deciding "a worker is alive" about a worker that has just decided to stop.
func (m *jobManager) take() *queuedFit {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.queue) == 0 {
		m.working = false

		return nil
	}

	next := m.queue[0]
	m.queue[0] = nil
	m.queue = m.queue[1:]

	return next
}

// runOne runs one job to its end.
func (m *jobManager) runOne(next *queuedFit) {
	// The context leaks if the run returns without anyone cancelling, which is
	// the ordinary success path.
	defer next.job.cancel()

	next.job.markRunning()
	next.run(next.ctx, next.job)
}

// recordLocked adds a job to the history. Caller holds mu.
func (m *jobManager) recordLocked(job *fitJob) {
	if m.byID == nil {
		m.byID = make(map[string]*fitJob)
	}

	m.jobs = append(m.jobs, job)
	m.byID[job.id] = job

	// Only finished jobs are dropped, which is why this is a loop with a
	// condition rather than a slice expression: a queue longer than the cap
	// would otherwise let the history forget a job that is still going to
	// report into it.
	for len(m.jobs) > maxStoredJobs && !m.jobs[0].running() {
		delete(m.byID, m.jobs[0].id)

		m.jobs[0] = nil
		m.jobs = m.jobs[1:]
	}
}

// adopt records a job that was rebuilt from disk rather than started here.
// Restart recovery calls it once per run directory, oldest first.
func (m *jobManager) adopt(job *fitJob) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recordLocked(job)
}

// cancel stops one job, whether it is waiting or running.
//
// A job still in the queue is dropped and marked cancelled without ever
// running, which is the point: a client that queued five fits and changed its
// mind should not have to watch four of them run. A job the worker already
// holds stops through its context, exactly as it always did. The two cannot
// both happen, because dropping from the queue and popping from it are the
// same lock, so finish is still called exactly once per job.
func (m *jobManager) cancel(job *fitJob) {
	if m.drop(job) {
		job.cancelQueued()

		return
	}

	job.cancel()
}

// drop removes a job from the queue, reporting whether it was still there.
func (m *jobManager) drop(job *fitJob) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, waiting := range m.queue {
		if waiting.job != job {
			continue
		}

		// Shifting left leaves a duplicate of the last element at the old tail
		// index, which append would otherwise keep alive: through its run
		// closure, the objective, the decoded reference samples and the
		// template stay reachable until the slice grows past that index again.
		// Nil it, same as take does for the element it pops.
		last := len(m.queue) - 1
		copy(m.queue[i:], m.queue[i+1:])
		m.queue[last] = nil
		m.queue = m.queue[:last]

		return true
	}

	return false
}

// makeRunDir claims the next free run directory and returns its name, which is
// also the job id. The counter is the only shared state involved, so mu is
// taken just long enough to bump it once per attempt; the Mkdir that follows,
// like MkdirAll above it, runs unlocked so a slow filesystem cannot stall the
// rest of the manager.
//
// The directory is made with an exclusive Mkdir rather than MkdirAll: the name
// carries a timestamp only to the second and a counter that starts again at
// one after a restart, so "this name is free" has to be decided by the
// filesystem rather than by a check that another process could win the moment
// it passed.
func (m *jobManager) makeRunDir(workDir string) (string, string, error) {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return "", "", fmt.Errorf("the work directory %s could not be created: %w", workDir, err)
	}

	stamp := time.Now().UTC().Format(runDirTimeLayout)

	for range runDirAttempts {
		m.mu.Lock()
		m.counter++
		counter := m.counter
		m.mu.Unlock()

		id := fmt.Sprintf("fit-%s-%04d", stamp, counter)
		dir := filepath.Join(workDir, id)

		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return id, dir, nil
		}

		if !errors.Is(err, fs.ErrExist) {
			return "", "", fmt.Errorf("the run directory %s could not be created: %w", dir, err)
		}
	}

	return "", "", fmt.Errorf("no free run directory under %s after %d attempts", workDir, runDirAttempts)
}

// active returns the most recent job, queued, running or finished, or nil.
//
// The unnumbered endpoints answer from the most recent job on purpose: a fit
// that has just ended is exactly the one whose preset a client wants, and it
// is what /api/fit meant before there was a history to ask instead. "Most
// recent" is the last job accepted, not the one the worker happens to hold, so
// a client that queues a fit immediately sees the fit it queued.
func (m *jobManager) active() *fitJob {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.jobs) == 0 {
		return nil
	}

	return m.jobs[len(m.jobs)-1]
}

// lookup finds a job by id, or returns nil. The id is the run directory's
// name, so this is also what keeps a client's id away from the filesystem:
// only a job the server itself recorded has a directory to serve from.
func (m *jobManager) lookup(id string) *fitJob {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.byID[id]
}

// history copies the job list, newest first.
func (m *jobManager) history() []*fitJob {
	m.mu.Lock()
	defer m.mu.Unlock()

	listed := make([]*fitJob, 0, len(m.jobs))
	for i := len(m.jobs) - 1; i >= 0; i-- {
		listed = append(listed, m.jobs[i])
	}

	return listed
}

// stopAll ends every fit this manager owns: the queue is emptied and whatever
// is running is asked to stop. It is what the server calls on shutdown, so no
// fit outlives the process that owns it.
//
// The queue is taken under the lock and emptied in the same breath, so the
// worker cannot pick up a job between the two halves of this and start a
// search the caller has just asked to end. Setting stopped in that same
// breath is what closes the other half of that: a start already past the
// point where it would have been in this queue -- mid-setup, with nothing
// recorded yet -- checks stopped before it records itself, so it refuses
// rather than becoming a job this drain already believes does not exist.
func (m *jobManager) stopAll() {
	m.mu.Lock()

	m.stopped = true

	waiting := m.queue
	m.queue = nil

	live := make([]*fitJob, 0, len(m.jobs))

	for _, job := range m.jobs {
		if job.running() {
			live = append(live, job)
		}
	}

	m.mu.Unlock()

	for _, entry := range waiting {
		entry.job.cancelQueued()
	}

	// Cancelling a job that has already finished, or one just marked cancelled
	// above, is a no-op: the CancelFunc is idempotent and a finished run is
	// not reading its context any more.
	for _, job := range live {
		job.cancel()
	}
}
