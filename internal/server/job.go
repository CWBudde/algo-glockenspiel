package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cwbudde/glockenspiel/internal/optimizer"
	"github.com/cwbudde/glockenspiel/internal/preset"
)

// fitState names where a job is. The strings are part of the HTTP API, so they
// are spelled once here and never formatted at a call site.
type fitState string

const (
	fitRunning   fitState = "running"
	fitSucceeded fitState = "succeeded"
	fitFailed    fitState = "failed"
	fitCanceled  fitState = "canceled"
)

// terminal reports whether a state is one a job never leaves.
func (s fitState) terminal() bool {
	return s != fitRunning
}

// errFitInProgress is returned when a start request arrives while a fit is
// still running. The manager owns exactly one slot: fitting is CPU-bound and
// evaluates candidates on every core, so a second concurrent fit would not run
// twice as much work, it would run the same work half as fast in each -- and it
// would make "the fitted preset" ambiguous for every read endpoint.
var errFitInProgress = errors.New("a fit is already running")

// errNoFit is returned by the read endpoints when nothing has been started yet.
var errNoFit = errors.New("no fit has been started")

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

	mu          sync.Mutex
	state       fitState
	progress    optimizer.Progress
	stopReason  string
	failure     string
	finishedAt  time.Time
	result      *preset.Preset
	subscribers map[chan struct{}]struct{}
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
	Iteration           int     `json:"iteration"`
	OptimizerIterations int     `json:"optimizerIterations"`
	Evaluations         int     `json:"evaluations"`
	CurrentCost         float64 `json:"currentCost"`
	BestCost            float64 `json:"bestCost"`
	ElapsedMS           int64   `json:"elapsedMs"`

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
}

func newFitJob(id string, request fitRequest, sampleRate int, referenceSeconds float64, cancel context.CancelFunc) *fitJob {
	return &fitJob{
		id:               id,
		request:          request,
		startedAt:        time.Now(),
		sampleRate:       sampleRate,
		referenceSeconds: referenceSeconds,
		cancel:           cancel,
		done:             make(chan struct{}),
		state:            fitRunning,
		subscribers:      make(map[chan struct{}]struct{}),
	}
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
		HasPreset:           j.result != nil,
	}

	if !j.finishedAt.IsZero() {
		finished := j.finishedAt
		snapshot.FinishedAt = &finished
	}

	// While the run is live the optimizer's own Elapsed only advances at a
	// report, so a long gap between reports would look like a stalled clock in
	// the browser. Wall time is what the caller is actually asking about.
	if !j.state.terminal() {
		snapshot.ElapsedMS = time.Since(j.startedAt).Milliseconds()
	}

	return snapshot
}

// report records one optimizer progress callback and wakes every subscriber.
//
// This is the whole of the SSE plumbing on the optimizer's side: it is passed
// as OptimizeOptions.Report, the same hook the CLI uses for checkpointing, so
// internal/optimizer needed no change at all for streaming to exist.
func (j *fitJob) report(progress optimizer.Progress) {
	j.mu.Lock()
	// BestParams is a slice the backend keeps mutating after the callback
	// returns, and it is the only reference-typed field in Progress. Copying it
	// is what keeps a snapshot taken later from describing a vector the
	// optimizer has since moved.
	progress.BestParams = append([]float64(nil), progress.BestParams...)
	j.progress = progress
	j.notifyLocked()
	j.mu.Unlock()
}

// finish moves the job to a terminal state and releases everyone waiting on it.
// It is called exactly once, by the goroutine running the fit, as the last
// thing that goroutine does -- which is what makes "done is closed" mean "the
// job slot is free" rather than "the job asked to stop".
func (j *fitJob) finish(state fitState, result *preset.Preset, final *optimizer.Result, cause error) {
	j.mu.Lock()

	j.state = state
	j.result = result
	j.finishedAt = time.Now()

	// The periodic Report callback is the only other thing that advances
	// j.progress, and reportEvery is allowed to be zero, so a terminal snapshot
	// that did not fold the backend's own Result in would state the numbers of
	// the last report -- or, with no report at all, a finished run that claims
	// zero evaluations and a best cost of zero.
	if final != nil {
		j.stopReason = final.StopReason
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

// running reports whether the goroutine that owns this job is still working.
func (j *fitJob) running() bool {
	select {
	case <-j.done:
		return false
	default:
		return true
	}
}

// jobManager owns the single fit slot.
type jobManager struct {
	mu      sync.Mutex
	current *fitJob
	counter uint64
}

// start claims the slot and launches run on its own goroutine.
//
// The context handed to run is rooted in context.Background rather than in the
// HTTP request: the request that starts a fit returns immediately, so a
// request-scoped context would cancel the search the moment the client had its
// job id. Cancellation therefore has to be explicit -- through the cancel
// endpoint, or through the server's shutdown -- and both go through the
// CancelFunc stored on the job.
func (m *jobManager) start(
	request fitRequest,
	sampleRate int,
	referenceSeconds float64,
	run func(ctx context.Context, job *fitJob),
) (*fitJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current != nil && m.current.running() {
		return nil, errFitInProgress
	}

	m.counter++

	ctx, cancel := context.WithCancel(context.Background())
	job := newFitJob(fmt.Sprintf("fit-%d", m.counter), request, sampleRate, referenceSeconds, cancel)
	m.current = job

	go func() {
		// The context leaks if the run returns without anyone cancelling, which
		// is the ordinary success path.
		defer cancel()

		run(ctx, job)
	}()

	return job, nil
}

// active returns the most recent job, running or finished, or nil. The read
// endpoints answer from the most recent job on purpose: a fit that has just
// ended is exactly the one whose preset a client wants.
func (m *jobManager) active() *fitJob {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.current
}

// cancelActive asks a running fit to stop, without waiting for it. It is what
// the server calls on shutdown, so a fit does not outlive the process that
// owns it.
func (m *jobManager) cancelActive() {
	job := m.active()
	if job == nil {
		return
	}

	job.cancel()
}
