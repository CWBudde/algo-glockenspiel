package server_test

// The queue and the job history. fit_test.go covers the endpoints that mean
// "the most recent job"; this file covers the ones that name a job, the order
// two starts run in, and what a restart makes of the run directories the last
// process left behind.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fitJobListing mirrors one row of the job history endpoint. It is spelled out
// here rather than exported, for the reason fitSnapshot is: a renamed wire
// field has to fail a test rather than quietly stop being found.
type fitJobListing struct {
	JobID      string     `json:"jobId"`
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
	BestCost   float64    `json:"bestCost"`
	Score      *float64   `json:"score"`
	Note       int        `json:"note"`
	Velocity   int        `json:"velocity"`
	Optimizer  string     `json:"optimizer"`
	Metric     string     `json:"metric"`
	Followed   bool       `json:"followed"`
}

type fitJobList struct {
	Jobs []fitJobListing `json:"jobs"`
}

// getFit performs one GET against the fit API and returns the recorder.
func getFit(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))

	return recorder
}

// postFit performs one POST against the fit API and returns the recorder.
func postFit(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, target, nil))

	return recorder
}

// jobSnapshot reads one job's state through the per-job endpoint.
func jobSnapshot(t *testing.T, handler http.Handler, jobID string) fitSnapshot {
	t.Helper()

	recorder := getFit(t, handler, "/api/fit/jobs/"+jobID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/fit/jobs/%s = %d: %s", jobID, recorder.Code, recorder.Body.String())
	}

	return decodeSnapshot(t, recorder.Body.Bytes())
}

// jobList reads the whole history.
func jobList(t *testing.T, handler http.Handler) fitJobList {
	t.Helper()

	recorder := getFit(t, handler, "/api/fit/jobs")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/fit/jobs = %d: %s", recorder.Code, recorder.Body.String())
	}

	var list fitJobList
	if err := json.Unmarshal(recorder.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode the job list from %q: %v", recorder.Body.String(), err)
	}

	return list
}

// waitForJob polls one job until it reaches a state it never leaves.
func waitForJob(t *testing.T, handler http.Handler, jobID string, timeout time.Duration) fitSnapshot {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snapshot := jobSnapshot(t, handler, jobID)
		if snapshot.State != "running" && snapshot.State != "queued" {
			return snapshot
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("fit %s was still going after %s", jobID, timeout)

	return fitSnapshot{}
}

// startQueued posts a start request and returns the accepted job's id.
func startQueued(t *testing.T, handler http.Handler, fields map[string]string) string {
	t.Helper()

	started := startFit(t, handler, fields)
	if started.Code != http.StatusAccepted {
		t.Fatalf("start = %d, want 202: %s", started.Code, started.Body.String())
	}

	return decodeSnapshot(t, started.Body.Bytes()).JobID
}

// Two starts in a row both run, and they run in the order they arrived. This
// is what replaced the 409: the work is not refused, it is lined up.
func TestTwoQueuedFitsBothRunInOrder(t *testing.T) {
	handler := newFitServer(t).Handler()

	first := startQueued(t, handler, shortFit())
	second := startQueued(t, handler, shortFit())

	firstFinal := waitForJob(t, handler, first, 60*time.Second)
	secondFinal := waitForJob(t, handler, second, 60*time.Second)

	if firstFinal.State != "succeeded" || secondFinal.State != "succeeded" {
		t.Fatalf("states = %q and %q, want both succeeded (errors %q, %q)",
			firstFinal.State, secondFinal.State, firstFinal.Error, secondFinal.Error)
	}

	if firstFinal.FinishedAt == nil || secondFinal.FinishedAt == nil {
		t.Fatal("a finished job reports no finish time")
	}

	// The second job's search started only once the first had ended, which is
	// the whole of "one fit at a time" as a client can observe it.
	if secondFinal.StartedAt.Before(*firstFinal.FinishedAt) {
		t.Fatalf("the second fit started at %s, before the first finished at %s",
			secondFinal.StartedAt, *firstFinal.FinishedAt)
	}
}

// A job cancelled while it is still waiting must never run at all. Anything
// else would make "cancel" mean "let it start first".
func TestCancellingAQueuedFitNeverRunsIt(t *testing.T) {
	srv := newFitServer(t)
	handler := srv.Handler()

	t.Cleanup(srv.Stop)

	running := startQueued(t, handler, endlessFit())
	queued := startQueued(t, handler, shortFit())

	if state := jobSnapshot(t, handler, queued).State; state != "queued" {
		t.Fatalf("the second job is %q, want queued", state)
	}

	cancelled := postCancel(t, handler, queued)
	if cancelled.Code != http.StatusOK {
		t.Fatalf("cancel of a queued job = %d, want 200: %s", cancelled.Code, cancelled.Body.String())
	}

	snapshot := decodeSnapshot(t, cancelled.Body.Bytes())
	if snapshot.State != "canceled" {
		t.Fatalf("the cancelled job is %q, want canceled", snapshot.State)
	}

	if snapshot.Evaluations != 0 || snapshot.HasPreset {
		t.Fatalf("a job cancelled while queued ran anyway: %d evaluations, hasPreset=%v",
			snapshot.Evaluations, snapshot.HasPreset)
	}

	if snapshot.StopReason != "canceled_while_queued" {
		t.Errorf("stop reason = %q, want canceled_while_queued", snapshot.StopReason)
	}

	// The job it was queued behind is untouched.
	if state := jobSnapshot(t, handler, running).State; state != "running" {
		t.Fatalf("the running job is %q, want running", state)
	}
}

// The history lists every fit, newest first.
func TestJobListReportsBothFitsNewestFirst(t *testing.T) {
	handler := newFitServer(t).Handler()

	first := startQueued(t, handler, shortFit())
	second := startQueued(t, handler, shortFit())

	waitForJob(t, handler, first, 60*time.Second)
	waitForJob(t, handler, second, 60*time.Second)

	list := jobList(t, handler)
	if len(list.Jobs) != 2 {
		t.Fatalf("the history holds %d jobs, want 2", len(list.Jobs))
	}

	if list.Jobs[0].JobID != second || list.Jobs[1].JobID != first {
		t.Fatalf("the history reads %s then %s, want %s then %s",
			list.Jobs[0].JobID, list.Jobs[1].JobID, second, first)
	}

	newest := list.Jobs[0]
	if newest.State != "succeeded" || newest.FinishedAt == nil {
		t.Fatalf("the newest row is %q, finished %v", newest.State, newest.FinishedAt)
	}

	// The score is what the run wrote to result.json, so it is there exactly
	// when the run shipped one -- and it is the cost the run finished on.
	if newest.Score == nil {
		t.Fatal("a finished row carries no score")
	}

	if *newest.Score != newest.BestCost {
		t.Errorf("score = %v, best cost = %v; a finished run's two numbers must agree",
			*newest.Score, newest.BestCost)
	}

	if newest.Optimizer != "simple" || newest.Metric == "" {
		t.Errorf("the row does not echo the request: optimizer %q, metric %q",
			newest.Optimizer, newest.Metric)
	}
}

// Every per-job endpoint answers for a finished fit, and every one of them
// says 404 for an id that names nothing.
func TestPerJobEndpointsAnswerAndRefuseAnUnknownID(t *testing.T) {
	handler := newFitServer(t).Handler()

	jobID := startQueued(t, handler, shortFit())

	final := waitForJob(t, handler, jobID, 60*time.Second)
	if final.State != "succeeded" {
		t.Fatalf("state = %q (error %q), want succeeded", final.State, final.Error)
	}

	preset := getFit(t, handler, "/api/fit/jobs/"+jobID+"/preset")
	if preset.Code != http.StatusOK {
		t.Fatalf("GET the job's preset = %d: %s", preset.Code, preset.Body.String())
	}

	if !strings.Contains(preset.Header().Get("Content-Disposition"), jobID) {
		t.Errorf("the preset download is not named after its job: %q",
			preset.Header().Get("Content-Disposition"))
	}

	// The audio is rendered on demand, which is why the duration query means
	// the same thing here as it does for the live job.
	audio := getFit(t, handler, "/api/fit/jobs/"+jobID+"/audio?duration=0.1")
	if audio.Code != http.StatusOK {
		t.Fatalf("GET the job's audio = %d: %s", audio.Code, audio.Body.String())
	}

	if got := audio.Header().Get("Content-Type"); got != "audio/wav" {
		t.Errorf("the render is served as %q, want audio/wav", got)
	}

	trace := getFit(t, handler, "/api/fit/jobs/"+jobID+"/trace")
	if trace.Code != http.StatusOK {
		t.Fatalf("GET the job's trace = %d: %s", trace.Code, trace.Body.String())
	}

	if got := trace.Header().Get("Content-Type"); got != "application/x-ndjson" {
		t.Errorf("the trace is served as %q, want application/x-ndjson", got)
	}

	if !strings.Contains(trace.Body.String(), "{") {
		t.Errorf("the trace is empty: %q", trace.Body.String())
	}

	for _, suffix := range []string{"", "/preset", "/audio", "/trace"} {
		target := "/api/fit/jobs/fit-19700101T000000-0001" + suffix

		if response := getFit(t, handler, target); response.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404: %s", target, response.Code, response.Body.String())
		}
	}
}

// An id that is trying to be a path is refused before anything is looked up,
// let alone opened. It is the fit API's half of TestDistRefusesEscapingPaths.
func TestJobIDsThatAreNotJobIDsAreRefused(t *testing.T) {
	handler := newFitServer(t).Handler()

	// Percent-encoded, because net/http normalises the plain forms out of the
	// path before a handler ever sees them -- and the encoded ones survive
	// that pass and reappear once the wildcard has been unescaped.
	targets := []string{
		"/api/fit/jobs/%2e%2e",
		"/api/fit/jobs/%2e%2e%2fetc",
		"/api/fit/jobs/%2e%2e%5cetc",
		"/api/fit/jobs/..%2fetc/preset",
		"/api/fit/jobs/a%2fb/trace",
	}

	for _, target := range targets {
		response := getFit(t, handler, target)
		if response.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400: %s", target, response.Code, response.Body.String())
		}
	}
}

// A run directory outlives the process that wrote it, so a server started over
// the same work directory finds the fits the last one ran.
func TestASecondServerSeesTheFirstOnesJobs(t *testing.T) {
	workDir := t.TempDir()

	first := newFitServerIn(t, workDir).Handler()

	jobID := startQueued(t, first, shortFit())

	final := waitForJob(t, first, jobID, 60*time.Second)
	if final.State != "succeeded" {
		t.Fatalf("state = %q (error %q), want succeeded", final.State, final.Error)
	}

	second := newFitServerIn(t, workDir).Handler()

	list := jobList(t, second)
	if len(list.Jobs) != 1 || list.Jobs[0].JobID != jobID {
		t.Fatalf("the restarted server's history is %+v, want just %s", list.Jobs, jobID)
	}

	restored := jobSnapshot(t, second, jobID)
	if restored.State != "succeeded" {
		t.Fatalf("the restored job is %q, want succeeded", restored.State)
	}

	if restored.BestCost != final.BestCost {
		t.Errorf("the restored best cost is %v, want %v", restored.BestCost, final.BestCost)
	}

	if restored.Note != final.Note || restored.Optimizer != final.Optimizer || restored.Metric != final.Metric {
		t.Errorf("the restored job does not echo the request: %+v against %+v", restored, final)
	}

	// The preset outlived the process too, which is what makes a restored job
	// worth having at all: it is read back from the run directory rather than
	// from a result this process never computed.
	preset := getFit(t, second, "/api/fit/jobs/"+jobID+"/preset")
	if preset.Code != http.StatusOK {
		t.Fatalf("GET the restored preset = %d: %s", preset.Code, preset.Body.String())
	}

	audio := getFit(t, second, "/api/fit/jobs/"+jobID+"/audio?duration=0.1")
	if audio.Code != http.StatusOK {
		t.Fatalf("GET the restored audio = %d: %s", audio.Code, audio.Body.String())
	}
}

// A directory holding a config.json and no result.json is a fit that has not
// finished. It comes back as running, and as a run this server did not start,
// which is what the periodic scan buys: the server keeps looking, so it does
// not have to decide from one glance whether the run is alive.
//
// This test used to assert the opposite -- that such a directory came back as
// failed once its config.json was old enough -- and it used to have a sibling,
// TestARecentlyStartedRunIsNotYetRestoredAsFailed, asserting that a young one
// was left out of the history entirely. Both premises are retired with
// restoreFreshnessWindow, and they were two halves of the same guess: the age
// of config.json is not evidence about a process. A run that really did die
// with its process now stays running here until somebody removes its
// directory, which is the trade Phase 8.8 makes deliberately.
func TestAHalfWrittenRunDirectoryComesBackAsFollowed(t *testing.T) {
	workDir := t.TempDir()

	const jobID = "fit-20240102T030405-0001"

	dir := filepath.Join(workDir, jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make the run directory: %v", err)
	}

	// The fields a rebuilt job reads, in the shape fitrun writes them. The
	// file is written by hand because the state under test is one a finished
	// run cannot produce: config.json exists and result.json has not arrived.
	config := `{
	  "note": 72,
	  "velocity": 90,
	  "sample_rate": 8000,
	  "metric": "rms",
	  "engine": {"name": "mayfly"},
	  "reference": {"seconds": 0.2},
	  "started": "2024-01-02T03:04:05Z"
	}`

	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	// Backdated by an hour, which used to be the whole question and is now
	// beside the point: how long ago a run started says nothing about whether
	// it is still going, and a long fit is exactly the one worth watching.
	stale := time.Now().Add(-time.Hour)
	if err := os.Chtimes(configPath, stale, stale); err != nil {
		t.Fatalf("backdate config.json: %v", err)
	}

	// A directory that is not a run at all is not a job either.
	if err := os.MkdirAll(filepath.Join(workDir, "not-a-run"), 0o755); err != nil {
		t.Fatalf("make the stray directory: %v", err)
	}

	handler := newFitServerIn(t, workDir).Handler()

	list := jobList(t, handler)
	if len(list.Jobs) != 1 || list.Jobs[0].JobID != jobID {
		t.Fatalf("the history is %+v, want just %s", list.Jobs, jobID)
	}

	if !list.Jobs[0].Followed {
		t.Error("a run this server did not start is not marked as followed in the history")
	}

	snapshot := jobSnapshot(t, handler, jobID)
	if snapshot.State != "running" {
		t.Fatalf("the half-written run is %q, want running", snapshot.State)
	}

	if !snapshot.Followed {
		t.Error("the half-written run is not marked as followed")
	}

	// It echoes the request it was started with, which is the point of reading
	// config.json rather than merely noticing the directory.
	if snapshot.Note != 72 || snapshot.Velocity != 90 || snapshot.Optimizer != "mayfly" {
		t.Errorf("the rebuilt job does not echo its config: %+v", snapshot)
	}

	// Nothing in this process owns the search, so the stop control refuses it
	// rather than marking a run cancelled that will go on writing.
	stop := postFit(t, handler, "/api/fit/cancel?job="+jobID)
	if stop.Code != http.StatusConflict {
		t.Errorf("POST cancel of a followed run = %d, want 409: %s", stop.Code, stop.Body.String())
	}

	// There is no preset, so the endpoints that would serve one say so rather
	// than reading a file that is not there.
	if response := getFit(t, handler, "/api/fit/jobs/"+jobID+"/preset"); response.Code != http.StatusConflict {
		t.Errorf("GET the preset of an unfinished run = %d, want 409: %s", response.Code, response.Body.String())
	}

	if response := getFit(t, handler, "/api/fit/jobs/"+jobID+"/trace"); response.Code != http.StatusNotFound {
		t.Errorf("GET the trace of an unfinished run = %d, want 404: %s", response.Code, response.Body.String())
	}
}
