package fitrun_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/analysis"
	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
)

// referencePath is the short synthetic reference: half a second of a bar the
// model itself can render, which keeps a fit of a few hundred evaluations to a
// couple of seconds.
const referencePath = "../../testdata/reference/legacy_synth_a4.wav"

// traceLine is one line of trace.jsonl. The pointers separate an absent key
// from a null one, which is the difference the contract turns on: a line that
// did not improve omits score and terms entirely.
type traceLine struct {
	Iteration           int                `json:"iteration"`
	OptimizerIterations int                `json:"optimizer_iterations"`
	Restart             int                `json:"restart"`
	Lambda              int                `json:"lambda"`
	Evaluations         int                `json:"evaluations"`
	ElapsedMS           int64              `json:"elapsed_ms"`
	Current             *float64           `json:"current"`
	Best                *float64           `json:"best"`
	Score               *float64           `json:"score"`
	Terms               *optimizer.Metrics `json:"terms"`
}

// assertTraceKeyOrder pins the fixed key order of the contract. The order is
// written by hand in trace.go precisely so that it cannot drift, so a test
// that only decodes the lines would not notice if it did.
func assertTraceKeyOrder(t *testing.T, dir string) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, fitrun.FileTrace))
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}

	first, _, ok := bytes.Cut(data, []byte("\n"))
	if !ok {
		t.Fatal("trace has no complete line")
	}

	const prefix = `{"iteration":`

	want := []string{
		"iteration", "optimizer_iterations", "restart", "lambda", "evaluations",
		"elapsed_ms", "current", "best", "score", "terms",
	}

	if !bytes.HasPrefix(first, []byte(prefix)) {
		t.Fatalf("trace line %s does not start with %s", first, prefix)
	}

	at := 0

	for _, key := range want {
		index := bytes.Index(first, []byte(`"`+key+`":`))
		if index < at {
			t.Fatalf("trace line %s does not carry %q after the keys before it", first, key)
		}

		at = index
	}
}

func readTrace(t *testing.T, dir string) []traceLine {
	t.Helper()

	file, err := os.Open(filepath.Join(dir, fitrun.FileTrace))
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}

	defer func() { _ = file.Close() }()

	var lines []traceLine

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line traceLine

		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("parse trace line %q: %v", scanner.Text(), err)
		}

		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("read trace: %v", err)
	}

	return lines
}

func readSummary(t *testing.T, dir string) fitrun.Summary {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, fitrun.FileResult))
	if err != nil {
		t.Fatalf("read result: %v", err)
	}

	var summary fitrun.Summary

	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatalf("decode result: %v", err)
	}

	return summary
}

// referenceLength is how many samples the loader hands the objective, which is
// also how long the render has to be.
func referenceLength(t *testing.T) int {
	t.Helper()

	loaded, err := analysis.LoadReference(referencePath, analysis.LoadOptions{})
	if err != nil {
		t.Fatalf("load reference: %v", err)
	}

	return len(loaded.Samples)
}

func cmaesSpec(dir string) fitrun.Spec {
	return fitrun.Spec{
		Dir:             dir,
		ReferencePath:   referencePath,
		Note:            69,
		Engine:          fitrun.Engine{Name: fitrun.EngineCMAES},
		MaxEvaluations:  300,
		Seed:            7,
		Workers:         2,
		CheckpointEvery: 1,
	}
}

func TestRunWritesEveryFileOfTheContract(t *testing.T) {
	dir := t.TempDir()

	outcome, err := fitrun.Run(context.Background(), cmaesSpec(dir), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, name := range []string{
		fitrun.FileConfig, fitrun.FileAnalysis, fitrun.FileTrace, fitrun.FileCheckpoint,
		fitrun.FilePreset, fitrun.FileRender, fitrun.FileResult, fitrun.FileLog,
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
		}
	}

	var config struct {
		Seed     int64 `json:"seed"`
		Resolved struct {
			Seed    int64 `json:"seed"`
			Workers int   `json:"workers"`
			Lambda  int   `json:"lambda"`
		} `json:"resolved"`
		Identity  fitrun.Identity `json:"identity"`
		Reference struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"reference"`
		Finished *string `json:"finished"`
	}

	data, err := os.ReadFile(filepath.Join(dir, fitrun.FileConfig))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}

	if config.Seed != 7 || config.Resolved.Seed != 7 {
		t.Errorf("config seed = %d, resolved %d, want 7 for both", config.Seed, config.Resolved.Seed)
	}

	if config.Resolved.Workers != 2 || config.Resolved.Lambda <= 0 {
		t.Errorf("resolved workers = %d, lambda = %d, want 2 and a positive lambda",
			config.Resolved.Workers, config.Resolved.Lambda)
	}

	if config.Identity.Go == "" || config.Identity.Libraries[fitrun.MayflyLibrary] == "" {
		t.Errorf("config identity is incomplete: %+v", config.Identity)
	}

	sum, err := fitrun.FileSHA256(referencePath)
	if err != nil {
		t.Fatalf("hash reference: %v", err)
	}

	if config.Reference.SHA256 != sum {
		t.Errorf("config reference sha256 = %q, want %q", config.Reference.SHA256, sum)
	}

	if config.Finished == nil {
		t.Error("config finished is absent, so the final write did not happen")
	}

	assertTraceKeyOrder(t, dir)

	lines := readTrace(t, dir)
	if len(lines) == 0 {
		t.Fatal("trace is empty")
	}

	if lines[0].Terms == nil || lines[0].Score == nil {
		t.Error("the first trace line carries no terms")
	}

	// The population of the run that produced each line is what tells a reader
	// of an IPOP ladder which rung a cost came from, so a cmaes trace has to
	// carry it.
	if lines[0].Lambda != config.Resolved.Lambda {
		t.Errorf("first trace line lambda = %d, want the resolved %d",
			lines[0].Lambda, config.Resolved.Lambda)
	}

	best := *lines[0].Best

	for i, line := range lines[1:] {
		if line.Best == nil {
			t.Fatalf("trace line %d carries no best cost", i+1)
		}

		switch {
		case *line.Best < best:
			if line.Terms == nil || line.Score == nil {
				t.Errorf("trace line %d improved to %g but carries no terms", i+1, *line.Best)
			}

			best = *line.Best
		case line.Terms != nil:
			t.Errorf("trace line %d did not improve on %g but carries terms", i+1, best)
		}
	}

	summary := readSummary(t, dir)
	if summary.Score != outcome.Summary.Score {
		t.Errorf("result.json score = %g, outcome score = %g", summary.Score, outcome.Summary.Score)
	}

	fitted, err := preset.Load(filepath.Join(dir, fitrun.FilePreset))
	if err != nil {
		t.Fatalf("load fitted preset: %v", err)
	}

	if fitted.Provenance == nil {
		t.Fatal("the fitted preset carries no provenance block")
	}

	if fitted.Provenance.Score != summary.Score {
		t.Errorf("provenance score = %g, want the summary's %g", fitted.Provenance.Score, summary.Score)
	}

	summaryTerms, err := json.Marshal(summary.Terms)
	if err != nil {
		t.Fatalf("encode summary terms: %v", err)
	}

	// Compacted before the comparison: preset.Save indents the whole document,
	// which re-flows the raw terms message without changing what it says.
	var compacted bytes.Buffer

	if err := json.Compact(&compacted, fitted.Provenance.Terms); err != nil {
		t.Fatalf("compact provenance terms: %v", err)
	}

	if !bytes.Equal(summaryTerms, compacted.Bytes()) {
		t.Errorf("provenance terms = %s, want the summary's %s", compacted.Bytes(), summaryTerms)
	}

	if fitted.Provenance.Engine.Name != fitrun.EngineCMAES || fitted.Provenance.Engine.Lambda <= 0 {
		t.Errorf("provenance engine = %+v, want a cmaes block with a lambda", fitted.Provenance.Engine)
	}

	rendered, sampleRate, err := wavio.LoadMono(filepath.Join(dir, fitrun.FileRender))
	if err != nil {
		t.Fatalf("load render: %v", err)
	}

	if sampleRate != 44100 {
		t.Errorf("render sample rate = %d, want 44100", sampleRate)
	}

	if length := referenceLength(t); len(rendered) != length {
		t.Errorf("render is %d samples, want the reference's %d", len(rendered), length)
	}
}

func TestRunIsReproducibleAtAFixedSeedAndWidth(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()

	firstOutcome, err := fitrun.Run(context.Background(), cmaesSpec(first), nil)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	secondOutcome, err := fitrun.Run(context.Background(), cmaesSpec(second), nil)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if firstOutcome.Summary.Score != secondOutcome.Summary.Score {
		t.Errorf("scores differ: %g and %g", firstOutcome.Summary.Score, secondOutcome.Summary.Score)
	}

	firstParams := presetParameters(t, filepath.Join(first, fitrun.FilePreset))
	secondParams := presetParameters(t, filepath.Join(second, fitrun.FilePreset))

	if !bytes.Equal(firstParams, secondParams) {
		t.Error("the two runs wrote different parameters at the same seed and width")
	}
}

// presetParameters returns the parameters object of a written preset, which is
// the part two runs at the same seed must agree on byte for byte. The
// provenance block is deliberately left out: it carries a timestamp.
func presetParameters(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read preset: %v", err)
	}

	var document struct {
		Parameters json.RawMessage `json:"parameters"`
	}

	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode preset: %v", err)
	}

	return document.Parameters
}

// cancellingWriter cancels a run as soon as it has reported once, which is the
// only deterministic place to cut a search short from outside it.
type cancellingWriter struct {
	cancel context.CancelFunc
	seen   bool
}

func (w *cancellingWriter) Write(chunk []byte) (int, error) {
	if !w.seen && bytes.Contains(chunk, []byte("iteration ")) {
		w.seen = true

		w.cancel()
	}

	return len(chunk), nil
}

func TestRunHonoursACancelledContext(t *testing.T) {
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	spec := cmaesSpec(dir)
	spec.MaxEvaluations = 5000

	if _, err := fitrun.Run(ctx, spec, &cancellingWriter{cancel: cancel}); err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, name := range []string{
		fitrun.FileConfig, fitrun.FileAnalysis, fitrun.FileTrace, fitrun.FileCheckpoint,
		fitrun.FilePreset, fitrun.FileRender, fitrun.FileResult, fitrun.FileLog,
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to exist after a cancelled run: %v", name, err)
		}
	}

	if summary := readSummary(t, dir); summary.StopReason != "context_canceled" {
		t.Errorf("stop_reason = %q, want context_canceled", summary.StopReason)
	}
}

func TestRunRunsMayflyWithRestarts(t *testing.T) {
	dir := t.TempDir()

	spec := fitrun.Spec{
		Dir:           dir,
		ReferencePath: referencePath,
		Note:          69,
		Engine: fitrun.Engine{
			Name:   fitrun.EngineMayfly,
			Mayfly: fitrun.MayflySettings{Population: 10, Restarts: 1},
		},
		MaxEvaluations: 400,
		Seed:           7,
		Workers:        2,
	}

	outcome, err := fitrun.Run(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if outcome.Summary.Population != 10 {
		t.Errorf("summary population = %d, want 10", outcome.Summary.Population)
	}

	lines := readTrace(t, dir)
	if len(lines) == 0 {
		t.Fatal("trace is empty")
	}

	if spent := lines[len(lines)-1].Evaluations; spent <= 200 {
		t.Errorf("the trace stops at %d evaluations, want a restart to carry it past 200", spent)
	}
}

// TestRunRunsTheSimpleEngine covers the third backend, which the campaign does
// not use and which was therefore the one path through buildOptimizer with no
// test of its own. Nelder-Mead takes no population and no seed, so the budget
// is an iteration count and the run is short.
func TestRunRunsTheSimpleEngine(t *testing.T) {
	dir := t.TempDir()

	spec := fitrun.Spec{
		Dir:           dir,
		ReferencePath: referencePath,
		Note:          69,
		Engine:        fitrun.Engine{Name: fitrun.EngineSimple},
		MaxIterations: 5,
		Workers:       2,
	}

	outcome, err := fitrun.Run(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, name := range []string{
		fitrun.FileConfig, fitrun.FileAnalysis, fitrun.FileTrace,
		fitrun.FilePreset, fitrun.FileRender, fitrun.FileResult, fitrun.FileLog,
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
		}
	}

	summary := readSummary(t, dir)
	if summary.Score != outcome.Summary.Score {
		t.Errorf("result.json score = %g, outcome score = %g", summary.Score, outcome.Summary.Score)
	}

	if summary.Evaluations <= 0 || summary.Dimension <= 0 {
		t.Errorf("summary = %+v, want a search that spent evaluations over a positive dimension", summary)
	}

	lines := readTrace(t, dir)
	if len(lines) == 0 {
		t.Fatal("trace is empty")
	}

	// Nelder-Mead is not a restart loop and has no population, so both keys are
	// written as zero rather than left out.
	if lines[0].Restart != 0 || lines[0].Lambda != 0 {
		t.Errorf("first trace line restart = %d, lambda = %d, want zero for both",
			lines[0].Restart, lines[0].Lambda)
	}

	if lines[0].Terms == nil || lines[0].Score == nil {
		t.Error("the first trace line carries no terms")
	}
}

// TestRunSurvivesACheckpointItCannotWrite pins the ruling that a checkpoint is
// a convenience for a resume and not something a finished search is thrown
// away for. The path is made unwritable by putting a directory where the file
// belongs, which is the failure a full disk or a stale permission produces.
func TestRunSurvivesACheckpointItCannotWrite(t *testing.T) {
	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, fitrun.FileCheckpoint), 0o755); err != nil {
		t.Fatalf("stage an unwritable checkpoint: %v", err)
	}

	var log bytes.Buffer

	spec := cmaesSpec(dir)

	outcome, err := fitrun.Run(context.Background(), spec, &log)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if outcome.Summary.Score <= 0 {
		t.Errorf("summary score = %g, want a finished search", outcome.Summary.Score)
	}

	for _, name := range []string{
		fitrun.FileConfig, fitrun.FileTrace, fitrun.FilePreset, fitrun.FileResult,
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to exist despite the checkpoint failure: %v", name, err)
		}
	}

	if !strings.Contains(log.String(), "checkpoint:") {
		t.Errorf("the run log does not report the checkpoint failure: %s", log.String())
	}
}

// TestOnProgressReceivesMonotonicIterationsAndMetrics pins the hook a live
// server needs: it fires on the exact cadence the trace file does, in the same
// order, and carries the same breakdown an improving line writes rather than a
// second, independently measured one.
func TestOnProgressReceivesMonotonicIterationsAndMetrics(t *testing.T) {
	dir := t.TempDir()

	var (
		calls      int
		lastIter   int
		sawMetrics bool
	)

	spec := cmaesSpec(dir)
	spec.OnProgress = func(progress optimizer.Progress, metrics *optimizer.Metrics) {
		if calls > 0 && progress.OptimizerIterations < lastIter {
			t.Errorf("optimizer iterations went backwards: %d after %d", progress.OptimizerIterations, lastIter)
		}

		lastIter = progress.OptimizerIterations
		calls++

		if metrics != nil {
			sawMetrics = true
		}
	}

	if _, err := fitrun.Run(context.Background(), spec, nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	if calls == 0 {
		t.Fatal("OnProgress was never called")
	}

	if !sawMetrics {
		t.Error("OnProgress never received a non-nil Metrics, want at least the first (improving) report to carry one")
	}
}

// TestOnResolveFiresOnceBeforeTheFirstProgressCall pins the ordering a live
// server relies on: the resolved seed and worker count have to be known
// before the first progress update can be attributed to a repeatable run.
func TestOnResolveFiresOnceBeforeTheFirstProgressCall(t *testing.T) {
	dir := t.TempDir()

	var (
		resolveCalls  int
		progressCalls int
		orderViolated bool
	)

	spec := cmaesSpec(dir)
	spec.OnResolve = func(fitrun.Resolved) {
		resolveCalls++

		if progressCalls > 0 {
			orderViolated = true
		}
	}
	spec.OnProgress = func(optimizer.Progress, *optimizer.Metrics) {
		progressCalls++
	}

	if _, err := fitrun.Run(context.Background(), spec, nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	if resolveCalls != 1 {
		t.Errorf("OnResolve was called %d times, want exactly 1", resolveCalls)
	}

	if orderViolated {
		t.Error("OnResolve fired after the first OnProgress call")
	}
}

// TestExplicitBoundsProduceACodecWithTheGivenBox pins the ruling that a
// non-nil Spec.Bounds replaces the whole box rather than widening it, exactly
// as internal/server/fit.go's client-supplied bounds do. The amplitude range
// is narrowed far below what the reference actually calls for, so a preset
// that ships outside it can only mean the box was never wired to the codec.
func TestExplicitBoundsProduceACodecWithTheGivenBox(t *testing.T) {
	dir := t.TempDir()

	bounds := optimizer.DefaultParamBounds
	bounds.Amplitude = optimizer.Range{Min: 0.1, Max: 0.2}

	spec := fitrun.Spec{
		Dir:           dir,
		ReferencePath: referencePath,
		Note:          69,
		Engine:        fitrun.Engine{Name: fitrun.EngineSimple},
		MaxIterations: 3,
		Workers:       2,
		Bounds:        &bounds,
		StrictBounds:  true,
	}

	outcome, err := fitrun.Run(context.Background(), spec, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	for i, mode := range outcome.Preset.Parameters.Modes {
		if mode.Amplitude < bounds.Amplitude.Min || mode.Amplitude > bounds.Amplitude.Max {
			t.Errorf("mode %d amplitude = %g, want inside the given box [%g, %g]",
				i, mode.Amplitude, bounds.Amplitude.Min, bounds.Amplitude.Max)
		}
	}
}

// TestServedSpecFieldsReachConfigJSON pins the config.json half of Task 2's
// point: a served fit's client-supplied bounds, alignment and gain settings
// have to be recorded, or two fits differing only in one of them write
// byte-identical config.json and neither can be reproduced from its own
// record.
func TestServedSpecFieldsReachConfigJSON(t *testing.T) {
	dir := t.TempDir()

	bounds := optimizer.DefaultParamBounds
	bounds.Amplitude = optimizer.Range{Min: 0.1, Max: 0.2}
	alignment := optimizer.AlignNone

	spec := fitrun.Spec{
		Dir:           dir,
		ReferencePath: referencePath,
		Note:          69,
		Engine:        fitrun.Engine{Name: fitrun.EngineSimple},
		MaxIterations: 2,
		Workers:       2,
		Bounds:        &bounds,
		StrictBounds:  true,
		Alignment:     &alignment,
		Gain:          optimizer.GainLeastSquares,
	}

	if _, err := fitrun.Run(context.Background(), spec, nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, fitrun.FileConfig))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var config struct {
		Bounds *struct {
			Amplitude struct {
				Min float64 `json:"min"`
				Max float64 `json:"max"`
			} `json:"amplitude"`
		} `json:"bounds"`
		StrictBounds bool    `json:"strict_bounds"`
		Alignment    *string `json:"alignment"`
		Gain         string  `json:"gain"`
	}

	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}

	if config.Bounds == nil || config.Bounds.Amplitude.Min != 0.1 || config.Bounds.Amplitude.Max != 0.2 {
		t.Errorf("config bounds = %+v, want amplitude [0.1, 0.2]", config.Bounds)
	}

	if !config.StrictBounds {
		t.Error("config strict_bounds = false, want true")
	}

	if config.Alignment == nil || *config.Alignment != "none" {
		t.Errorf("config alignment = %v, want \"none\"", config.Alignment)
	}

	if config.Gain != "least_squares" {
		t.Errorf("config gain = %q, want \"least_squares\"", config.Gain)
	}
}

// TestNilAlignmentKeepsOnsetCorrelation pins the ruling that a nil
// Spec.Alignment leaves the objective at today's default rather than at
// AlignNone, the enum's zero value. AlignOnsetCorrelation and AlignNone solve
// the gain and waveform terms over different windows, so an explicit
// AlignOnsetCorrelation run and the nil run below must score identically
// under the Simple engine's deterministic Nelder-Mead search; that they
// diverge from an explicit AlignNone run is pinned separately by
// TestExplicitAlignNoneChangesTheScore.
func TestNilAlignmentKeepsOnsetCorrelation(t *testing.T) {
	base := fitrun.Spec{
		ReferencePath: referencePath,
		Note:          69,
		Engine:        fitrun.Engine{Name: fitrun.EngineSimple},
		MaxIterations: 3,
		Workers:       1,
	}

	specNil := base
	specNil.Dir = t.TempDir()

	outcomeNil, err := fitrun.Run(context.Background(), specNil, nil)
	if err != nil {
		t.Fatalf("nil alignment run: %v", err)
	}

	onset := optimizer.AlignOnsetCorrelation
	specOnset := base
	specOnset.Dir = t.TempDir()
	specOnset.Alignment = &onset

	outcomeOnset, err := fitrun.Run(context.Background(), specOnset, nil)
	if err != nil {
		t.Fatalf("explicit AlignOnsetCorrelation run: %v", err)
	}

	if outcomeNil.Metrics.GainDB != outcomeOnset.Metrics.GainDB {
		t.Errorf("nil alignment gain = %g, want the explicit AlignOnsetCorrelation gain %g",
			outcomeNil.Metrics.GainDB, outcomeOnset.Metrics.GainDB)
	}

	if outcomeNil.Summary.Score != outcomeOnset.Summary.Score {
		t.Errorf("nil alignment score = %g, want the explicit AlignOnsetCorrelation score %g",
			outcomeNil.Summary.Score, outcomeOnset.Summary.Score)
	}
}

// TestExplicitAlignNoneChangesTheScore pins the ruling that a non-nil
// Spec.Alignment actually reaches the objective config rather than being read
// and dropped. AlignNone skips the onset-correlation search the default
// alignment runs, which moves the gain and waveform terms even on this
// reference, whose candidate happens to need no time shift at all: the two
// modes solve those terms over different windows regardless of the lag
// either one finds, so a run forced to AlignNone must score differently from
// the same run left at the default.
func TestExplicitAlignNoneChangesTheScore(t *testing.T) {
	base := fitrun.Spec{
		ReferencePath: referencePath,
		Note:          69,
		Engine:        fitrun.Engine{Name: fitrun.EngineSimple},
		MaxIterations: 3,
		Workers:       1,
	}

	specDefault := base
	specDefault.Dir = t.TempDir()

	outcomeDefault, err := fitrun.Run(context.Background(), specDefault, nil)
	if err != nil {
		t.Fatalf("default alignment run: %v", err)
	}

	none := optimizer.AlignNone
	specNone := base
	specNone.Dir = t.TempDir()
	specNone.Alignment = &none

	outcomeNone, err := fitrun.Run(context.Background(), specNone, nil)
	if err != nil {
		t.Fatalf("explicit AlignNone run: %v", err)
	}

	if outcomeNone.Metrics.GainDB == outcomeDefault.Metrics.GainDB {
		t.Errorf("AlignNone gain = %g, want it to differ from the default alignment's gain %g; "+
			"the pointer did not reach the objective config",
			outcomeNone.Metrics.GainDB, outcomeDefault.Metrics.GainDB)
	}
}

// TestReferenceWavMatchesAnalysisJSONsCut pins reference.wav to the signal the
// objective actually scored: the same cut analysis.json records, not a fresh
// reload of the source file under some other policy.
func TestReferenceWavMatchesAnalysisJSONsCut(t *testing.T) {
	dir := t.TempDir()

	spec := fitrun.Spec{
		Dir:           dir,
		ReferencePath: referencePath,
		Note:          69,
		Engine:        fitrun.Engine{Name: fitrun.EngineSimple},
		MaxIterations: 2,
		Workers:       2,
	}

	if _, err := fitrun.Run(context.Background(), spec, nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, fitrun.FileAnalysis))
	if err != nil {
		t.Fatalf("read analysis: %v", err)
	}

	var document struct {
		Reference struct {
			Onset int `json:"onset"`
			End   int `json:"end"`
		} `json:"reference"`
	}

	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode analysis: %v", err)
	}

	want := document.Reference.End - document.Reference.Onset

	samples, _, err := wavio.LoadMono(filepath.Join(dir, fitrun.FileReference))
	if err != nil {
		t.Fatalf("load %s: %v", fitrun.FileReference, err)
	}

	if len(samples) != want {
		t.Errorf("%s has %d samples, want %d (analysis.json's onset..end cut)",
			fitrun.FileReference, len(samples), want)
	}
}

// TestLongReferenceRecordsTheDefaultFrameSize pins analysisFrameSize's no-op
// case: a reference at least as long as analysis.DefaultFrameSize is measured
// at that window, unhalved. legacy_synth_a4.wav cuts to 24579 samples, well
// over the 16384-point default, so nothing here should ever shrink it. The
// value is read back from analysis.json rather than the internal function,
// because it is the recorded number a later edit to the halving rule would
// have to keep honest, not the call that produced it.
func TestLongReferenceRecordsTheDefaultFrameSize(t *testing.T) {
	dir := t.TempDir()

	spec := fitrun.Spec{
		Dir:           dir,
		ReferencePath: referencePath,
		Note:          69,
		Engine:        fitrun.Engine{Name: fitrun.EngineSimple},
		MaxIterations: 2,
		Workers:       2,
	}

	if _, err := fitrun.Run(context.Background(), spec, nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	document := readAnalysisFrameSize(t, dir)

	if document.Reference.End-document.Reference.Onset < analysis.DefaultFrameSize {
		t.Fatalf("reference cut is %d samples, too short to prove the no-op case",
			document.Reference.End-document.Reference.Onset)
	}

	if document.Options.FrameSize != analysis.DefaultFrameSize {
		t.Errorf("analysis.json options.FrameSize = %d, want the default %d",
			document.Options.FrameSize, analysis.DefaultFrameSize)
	}
}

// TestShortReferenceRecordsAHalvedFrameSize pins analysisFrameSize's other
// branch: a reference shorter than the default window is measured at a
// smaller one, halved down until it fits, rather than failing outright. A
// fit against a fifth of a second, which is what a single strike auditioned
// through the server can be, must still produce an analysis.json.
func TestShortReferenceRecordsAHalvedFrameSize(t *testing.T) {
	dir := t.TempDir()
	shortPath := filepath.Join(dir, "short.wav")

	template, err := preset.Load(filepath.FromSlash("../../testdata/presets/minimal.json"))
	if err != nil {
		t.Fatalf("load preset: %v", err)
	}

	engine, err := synth.NewSynthesizer(template, 44100)
	if err != nil {
		t.Fatalf("new synthesizer: %v", err)
	}

	if err := wavio.WriteMono(shortPath, 44100, engine.RenderNote(69, 100, 0.05)); err != nil {
		t.Fatalf("write short reference: %v", err)
	}

	spec := fitrun.Spec{
		Dir:           dir,
		ReferencePath: shortPath,
		Note:          69,
		Engine:        fitrun.Engine{Name: fitrun.EngineSimple},
		MaxIterations: 2,
		Workers:       2,
	}

	if _, err := fitrun.Run(context.Background(), spec, nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	document := readAnalysisFrameSize(t, dir)

	cut := document.Reference.End - document.Reference.Onset
	if cut >= analysis.DefaultFrameSize {
		t.Fatalf("reference cut is %d samples, too long to prove the halving case", cut)
	}

	want := analysis.DefaultFrameSize
	for want > cut {
		want /= 2
	}

	if document.Options.FrameSize != want {
		t.Errorf("analysis.json options.FrameSize = %d, want %d (halved to fit %d samples)",
			document.Options.FrameSize, want, cut)
	}
}

// readAnalysisFrameSize decodes the cut and the measurement window out of a
// run directory's analysis.json.
func readAnalysisFrameSize(t *testing.T, dir string) struct {
	Reference struct {
		Onset int `json:"onset"`
		End   int `json:"end"`
	} `json:"reference"`
	Options struct {
		FrameSize int `json:"FrameSize"`
	} `json:"options"`
} {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, fitrun.FileAnalysis))
	if err != nil {
		t.Fatalf("read analysis: %v", err)
	}

	var document struct {
		Reference struct {
			Onset int `json:"onset"`
			End   int `json:"end"`
		} `json:"reference"`
		Options struct {
			FrameSize int `json:"FrameSize"`
		} `json:"options"`
	}

	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode analysis: %v", err)
	}

	return document
}

// TestMayflyReportsBothRoundsAsRestarts pins Progress.Restart to the
// schedule's own round index, per the field's contract: the zero-based index
// of the search in progress. Before this, mayfly never set the field, so every
// front end watching a scheduled run saw restart zero for the whole run.
func TestMayflyReportsBothRoundsAsRestarts(t *testing.T) {
	dir := t.TempDir()

	spec := fitrun.Spec{
		Dir:           dir,
		ReferencePath: referencePath,
		Note:          69,
		Engine: fitrun.Engine{
			Name:   fitrun.EngineMayfly,
			Mayfly: fitrun.MayflySettings{Population: 10, Restarts: 1},
		},
		MaxEvaluations: 400,
		Seed:           7,
		Workers:        2,
	}

	if _, err := fitrun.Run(context.Background(), spec, nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	seen := map[int]bool{}
	for _, line := range readTrace(t, dir) {
		seen[line.Restart] = true
	}

	if !seen[0] || !seen[1] {
		t.Errorf("trace restarts seen = %v, want both round 0 and round 1", seen)
	}
}

// TestCampaignSpecStillWritesTheSameConfigFields is the campaign smoke test
// the brief asks for: a Spec built the way internal/campaign/run.go builds
// one, with none of this task's additions set, writes exactly the config.json
// field set it wrote before they existed.
func TestCampaignSpecStillWritesTheSameConfigFields(t *testing.T) {
	dir := t.TempDir()

	spec := fitrun.Spec{
		Dir:           dir,
		ReferencePath: referencePath,
		Note:          69,
		Engine:        fitrun.Engine{Name: fitrun.EngineSimple},
		MaxIterations: 3,
		Seed:          7,
		Workers:       2,
		ReportEvery:   1,
		GeneratedBy:   "glockenspiel-campaign",
		Name:          "smoke/arm/b00",
	}

	if _, err := fitrun.Run(context.Background(), spec, nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, fitrun.FileConfig))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("decode config: %v", err)
	}

	want := []string{
		"dir", "reference_options", "template", "modes", "note", "velocity",
		"sample_rate", "metric", "engine", "max_iterations", "max_evaluations",
		"time_budget", "seed", "workers", "report_every", "checkpoint_every",
		"generated_by", "name", "resolved", "identity", "reference", "started", "finished",
	}

	for _, key := range want {
		if _, ok := fields[key]; !ok {
			t.Errorf("config.json is missing %q", key)
		}
	}

	if len(fields) != len(want) {
		t.Errorf("config.json has %d top-level fields, want %d: %v", len(fields), len(want), fields)
	}
}
