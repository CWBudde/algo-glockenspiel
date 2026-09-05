package server

// This file is package server, not server_test: following a live run directory
// is driven by a ticker whose period is a field on the Server, and a test that
// had to wait the real interval would spend seconds per tick doing nothing.
// The rest of what it asserts goes through the same HTTP handler every other
// test of this package uses.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
)

// followTestInterval is fast enough that a test spends its time on the
// filesystem rather than on the clock, and slow enough to be a tick rather
// than a spin.
const followTestInterval = 5 * time.Millisecond

// TestAServerFollowsARunDirectoryBeingWritten is the pinning test for Phase
// 8.8's second item: a fit running in another process -- `glockenspiel fit` in
// a second terminal, or a campaign job -- is a directory being filled in line
// by line, and the server watching that directory has to read it as a live run
// rather than as a corpse or as nothing at all.
//
// The run directory is written here by hand, in the order fitrun writes it, so
// that the assertions are about what the server makes of the files rather than
// about how fast some real search happens to converge.
func TestAServerFollowsARunDirectoryBeingWritten(t *testing.T) {
	workDir := t.TempDir()

	const jobID = "fit-20240102T030405-0001"

	dir := filepath.Join(workDir, jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make the run directory: %v", err)
	}

	writeRunConfig(t, dir)

	trace := openTrace(t, dir)

	server := newFollowingServer(t, workDir)
	handler := server.Handler()

	// The directory holds a config.json and no result.json, so it is a fit in
	// flight and the server says so.
	row := waitForListing(t, handler, jobID, func(row fitJobListing) bool {
		return row.State == fitRunning
	})

	if !row.Followed {
		t.Fatal("a run the server did not start is not marked as followed")
	}

	if row.Score != nil {
		t.Errorf("a run that has written no result.json reports a score of %v", *row.Score)
	}

	// The trace grows a line at a time, and the best cost the server reports
	// falls with it. Each line is written whole, which is what fitrun's own
	// per-line flush guarantees; the tail's refusal to consume a partial line
	// is covered by TestAFollowedTraceIgnoresAPartialLine.
	for _, best := range []float64{9.5, 4.25, 1.125} {
		appendTraceLine(t, trace, best)

		row = waitForListing(t, handler, jobID, func(row fitJobListing) bool {
			return row.BestCost == best
		})

		if row.State != fitRunning {
			t.Fatalf("the followed run is %q while its trace is still growing, want running", row.State)
		}
	}

	// A followed run has no search in this process, so the stop control must
	// refuse it rather than mark it cancelled while it goes on writing.
	stop := httptest.NewRecorder()
	handler.ServeHTTP(stop, httptest.NewRequest(http.MethodPost, "/api/fit/cancel?job="+jobID, nil))

	if stop.Code != http.StatusConflict {
		t.Errorf("POST cancel of a followed run = %d, want 409: %s", stop.Code, stop.Body.String())
	}

	// The trace also reaches the client as the file it is, which is what the
	// cost curve is drawn from.
	served := httptest.NewRecorder()
	handler.ServeHTTP(served, httptest.NewRequest(http.MethodGet, "/api/fit/jobs/"+jobID+"/trace", nil))

	if served.Code != http.StatusOK {
		t.Errorf("GET the followed run's trace = %d, want 200: %s", served.Code, served.Body.String())
	}

	if err := trace.Close(); err != nil {
		t.Fatalf("close the trace: %v", err)
	}

	// result.json lands, and with it the run is over: the state is terminal
	// and the numbers are the summary's, not the last trace line's.
	writeRunResult(t, dir, 0.75)

	row = waitForListing(t, handler, jobID, func(row fitJobListing) bool {
		return row.State == fitSucceeded
	})

	if row.Score == nil || *row.Score != 0.75 {
		t.Fatalf("the finished run's score is %v, want the 0.75 result.json records", row.Score)
	}

	if row.BestCost != 0.75 {
		t.Errorf("the finished run's best cost is %v, want result.json's 0.75", row.BestCost)
	}

	if row.FinishedAt == nil {
		t.Error("the finished run has no finish time")
	}

	// It is still a run this server did not start, which is what keeps the
	// history honest about where a fit came from after it has ended.
	if !row.Followed {
		t.Error("the finished run stopped being marked as followed")
	}

	snapshot := followedSnapshot(t, handler, jobID)
	if snapshot.State != fitSucceeded || snapshot.StopReason != "budget" {
		t.Errorf("the finished run's snapshot is %+v, want the summary's own stop reason", snapshot)
	}
}

// TestAFollowedTraceIgnoresAPartialLine pins the tail's one real hazard. A
// trace is written by another process, so a read can land in the middle of a
// line; consuming that fragment would not merely lose one report, it would
// leave the offset inside a line and every line after it would be read as
// garbage.
func TestAFollowedTraceIgnoresAPartialLine(t *testing.T) {
	workDir := t.TempDir()

	const jobID = "fit-20240102T030405-0002"

	dir := filepath.Join(workDir, jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make the run directory: %v", err)
	}

	writeRunConfig(t, dir)

	trace := openTrace(t, dir)

	defer func() {
		_ = trace.Close()
	}()

	appendTraceLine(t, trace, 8)

	// Half a line, with no newline to end it: exactly what a reader sees
	// between a writer's two flushes.
	partial := `{"iteration":2,"optimizer_iterations":2,"restart":0,"lambda":0,"evaluations":40,` +
		`"elapsed_ms":200,"current":2.5,"best":2.5`
	if _, err := trace.WriteString(partial); err != nil {
		t.Fatalf("write the partial line: %v", err)
	}

	server := newFollowingServer(t, workDir)
	handler := server.Handler()

	waitForListing(t, handler, jobID, func(row fitJobListing) bool {
		return row.BestCost == 8
	})

	// Several ticks pass over the fragment. It must stay unread rather than
	// become a report of its own.
	time.Sleep(10 * followTestInterval)

	if row := listingFor(t, handler, jobID); row.BestCost != 8 {
		t.Fatalf("the partial line was read: best cost is %v, want the 8 of the last whole line", row.BestCost)
	}

	// Completing the line makes it a report, read from exactly where the
	// fragment started rather than from after it.
	if _, err := trace.WriteString("}\n"); err != nil {
		t.Fatalf("finish the partial line: %v", err)
	}

	row := waitForListing(t, handler, jobID, func(row fitJobListing) bool {
		return row.BestCost == 2.5
	})

	if row.evaluations != 40 {
		t.Errorf("the completed line was read as %+v, want the 40 evaluations it names", row)
	}
}

// followedListing is one history row plus the evaluation count the test reads
// from the job's own snapshot. The listing carries no such field, which is
// deliberate -- see fitJobListing -- so an assertion about what a trace line
// actually said has to reach the job endpoint for it.
type followedListing struct {
	fitJobListing

	evaluations int
}

// newFollowingServer builds a server over workDir whose scan runs on the test
// interval, and stops it when the test ends.
func newFollowingServer(t *testing.T, workDir string) *Server {
	t.Helper()

	server, err := New(Config{
		Addr:    "127.0.0.1:0",
		Version: "test-version",
		Static: fstest.MapFS{
			placeholderFileName: &fstest.MapFile{Data: []byte("<!doctype html><title>not built</title>")},
		},
		DistDir:         t.TempDir(),
		WorkDir:         workDir,
		Log:             io.Discard,
		ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	server.followInterval = followTestInterval

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// followRuns is what Run starts; the test drives it directly so that the
	// scan is the only thing running and no port has to be opened.
	go server.followRuns(ctx)

	t.Cleanup(server.Stop)

	return server
}

// writeRunConfig writes the config.json a run leaves before it starts. It is
// written by hand because the state under test is one no finished run can
// produce: a config with no result beside it.
func writeRunConfig(t *testing.T, dir string) {
	t.Helper()

	config := `{
	  "note": 72,
	  "velocity": 90,
	  "sample_rate": 8000,
	  "metric": "rms",
	  "engine": {"name": "mayfly"},
	  "reference": {"seconds": 0.2},
	  "started": "2024-01-02T03:04:05Z"
	}`

	if err := os.WriteFile(filepath.Join(dir, fitrun.FileConfig), []byte(config), 0o600); err != nil {
		t.Fatalf("write %s: %v", fitrun.FileConfig, err)
	}
}

// writeRunResult writes the result.json that ends a run.
func writeRunResult(t *testing.T, dir string, score float64) {
	t.Helper()

	summary := fitrun.Summary{
		Score:          score,
		Evaluations:    120,
		Iterations:     3,
		StopReason:     "budget",
		ElapsedSeconds: 0.3,
	}

	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("encode the summary: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, fitrun.FileResult), encoded, 0o600); err != nil {
		t.Fatalf("write %s: %v", fitrun.FileResult, err)
	}
}

// openTrace creates the trace the test appends to, as the run itself would.
func openTrace(t *testing.T, dir string) *os.File {
	t.Helper()

	trace, err := os.Create(filepath.Join(dir, fitrun.FileTrace))
	if err != nil {
		t.Fatalf("create %s: %v", fitrun.FileTrace, err)
	}

	return trace
}

// appendTraceLine writes one whole line in the shape fitrun's traceWriter
// writes, flushed as that writer flushes: per line, so a reader always finds
// whole lines.
func appendTraceLine(t *testing.T, trace *os.File, best float64) {
	t.Helper()

	line := fmt.Sprintf(
		`{"iteration":1,"optimizer_iterations":1,"restart":0,"lambda":0,"evaluations":20,`+
			`"elapsed_ms":100,"current":%v,"best":%v}`+"\n", best, best)

	if _, err := trace.WriteString(line); err != nil {
		t.Fatalf("append a trace line: %v", err)
	}
}

// waitForListing polls the history until one row satisfies want, or the test
// runs out of patience. The scan is a ticker, so every assertion about it is
// an assertion about what the server settles on rather than about what it has
// already done.
func waitForListing(t *testing.T, handler http.Handler, jobID string, want func(fitJobListing) bool) followedListing {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	var last followedListing

	for time.Now().Before(deadline) {
		last = listingFor(t, handler, jobID)
		if last.JobID == jobID && want(last.fitJobListing) {
			return last
		}

		time.Sleep(followTestInterval)
	}

	t.Fatalf("the history never settled on what was wanted for %s; last row %+v", jobID, last)

	return last
}

// listingFor reads one history row, and the evaluation count from the job's
// own snapshot. A row that is not there yet comes back zero.
func listingFor(t *testing.T, handler http.Handler, jobID string) followedListing {
	t.Helper()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/fit/jobs", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("GET the job list = %d: %s", response.Code, response.Body.String())
	}

	var list fitJobList
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode the job list: %v", err)
	}

	for _, row := range list.Jobs {
		if row.JobID != jobID {
			continue
		}

		return followedListing{
			fitJobListing: row,
			evaluations:   followedSnapshot(t, handler, jobID).Evaluations,
		}
	}

	return followedListing{}
}

// followedSnapshot reads one job's own status document.
func followedSnapshot(t *testing.T, handler http.Handler, jobID string) fitSnapshot {
	t.Helper()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/fit/jobs/"+jobID, nil))

	if response.Code != http.StatusOK {
		t.Fatalf("GET the job %s = %d: %s", jobID, response.Code, response.Body.String())
	}

	var snapshot fitSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode the job snapshot: %v", err)
	}

	return snapshot
}

// TestACampaignTreeIsDiscovered pins the descent. A campaign does not put its
// runs where a served fit does: internal/campaign writes them to
// jobs/bNN/<arm>, two levels below the root it is given, so a scan of
// immediate children finds one directory holding no config.json and reports an
// empty history for a tree full of runs.
//
// The nested id is asserted as well, and not for tidiness. A job id is one
// path segment of /api/fit/jobs/{id}, so a run listed under an id containing a
// separator would be listed and then answer 404 for everything about it.
func TestACampaignTreeIsDiscovered(t *testing.T) {
	workDir := t.TempDir()

	runDir := filepath.Join(workDir, "jobs", "b00", "mayfly-r16")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("create the campaign job directory: %v", err)
	}

	writeRunConfig(t, runDir)
	writeRunResult(t, runDir, 0.5)

	handler := newFollowingServer(t, workDir).Handler()

	const jobID = "jobs-b00-mayfly-r16"

	row := waitForListing(t, handler, jobID, func(row fitJobListing) bool {
		return row.State == fitSucceeded
	})

	if !row.Followed {
		t.Error("a campaign job is not marked as followed")
	}

	// The id has to address the run, not merely name it.
	snapshot := httptest.NewRecorder()
	handler.ServeHTTP(snapshot, httptest.NewRequest(http.MethodGet, "/api/fit/jobs/"+jobID, nil))

	if snapshot.Code != http.StatusOK {
		t.Errorf("GET the campaign job = %d, want 200: %s", snapshot.Code, snapshot.Body.String())
	}
}

// TestAResumedRunDirectoryIsReadAgain pins what a resume does to a directory
// the server has already adopted. `glockenspiel fit --resume` continues into
// the work directory it was given rather than making a new one, so the same
// path becomes a second run; a scan that remembered only that it had seen the
// name would leave the job frozen on the first run's result for as long as the
// server lived.
func TestAResumedRunDirectoryIsReadAgain(t *testing.T) {
	workDir := t.TempDir()

	runDir := filepath.Join(workDir, "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("create the run directory: %v", err)
	}

	writeRunConfig(t, runDir)
	writeRunResult(t, runDir, 0.75)

	handler := newFollowingServer(t, workDir).Handler()

	waitForListing(t, handler, "run", func(row fitJobListing) bool {
		return row.State == fitSucceeded && row.Score != nil && *row.Score == 0.75
	})

	// The resume: the result is taken away, the config rewritten, and the
	// search continues. The modification time is what the scan compares, so it
	// is moved forward explicitly rather than left to a filesystem whose
	// timestamps may not resolve two writes a millisecond apart.
	if err := os.Remove(filepath.Join(runDir, fitrun.FileResult)); err != nil {
		t.Fatalf("remove the result: %v", err)
	}

	writeRunConfig(t, runDir)

	later := time.Now().Add(time.Second)
	if err := os.Chtimes(filepath.Join(runDir, fitrun.FileConfig), later, later); err != nil {
		t.Fatalf("age the config: %v", err)
	}

	row := waitForListing(t, handler, "run", func(row fitJobListing) bool {
		return row.State == fitRunning
	})

	if row.Score != nil {
		t.Errorf("the resumed run still reports the first run's score %v", *row.Score)
	}

	// And it finishes on the second run's own result rather than the first's.
	writeRunResult(t, runDir, 0.25)

	waitForListing(t, handler, "run", func(row fitJobListing) bool {
		return row.State == fitSucceeded && row.Score != nil && *row.Score == 0.25
	})
}

// TestAFinishedFollowedRunReportsWhatTheBackendChose pins the second read of
// config.json. fitrun writes that file twice -- once before the search with an
// empty resolved block, once after it with the seed the backend drew and the
// width it sized to the machine -- and a job built from the first copy would
// otherwise end reporting zeros beside a file that records the real values.
func TestAFinishedFollowedRunReportsWhatTheBackendChose(t *testing.T) {
	workDir := t.TempDir()

	runDir := filepath.Join(workDir, "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("create the run directory: %v", err)
	}

	writeRunConfig(t, runDir)

	handler := newFollowingServer(t, workDir).Handler()

	waitForListing(t, handler, "run", func(row fitJobListing) bool {
		return row.State == fitRunning
	})

	// The config fitrun rewrites when the run ends, now carrying what the
	// backend resolved for itself.
	resolved := `{
	  "note": 72,
	  "velocity": 90,
	  "sample_rate": 8000,
	  "metric": "rms",
	  "engine": {"name": "mayfly"},
	  "reference": {"seconds": 0.2},
	  "started": "2024-01-02T03:04:05Z",
	  "resolved": {"seed": 4242, "workers": 7, "population": 10, "variant": "desma"}
	}`

	configPath := filepath.Join(runDir, fitrun.FileConfig)

	before, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat the config: %v", err)
	}

	if err := os.WriteFile(configPath, []byte(resolved), 0o600); err != nil {
		t.Fatalf("rewrite the config: %v", err)
	}

	// The modification time is put back, so that this test is about the second
	// read of config.json and nothing else. Leaving it to move would let the
	// scan notice the rewrite, adopt the directory again as if it were a
	// resumed run, and build a fresh job from the new file -- which produces
	// the right answer by a route TestAResumedRunDirectoryIsReadAgain already
	// covers, and would leave this test passing with the finish path still
	// reporting zeros.
	if err := os.Chtimes(configPath, before.ModTime(), before.ModTime()); err != nil {
		t.Fatalf("hold the config's timestamp: %v", err)
	}

	writeRunResult(t, runDir, 0.5)

	waitForListing(t, handler, "run", func(row fitJobListing) bool {
		return row.State == fitSucceeded
	})

	snapshot := followedSnapshot(t, handler, "run")

	if snapshot.Request.Seed != "4242" {
		t.Errorf("the finished followed run reports seed %q, want the resolved 4242", snapshot.Request.Seed)
	}

	if snapshot.Request.Workers != 7 {
		t.Errorf("the finished followed run reports %d workers, want the resolved 7", snapshot.Request.Workers)
	}
}

// TestAMalformedResultCountIsConsecutive pins what followedRun.malformed is
// documented to count. A result.json that exists and will not parse is usually
// a moment rather than a state -- fitrun writes it with a plain os.WriteFile,
// so a scan can land between the create and the write -- which is why several
// ticks have to fail before the run is called broken. The counter is therefore
// only meaningful if it starts again whenever a tick does not fail, and it did
// not: it only ever grew, so failures minutes or hours apart accumulated into
// a verdict that no single episode had earned.
func TestAMalformedResultCountIsConsecutive(t *testing.T) {
	dir := t.TempDir()

	job := syntheticJob("run", false)
	job.dir = dir
	job.followed = true

	follow := &followedRun{job: job, malformed: followedResultAttempts - 1}

	// No result.json at all: the run is simply not finished, and the count of
	// consecutive failures is back to none.
	if follow.finished() {
		t.Fatal("a run with no result.json was called finished")
	}

	if follow.malformed != 0 {
		t.Fatalf("malformed = %d after a tick that found no result.json, want 0", follow.malformed)
	}

	// One unparseable read is one failure, not the last of a tally that has
	// been running since the server started, so the run keeps going.
	if err := os.WriteFile(filepath.Join(dir, fitrun.FileResult), []byte("{ truncated"), 0o600); err != nil {
		t.Fatalf("write a half-written result: %v", err)
	}

	if follow.finished() {
		t.Fatal("a single unparseable result.json ended the run")
	}

	if follow.malformed != 1 {
		t.Fatalf("malformed = %d after one unparseable read, want 1", follow.malformed)
	}

	// It still gives up once the failures really are consecutive. The loop
	// stops at the first true, because that is the contract advanceFollowed
	// keeps: a run is retired the tick it reports itself finished, and asking
	// again afterwards would be asking a job that is already over.
	for range followedResultAttempts {
		if follow.finished() {
			break
		}
	}

	if job.state != fitFailed {
		t.Errorf("a result.json unreadable on every tick left the run %q, want failed", job.state)
	}
}
