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
