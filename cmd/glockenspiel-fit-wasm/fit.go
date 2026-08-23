//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"sync"
	"syscall/js"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/browserfit"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

var globalFits wasmFitManager

type wasmFitManager struct {
	mu     sync.Mutex
	serial uint64
	active *wasmFitJob
}

type wasmFitJob struct {
	mu       sync.Mutex
	id       string
	prepared *browserfit.Prepared
	request  browserfit.Request
	callback js.Value
	cancel   context.CancelFunc
	started  time.Time

	state      string
	progress   optimizer.Progress
	reports    int
	stopReason string
	failure    string
	finished   time.Time
	fitted     *preset.Preset
}

type wasmFitSnapshot struct {
	JobID string `json:"jobId"`
	State string `json:"state"`

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
	HasPreset  bool       `json:"hasPreset"`
}

func (m *wasmFitManager) start(_ js.Value, args []js.Value) any {
	if len(args) < 5 || args[4].Type() != js.TypeFunction {
		return "fitStart needs a request, reference, preset, bounds and callback"
	}

	m.mu.Lock()
	if m.active != nil && m.active.running() {
		m.mu.Unlock()

		return "a fit is already running"
	}
	m.mu.Unlock()

	request, err := browserfit.DecodeRequest([]byte(args[0].String()))
	if err != nil {
		return err.Error()
	}

	reference, err := copyBytes(args[1], browserfit.MaxReferenceBytes)
	if err != nil {
		return "read reference: " + err.Error()
	}

	startingPreset, err := copyBytes(args[2], 4<<20)
	if err != nil {
		return "read starting preset: " + err.Error()
	}

	bounds, err := copyBytes(args[3], 1<<20)
	if err != nil {
		return "read bounds: " + err.Error()
	}

	prepared, err := browserfit.New(request, reference, startingPreset, bounds)
	if err != nil {
		return err.Error()
	}

	ctx, cancel := context.WithCancel(context.Background())

	m.mu.Lock()
	m.serial++
	job := &wasmFitJob{
		id:       "wasm-" + strconv.FormatUint(m.serial, 10),
		prepared: prepared,
		request:  prepared.Request(),
		callback: args[4],
		cancel:   cancel,
		started:  time.Now(),
		state:    "running",
	}
	m.active = job
	m.mu.Unlock()

	job.emit()

	go job.run(ctx)

	return nil
}

func (m *wasmFitManager) cancel(_ js.Value, args []js.Value) any {
	m.mu.Lock()
	job := m.active
	m.mu.Unlock()

	if job == nil {
		return "no fit has been started"
	}

	if len(args) > 0 && args[0].String() != "" && args[0].String() != job.id {
		return fmt.Sprintf("the current fit is %s, not %s", job.id, args[0].String())
	}

	if !job.running() {
		job.emit()

		return nil
	}

	job.cancel()

	return nil
}

func (m *wasmFitManager) preset(_ js.Value, _ []js.Value) any {
	job, fitted, err := m.fittedPreset()
	if err != nil {
		return jsResult(nil, err)
	}

	data, err := browserfit.MarshalPreset(fitted)
	if err != nil {
		return jsResult(nil, fmt.Errorf("encode preset for %s: %w", job.id, err))
	}

	return jsResult(string(data), nil)
}

func (m *wasmFitManager) render(_ js.Value, args []js.Value) any {
	if len(args) < 3 {
		return jsResult(nil, fmt.Errorf("fitRender needs note, velocity and duration"))
	}

	job, fitted, err := m.fittedPreset()
	if err != nil {
		return jsResult(nil, err)
	}

	seconds := args[2].Float()
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return jsResult(nil, fmt.Errorf("duration must be finite"))
	}

	data, err := browserfit.Render(fitted, job.prepared.SampleRate(),
		args[0].Int(), args[1].Int(), time.Duration(seconds*float64(time.Second)))
	if err != nil {
		return jsResult(nil, err)
	}

	array := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(array, data)

	return jsResult(array, nil)
}

func (m *wasmFitManager) fittedPreset() (*wasmFitJob, *preset.Preset, error) {
	m.mu.Lock()
	job := m.active
	m.mu.Unlock()

	if job == nil {
		return nil, nil, fmt.Errorf("no fit has been started")
	}

	job.mu.Lock()
	defer job.mu.Unlock()

	if job.fitted == nil {
		return nil, nil, fmt.Errorf("fit %s has produced no preset yet", job.id)
	}

	return job, job.fitted.Clone(), nil
}

func (j *wasmFitJob) running() bool {
	j.mu.Lock()
	defer j.mu.Unlock()

	return j.state == "running"
}

func (j *wasmFitJob) run(ctx context.Context) {
	result, err := j.prepared.Run(ctx, j.report)
	if err != nil {
		j.finish("failed", nil, err)

		return
	}

	fitted, err := j.prepared.Preset(result.BestParams)
	if err != nil {
		j.finish("failed", result, err)

		return
	}

	state := "succeeded"
	if ctx.Err() != nil {
		state = "canceled"
	}

	j.mu.Lock()
	j.fitted = fitted
	j.mu.Unlock()

	j.finish(state, result, nil)
}

func (j *wasmFitJob) report(progress optimizer.Progress) {
	fitted, err := j.prepared.Preset(progress.BestParams)

	j.mu.Lock()

	j.progress = progress
	if err == nil {
		j.fitted = fitted
	}

	visible := j.request.ReportEvery > 0 &&
		progress.OptimizerIterations%j.request.ReportEvery == 0
	if visible {
		j.reports++
	}
	j.mu.Unlock()

	if visible {
		j.emit()
	}

	// A CPU-bound Go WASM goroutine otherwise owns this worker's JavaScript
	// event loop until the optimizer returns. Sleeping hands control back long
	// enough for a queued Cancel command (and the progress postMessage) to run.
	time.Sleep(time.Millisecond)
}

func (j *wasmFitJob) finish(state string, result *optimizer.Result, cause error) {
	j.mu.Lock()
	j.state = state
	j.finished = time.Now()

	if result != nil {
		j.progress.OptimizerIterations = result.Iterations
		j.progress.Evaluations = result.Evaluations
		j.progress.CurrentCost = result.BestCost
		j.progress.BestCost = result.BestCost
		j.progress.Elapsed = result.Elapsed
		j.stopReason = result.StopReason
	}

	if cause != nil {
		j.failure = cause.Error()
	}
	j.mu.Unlock()

	j.emit()
}

func (j *wasmFitJob) emit() {
	snapshot := j.snapshot()

	data, err := json.Marshal(snapshot)
	if err != nil {
		println("encode WASM fit snapshot:", err.Error())

		return
	}

	j.callback.Invoke(string(data))
}

func (j *wasmFitJob) snapshot() wasmFitSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()

	elapsed := time.Since(j.started)
	if j.state != "running" && !j.finished.IsZero() {
		elapsed = j.progress.Elapsed
	}

	snapshot := wasmFitSnapshot{
		JobID:               j.id,
		State:               j.state,
		Iteration:           j.reports,
		OptimizerIterations: j.progress.OptimizerIterations,
		Evaluations:         j.progress.Evaluations,
		CurrentCost:         j.progress.CurrentCost,
		BestCost:            j.progress.BestCost,
		ElapsedMS:           elapsed.Milliseconds(),
		StopReason:          j.stopReason,
		Error:               j.failure,
		SampleRate:          j.prepared.SampleRate(),
		ReferenceSeconds:    j.prepared.ReferenceSeconds(),
		Note:                j.request.Note,
		Velocity:            j.request.Velocity,
		Optimizer:           j.request.Optimizer,
		Metric:              j.request.Metric,
		StartedAt:           j.started,
		HasPreset:           j.fitted != nil,
	}

	if !j.finished.IsZero() {
		finished := j.finished
		snapshot.FinishedAt = &finished
	}

	return snapshot
}

func copyBytes(value js.Value, limit int) ([]byte, error) {
	length := value.Get("byteLength")
	if length.Type() != js.TypeNumber {
		return nil, fmt.Errorf("input is not a byte array")
	}

	size := length.Int()
	if size > limit {
		return nil, fmt.Errorf("input is %d bytes, above the %d byte limit", size, limit)
	}

	data := make([]byte, size)
	if copied := js.CopyBytesToGo(data, value); copied != size {
		return nil, fmt.Errorf("copied %d of %d bytes", copied, size)
	}

	return data, nil
}

func jsResult(data any, err error) js.Value {
	result := js.Global().Get("Object").New()
	if err != nil {
		result.Set("error", err.Error())

		return result
	}

	result.Set("data", data)

	return result
}
