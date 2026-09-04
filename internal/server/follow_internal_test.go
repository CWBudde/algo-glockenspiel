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
