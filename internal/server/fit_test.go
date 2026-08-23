package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/server"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
)

// fitSnapshot mirrors the JSON the fit endpoints answer with. It is spelled out
// again here rather than exported from the package, so that a field rename in
// the server is a failing test rather than a silently renamed wire field that
// every existing client stops finding.
type fitSnapshot struct {
	JobID               string  `json:"jobId"`
	State               string  `json:"state"`
	Iteration           int     `json:"iteration"`
	OptimizerIterations int     `json:"optimizerIterations"`
	Evaluations         int     `json:"evaluations"`
	CurrentCost         float64 `json:"currentCost"`
	BestCost            float64 `json:"bestCost"`
	ElapsedMS           int64   `json:"elapsedMs"`
	StopReason          string  `json:"stopReason"`
	Error               string  `json:"error"`
	SampleRate          int     `json:"sampleRate"`
	ReferenceSeconds    float64 `json:"referenceSeconds"`
	Note                int     `json:"note"`
	Velocity            int     `json:"velocity"`
	Optimizer           string  `json:"optimizer"`
	Metric              string  `json:"metric"`
	HasPreset           bool    `json:"hasPreset"`

	MayflyVariant        string `json:"mayflyVariant"`
	MayflySeed           string `json:"mayflySeed"`
	MayflyRecommendation string `json:"mayflyRecommendation"`
}

// The reference used throughout. 8000 Hz keeps a render cheap while staying a
// rate the synthesizer treats normally, and a fifth of a second is long enough
// that the onset alignment has something to correlate against.
const (
	testSampleRate      = 8000
	testReferenceLength = 0.2
)

// shortFit is a run that finishes on its own within a test's patience: five
// Nelder-Mead iterations over a fifth of a second of audio.
func shortFit() map[string]string {
	return map[string]string{
		"optimizer":     "simple",
		"maxIterations": "5",
		"reportEvery":   "1",
		"timeBudget":    "20s",
	}
}

// endlessFit is a run that does not stop until it is told to. Mayfly with a
// population evaluates population*2 candidates per iteration and has no
// convergence criterion that fires on a budget this large, so "still running"
// is a property of the configuration rather than a hope about timing.
func endlessFit() map[string]string {
	return map[string]string{
		"optimizer":        "mayfly",
		"mayflyPopulation": "12",
		"maxIterations":    "100000",
		"reportEvery":      "1",
		"timeBudget":       "30m",
	}
}

// shortMayflyFit is a mayfly run that finishes on its own within a test's
// patience. The population is large enough that mayfly's default mutant count
// -- five percent of it, rounded -- is at least one, which is what makes a
// written offspring count of zero an error the API can be seen to reject.
func shortMayflyFit() map[string]string {
	return map[string]string{
		"optimizer":        "mayfly",
		"mayflyPopulation": "20",
		"maxIterations":    "2",
		"reportEvery":      "1",
		"timeBudget":       "60s",
	}
}

func newFitServer(t *testing.T) *server.Server {
	t.Helper()

	srv, err := server.New(server.Config{
		Addr:            "127.0.0.1:0",
		Version:         "test-version",
		Static:          testTree(),
		DistDir:         t.TempDir(),
		Log:             io.Discard,
		ShutdownTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	return srv
}

// referenceWAV builds a plausible struck-bar reference: two decaying partials,
// so the objective has structure to fit rather than a constant.
func referenceWAV(t *testing.T, seconds float64, sampleRate int) []byte {
	t.Helper()

	count := int(seconds * float64(sampleRate))
	samples := make([]float32, count)

	for i := range samples {
		seconds := float64(i) / float64(sampleRate)
		value := 0.5*math.Sin(2*math.Pi*880*seconds)*math.Exp(-3*seconds) +
			0.2*math.Sin(2*math.Pi*2637*seconds)*math.Exp(-8*seconds)
		samples[i] = float32(value)
	}

	encoded, err := wavio.MarshalMono(sampleRate, samples)
	if err != nil {
		t.Fatalf("encode reference: %v", err)
	}

	return encoded
}

// multipartFit assembles a start request body.
func multipartFit(t *testing.T, reference []byte, fields map[string]string) (io.Reader, string) {
	t.Helper()

	return multipartFitWithFiles(t, reference, fields, nil)
}

// multipartFitWithFiles assembles a start request body carrying further file
// parts -- the optional starting preset and the optional bounds -- alongside
// the reference.
func multipartFitWithFiles(
	t *testing.T,
	reference []byte,
	fields map[string]string,
	files map[string][]byte,
) (io.Reader, string) {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	if reference != nil {
		// The part filename is deliberately hostile: nothing in the server may
		// use it, and a traversal attempt reaching a path would show up as a
		// file written outside the test's temporary directory.
		part, err := writer.CreateFormFile("reference", `../../../etc/passwd`)
		if err != nil {
			t.Fatalf("create reference part: %v", err)
		}

		if _, err := part.Write(reference); err != nil {
			t.Fatalf("write reference part: %v", err)
		}
	}

	for name, content := range files {
		// Hostile filenames here too: the bounds and the starting preset are
		// read from bytes, and neither part's filename may reach a path.
		part, err := writer.CreateFormFile(name, `../../../etc/`+name+`.json`)
		if err != nil {
			t.Fatalf("create %s part: %v", name, err)
		}

		if _, err := part.Write(content); err != nil {
			t.Fatalf("write %s part: %v", name, err)
		}
	}

	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatalf("write field %s: %v", name, err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	return body, writer.FormDataContentType()
}

// startFit posts a start request against handler and returns the response.
func startFit(t *testing.T, handler http.Handler, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	return startFitWithReference(t, handler, referenceWAV(t, testReferenceLength, testSampleRate), fields)
}

func startFitWithReference(t *testing.T, handler http.Handler, reference []byte, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	return startFitWithFiles(t, handler, reference, fields, nil)
}

// startFitWithFiles posts a start request carrying extra file parts.
func startFitWithFiles(
	t *testing.T,
	handler http.Handler,
	reference []byte,
	fields map[string]string,
	files map[string][]byte,
) *httptest.ResponseRecorder {
	t.Helper()

	body, contentType := multipartFitWithFiles(t, reference, fields, files)

	request := httptest.NewRequest(http.MethodPost, "/api/fit/start", body)
	request.Header.Set("Content-Type", contentType)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	return recorder
}

func decodeSnapshot(t *testing.T, body []byte) fitSnapshot {
	t.Helper()

	var snapshot fitSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		t.Fatalf("decode snapshot from %q: %v", body, err)
	}

	return snapshot
}

func fitStatus(t *testing.T, handler http.Handler) fitSnapshot {
	t.Helper()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/fit", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/fit = %d: %s", recorder.Code, recorder.Body.String())
	}

	return decodeSnapshot(t, recorder.Body.Bytes())
}

// waitForTerminalState polls the status endpoint until the job stops running.
func waitForTerminalState(t *testing.T, handler http.Handler, timeout time.Duration) fitSnapshot {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snapshot := fitStatus(t, handler)
		if snapshot.State != "running" {
			return snapshot
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("fit was still running after %s", timeout)

	return fitSnapshot{}
}

// A fit run end to end: start, watch it finish, read the preset back, hear it.
func TestFitRunsAndProducesAPresetAndAudio(t *testing.T) {
	handler := newFitServer(t).Handler()

	response := startFit(t, handler, shortFit())
	if response.Code != http.StatusAccepted {
		t.Fatalf("start = %d, want 202: %s", response.Code, response.Body.String())
	}

	accepted := decodeSnapshot(t, response.Body.Bytes())
	if accepted.JobID == "" {
		t.Fatal("the start response carries no job id")
	}

	if accepted.SampleRate != testSampleRate {
		t.Fatalf("sample rate = %d, want the reference's %d", accepted.SampleRate, testSampleRate)
	}

	if math.Abs(accepted.ReferenceSeconds-testReferenceLength) > 1e-6 {
		t.Fatalf("reference length = %g, want %g", accepted.ReferenceSeconds, testReferenceLength)
	}

	final := waitForTerminalState(t, handler, 60*time.Second)
	if final.State != "succeeded" {
		t.Fatalf("state = %q (error %q), want succeeded", final.State, final.Error)
	}

	if final.JobID != accepted.JobID {
		t.Fatalf("job id changed from %q to %q", accepted.JobID, final.JobID)
	}

	if !final.HasPreset {
		t.Fatal("a succeeded fit reports no preset")
	}

	if final.Evaluations == 0 {
		t.Fatal("the fit reports zero objective evaluations, so nothing was searched")
	}

	if final.StopReason == "" {
		t.Fatal("the fit reports no stop reason")
	}

	// The preset must be a real, loadable preset -- not merely well-formed
	// JSON. preset.Decode is the same validation a file on disk goes through.
	presetResponse := httptest.NewRecorder()
	handler.ServeHTTP(presetResponse, httptest.NewRequest(http.MethodGet, "/api/fit/preset", nil))

	if presetResponse.Code != http.StatusOK {
		t.Fatalf("GET /api/fit/preset = %d: %s", presetResponse.Code, presetResponse.Body.String())
	}

	fitted, err := preset.Decode(presetResponse.Body.Bytes(), "the fitted preset")
	if err != nil {
		t.Fatalf("the fitted preset does not validate: %v", err)
	}

	if len(fitted.Parameters.Modes) == 0 {
		t.Fatal("the fitted preset has no modes")
	}

	// And it must render. The audio endpoint's default duration is the
	// reference's length, which is what makes the two directly comparable.
	audioResponse := httptest.NewRecorder()
	handler.ServeHTTP(audioResponse, httptest.NewRequest(http.MethodGet, "/api/fit/audio", nil))

	if audioResponse.Code != http.StatusOK {
		t.Fatalf("GET /api/fit/audio = %d: %s", audioResponse.Code, audioResponse.Body.String())
	}

	if got := audioResponse.Header().Get("Content-Type"); got != "audio/wav" {
		t.Fatalf("content type = %q, want audio/wav", got)
	}

	samples, sampleRate, err := wavio.DecodeMono(bytes.NewReader(audioResponse.Body.Bytes()), "rendered fit")
	if err != nil {
		t.Fatalf("the rendered audio does not decode: %v", err)
	}

	if sampleRate != testSampleRate {
		t.Fatalf("rendered sample rate = %d, want %d", sampleRate, testSampleRate)
	}

	wantSamples := int(testReferenceLength * testSampleRate)
	if samples := len(samples); samples != wantSamples {
		t.Fatalf("rendered %d samples, want the reference's %d", samples, wantSamples)
	}
}

// A second start while a fit runs must be refused rather than served: there is
// one slot, and quietly replacing the running job would lose it.
func TestSecondStartIsRefusedWhileAFitRuns(t *testing.T) {
	srv := newFitServer(t)
	handler := srv.Handler()

	t.Cleanup(srv.Stop)

	first := startFit(t, handler, endlessFit())
	if first.Code != http.StatusAccepted {
		t.Fatalf("first start = %d: %s", first.Code, first.Body.String())
	}

	firstID := decodeSnapshot(t, first.Body.Bytes()).JobID

	second := startFit(t, handler, endlessFit())
	if second.Code != http.StatusConflict {
		t.Fatalf("second start = %d, want 409: %s", second.Code, second.Body.String())
	}

	// The refusal must not have disturbed the running job.
	if current := fitStatus(t, handler); current.JobID != firstID || current.State != "running" {
		t.Fatalf("after the refused start the job is %s/%s, want %s/running",
			current.JobID, current.State, firstID)
	}
}

// Cancel must stop the search, keep what it found, and leave the slot free by
// the time it answers -- so that a cancel-then-start sequence works without the
// client having to poll and retry.
func TestCancelStopsTheFitAndFreesTheSlotImmediately(t *testing.T) {
	handler := newFitServer(t).Handler()

	started := startFit(t, handler, endlessFit())
	if started.Code != http.StatusAccepted {
		t.Fatalf("start = %d: %s", started.Code, started.Body.String())
	}

	firstID := decodeSnapshot(t, started.Body.Bytes()).JobID

	// Let the search produce at least one report, so the "a cancelled run keeps
	// its best result" half of this is actually exercised.
	waitForFirstReport(t, handler)

	cancelled := postCancel(t, handler, "")
	if cancelled.Code != http.StatusOK {
		t.Fatalf("cancel = %d, want 200: %s", cancelled.Code, cancelled.Body.String())
	}

	snapshot := decodeSnapshot(t, cancelled.Body.Bytes())
	if snapshot.State != "canceled" {
		t.Fatalf("state = %q, want canceled; cancel answered before the job stopped", snapshot.State)
	}

	if !snapshot.HasPreset {
		t.Fatal("a cancelled fit dropped the best parameters it had found")
	}

	// No polling in between: the slot has to be free the instant cancel returns.
	restarted := startFit(t, handler, shortFit())
	if restarted.Code != http.StatusAccepted {
		t.Fatalf("start after cancel = %d, want 202: %s", restarted.Code, restarted.Body.String())
	}

	if secondID := decodeSnapshot(t, restarted.Body.Bytes()).JobID; secondID == firstID {
		t.Fatalf("the restarted fit reuses the job id %q", secondID)
	}

	waitForTerminalState(t, handler, 60*time.Second)
}

// Cancelling by id must not kill a job that started after the client decided to
// cancel. Without the check, a client watching job N and reacting slowly would
// silently stop job N+1.
func TestCancelRefusesAStaleJobID(t *testing.T) {
	srv := newFitServer(t)
	handler := srv.Handler()

	t.Cleanup(srv.Stop)

	if response := startFit(t, handler, endlessFit()); response.Code != http.StatusAccepted {
		t.Fatalf("start = %d: %s", response.Code, response.Body.String())
	}

	response := postCancel(t, handler, "fit-does-not-exist")
	if response.Code != http.StatusConflict {
		t.Fatalf("cancel with a stale id = %d, want 409: %s", response.Code, response.Body.String())
	}

	if current := fitStatus(t, handler); current.State != "running" {
		t.Fatalf("the refused cancel stopped the job anyway: state = %q", current.State)
	}
}

func postCancel(t *testing.T, handler http.Handler, jobID string) *httptest.ResponseRecorder {
	t.Helper()

	target := "/api/fit/cancel"
	if jobID != "" {
		target += "?job=" + jobID
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, target, nil))

	return recorder
}

// waitForFirstReport blocks until the optimizer has reported progress at least
// once, so a test that depends on there being a best result does not race the
// first callback.
func waitForFirstReport(t *testing.T, handler http.Handler) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if fitStatus(t, handler).Evaluations > 0 {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatal("the optimizer never reported progress")
}

// The read endpoints must say "nothing here" rather than 500 or an empty 200
// before anything has been started.
func TestReadEndpointsBeforeAnyFit(t *testing.T) {
	handler := newFitServer(t).Handler()

	for _, target := range []string{"/api/fit", "/api/fit/preset", "/api/fit/audio", "/api/fit/events"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))

		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s before any fit = %d, want 404", target, recorder.Code)
		}
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/fit/cancel", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("cancel before any fit = %d, want 404", recorder.Code)
	}
}

// The preset and audio endpoints exist while a fit is running but have nothing
// to answer with until the run ends; that is a 409, not a 404 and not a 500.
func TestPresetIsUnavailableUntilTheFitEnds(t *testing.T) {
	srv := newFitServer(t)
	handler := srv.Handler()

	t.Cleanup(srv.Stop)

	if response := startFit(t, handler, endlessFit()); response.Code != http.StatusAccepted {
		t.Fatalf("start = %d: %s", response.Code, response.Body.String())
	}

	for _, target := range []string{"/api/fit/preset", "/api/fit/audio"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))

		if recorder.Code != http.StatusConflict {
			t.Fatalf("GET %s during a run = %d, want 409: %s", target, recorder.Code, recorder.Body.String())
		}
	}
}

// The method gates. The write endpoints must accept POST and nothing else, and
// -- the point of having a second gate at all -- the read endpoints and the
// whole static tree must still refuse POST.
func TestFitMethodGates(t *testing.T) {
	handler := newFitServer(t).Handler()

	tests := []struct {
		target    string
		method    string
		wantAllow string
	}{
		{target: "/api/fit/start", method: http.MethodGet, wantAllow: "POST"},
		{target: "/api/fit/start", method: http.MethodDelete, wantAllow: "POST"},
		{target: "/api/fit/cancel", method: http.MethodGet, wantAllow: "POST"},
		{target: "/api/fit", method: http.MethodPost, wantAllow: "GET, HEAD"},
		{target: "/api/fit/preset", method: http.MethodPost, wantAllow: "GET, HEAD"},
		{target: "/api/fit/audio", method: http.MethodPut, wantAllow: "GET, HEAD"},
		{target: "/api/fit/events", method: http.MethodPost, wantAllow: "GET, HEAD"},
		{target: "/", method: http.MethodPost, wantAllow: "GET, HEAD"},
		{target: "/api/version", method: http.MethodPost, wantAllow: "GET, HEAD"},
	}

	for _, testCase := range tests {
		t.Run(testCase.method+" "+testCase.target, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(testCase.method, testCase.target, nil))

			if recorder.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", recorder.Code)
			}

			if got := recorder.Header().Get("Allow"); got != testCase.wantAllow {
				t.Fatalf("Allow = %q, want %q", got, testCase.wantAllow)
			}
		})
	}
}

// An unknown path under the API must stay inside the API rather than falling
// through to the static handler.
func TestUnknownFitSubpathIs404(t *testing.T) {
	handler := newFitServer(t).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/fit/nonsense", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

// Everything a start request can get wrong, answered as a 4xx with a reason
// rather than as a job that dies immediately or a 500.
func TestStartRejectsBadRequests(t *testing.T) {
	handler := newFitServer(t).Handler()
	reference := referenceWAV(t, testReferenceLength, testSampleRate)

	tests := []struct {
		name       string
		reference  []byte
		fields     map[string]string
		bounds     []byte
		wantStatus int
		wantText   string
	}{
		{
			name:       "no reference part",
			reference:  nil,
			fields:     shortFit(),
			wantStatus: http.StatusBadRequest,
			wantText:   "reference",
		},
		{
			name:       "reference is not a wav",
			reference:  []byte("this is a text file wearing a wav name"),
			fields:     shortFit(),
			wantStatus: http.StatusBadRequest,
			wantText:   "valid wav",
		},
		{
			name:       "unknown metric",
			reference:  reference,
			fields:     map[string]string{"metric": "vibes"},
			wantStatus: http.StatusBadRequest,
			wantText:   "vibes",
		},
		{
			name:       "unknown optimizer",
			reference:  reference,
			fields:     map[string]string{"optimizer": "annealing"},
			wantStatus: http.StatusBadRequest,
			wantText:   "annealing",
		},
		{
			name:       "note out of range",
			reference:  reference,
			fields:     map[string]string{"note": "512"},
			wantStatus: http.StatusBadRequest,
			wantText:   "note",
		},
		{
			name:       "note is not a number",
			reference:  reference,
			fields:     map[string]string{"note": "middle c"},
			wantStatus: http.StatusBadRequest,
			wantText:   "note",
		},
		{
			name:       "velocity out of range",
			reference:  reference,
			fields:     map[string]string{"velocity": "-1"},
			wantStatus: http.StatusBadRequest,
			wantText:   "velocity",
		},
		{
			name:       "iteration budget above the cap",
			reference:  reference,
			fields:     map[string]string{"maxIterations": "100000000"},
			wantStatus: http.StatusBadRequest,
			wantText:   "maxIterations",
		},
		{
			name:       "zero iterations",
			reference:  reference,
			fields:     map[string]string{"maxIterations": "0"},
			wantStatus: http.StatusBadRequest,
			wantText:   "maxIterations",
		},
		{
			name:       "time budget above the cap",
			reference:  reference,
			fields:     map[string]string{"timeBudget": "48h"},
			wantStatus: http.StatusBadRequest,
			wantText:   "timeBudget",
		},
		{
			name:       "unparseable time budget",
			reference:  reference,
			fields:     map[string]string{"timeBudget": "soon"},
			wantStatus: http.StatusBadRequest,
			wantText:   "timeBudget",
		},
		{
			name:       "mayfly population below two",
			reference:  reference,
			fields:     map[string]string{"optimizer": "mayfly", "mayflyPopulation": "1"},
			wantStatus: http.StatusBadRequest,
			wantText:   "mayflyPopulation",
		},
		{
			name:       "align is not a boolean",
			reference:  reference,
			fields:     map[string]string{"align": "sometimes"},
			wantStatus: http.StatusBadRequest,
			wantText:   "align",
		},
		{
			name:       "malformed bounds",
			reference:  reference,
			fields:     shortFit(),
			bounds:     []byte(`{"decay_ms": [50.0`),
			wantStatus: http.StatusBadRequest,
			wantText:   "decode bounds",
		},
		{
			name:       "bounds with an unknown key",
			reference:  reference,
			fields:     shortFit(),
			bounds:     []byte(`{"decay_millis": [50.0, 400.0]}`),
			wantStatus: http.StatusBadRequest,
			wantText:   "decode bounds",
		},
		{
			name:       "inverted bounds range",
			reference:  reference,
			fields:     shortFit(),
			bounds:     []byte(`{"decay_ms": [400.0, 50.0]}`),
			wantStatus: http.StatusBadRequest,
			wantText:   "must be below max",
		},
		{
			// Well-formed and ordered, but the model's own floor for the
			// dimension is above zero, and a log-encoded dimension could not
			// start there anyway. Rejected before a job slot is claimed.
			name:       "bounds a codec cannot encode",
			reference:  reference,
			fields:     shortFit(),
			bounds:     []byte(`{"decay_ms": [0.0, 400.0]}`),
			wantStatus: http.StatusBadRequest,
			wantText:   "decay_ms",
		},
		{
			// Ordered and finite, but no candidate inside it survives
			// model.ValidateBarParams, so the fit could only burn its budget on
			// +Inf scores. Rejected up front instead.
			name:       "bounds outside the model domain",
			reference:  reference,
			fields:     shortFit(),
			bounds:     []byte(`{"input_mix": [3.0, 4.0]}`),
			wantStatus: http.StatusBadRequest,
			wantText:   "leaves the model range",
		},
		{
			// Only the first object would be applied, so the fit would run
			// against constraints the client never asked for.
			name:       "bounds followed by a second document",
			reference:  reference,
			fields:     shortFit(),
			bounds:     []byte(`{"decay_ms": [50.0, 400.0]}{"decay_ms": [1.0, 2.0]}`),
			wantStatus: http.StatusBadRequest,
			wantText:   "unexpected content after the bounds object",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var files map[string][]byte
			if testCase.bounds != nil {
				files = map[string][]byte{"bounds": testCase.bounds}
			}

			response := startFitWithFiles(t, handler, testCase.reference, testCase.fields, files)
			if response.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d: %s", response.Code, testCase.wantStatus, response.Body.String())
			}

			if !strings.Contains(response.Body.String(), testCase.wantText) {
				t.Fatalf("body %q does not name %q", response.Body.String(), testCase.wantText)
			}

			// Nothing may have been started; every one of these is rejected
			// before a job slot is claimed.
			status := httptest.NewRecorder()
			handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/fit", nil))

			if status.Code != http.StatusNotFound {
				t.Fatalf("a rejected request left a job behind: GET /api/fit = %d", status.Code)
			}
		})
	}
}

// Bounds that arrive with the request are a hard constraint on the fitted
// preset, exactly as `fit --bounds` is on the command line. The box below is
// deliberately one the embedded starting preset violates -- its shortest mode
// decays in well under a millisecond and its amplitudes sit at +/-2 -- so a run
// that ignored the field, or that widened the box to contain the template,
// would produce a preset outside it.
func TestFitHonorsSuppliedBounds(t *testing.T) {
	handler := newFitServer(t).Handler()

	const (
		minDecayMs   = 50.0
		maxDecayMs   = 400.0
		minAmplitude = -1.0
		maxAmplitude = 1.0
		minBaseHz    = 435.0
		maxBaseHz    = 445.0
		// Encoding and decoding a bound is a round trip through a logarithm,
		// so a value sitting on a boundary comes back a few ulps outside it.
		tolerance = 1e-6
	)

	bounds := []byte(`{"base_frequency": [435.0, 445.0], "decay_ms": [50.0, 400.0], "amplitude": [-1.0, 1.0]}`)

	response := startFitWithFiles(t, handler, referenceWAV(t, testReferenceLength, testSampleRate), shortFit(),
		map[string][]byte{"bounds": bounds})
	if response.Code != http.StatusAccepted {
		t.Fatalf("start = %d, want 202: %s", response.Code, response.Body.String())
	}

	final := waitForTerminalState(t, handler, 60*time.Second)
	if final.State != "succeeded" {
		t.Fatalf("state = %q (error %q), want succeeded", final.State, final.Error)
	}

	presetResponse := httptest.NewRecorder()
	handler.ServeHTTP(presetResponse, httptest.NewRequest(http.MethodGet, "/api/fit/preset", nil))

	if presetResponse.Code != http.StatusOK {
		t.Fatalf("GET /api/fit/preset = %d: %s", presetResponse.Code, presetResponse.Body.String())
	}

	fitted, err := preset.Decode(presetResponse.Body.Bytes(), "the fitted preset")
	if err != nil {
		t.Fatalf("the fitted preset does not validate: %v", err)
	}

	base := fitted.Parameters.BaseFrequency
	if base < minBaseHz-tolerance || base > maxBaseHz+tolerance {
		t.Fatalf("fitted base frequency %g is outside the requested [%g,%g]", base, minBaseHz, maxBaseHz)
	}

	for i, mode := range fitted.Parameters.Modes {
		if mode.DecayMs < minDecayMs-tolerance || mode.DecayMs > maxDecayMs+tolerance {
			t.Fatalf("mode %d decays in %g ms, outside the requested [%g,%g]", i, mode.DecayMs, minDecayMs, maxDecayMs)
		}

		if mode.Amplitude < minAmplitude-tolerance || mode.Amplitude > maxAmplitude+tolerance {
			t.Fatalf("mode %d amplitude %g is outside the requested [%g,%g]",
				i, mode.Amplitude, minAmplitude, maxAmplitude)
		}
	}
}

// Without the field the default box applies, and the default box is widened to
// contain the starting preset -- so the very parameters the test above pins
// inside a narrow range are free to sit outside it.
func TestFitWithoutBoundsKeepsTheDefaultBox(t *testing.T) {
	handler := newFitServer(t).Handler()

	response := startFit(t, handler, shortFit())
	if response.Code != http.StatusAccepted {
		t.Fatalf("start = %d, want 202: %s", response.Code, response.Body.String())
	}

	final := waitForTerminalState(t, handler, 60*time.Second)
	if final.State != "succeeded" {
		t.Fatalf("state = %q (error %q), want succeeded", final.State, final.Error)
	}

	presetResponse := httptest.NewRecorder()
	handler.ServeHTTP(presetResponse, httptest.NewRequest(http.MethodGet, "/api/fit/preset", nil))

	fitted, err := preset.Decode(presetResponse.Body.Bytes(), "the fitted preset")
	if err != nil {
		t.Fatalf("the fitted preset does not validate: %v", err)
	}

	fastest := math.Inf(1)
	for _, mode := range fitted.Parameters.Modes {
		fastest = math.Min(fastest, mode.DecayMs)
	}

	if fastest >= 50.0 {
		t.Fatalf("the default box no longer admits the template's %g ms mode; "+
			"TestFitHonorsSuppliedBounds proves nothing", fastest)
	}
}

// A start request that is not a multipart form at all.
func TestStartRejectsANonMultipartBody(t *testing.T) {
	handler := newFitServer(t).Handler()

	request := httptest.NewRequest(http.MethodPost, "/api/fit/start", strings.NewReader(`{"note":69}`))
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
}

// The upload limit has to be enforced by the reader, not by a length check
// after the fact: a body larger than the limit must be refused without the
// server having buffered all of it.
func TestOversizedReferenceIsRefused(t *testing.T) {
	const limit = 64 << 10

	srv, err := server.New(server.Config{
		Addr:              "127.0.0.1:0",
		Version:           "test-version",
		Static:            testTree(),
		DistDir:           t.TempDir(),
		Log:               io.Discard,
		MaxReferenceBytes: limit,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	handler := srv.Handler()

	// Comfortably above limit + the multipart overhead allowance.
	oversized := referenceWAV(t, 8, 44100)
	if len(oversized) <= limit {
		t.Fatalf("the oversized reference is only %d bytes, which is under the %d byte limit", len(oversized), limit)
	}

	response := startFitWithReference(t, handler, oversized, shortFit())
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", response.Code, response.Body.String())
	}

	// A reference under the limit still works, so the limit is a limit and not
	// a blanket refusal.
	small := referenceWAV(t, testReferenceLength, testSampleRate)
	if len(small) >= limit {
		t.Fatalf("the small reference is %d bytes, which is not under the %d byte limit", len(small), limit)
	}

	if accepted := startFitWithReference(t, handler, small, shortFit()); accepted.Code != http.StatusAccepted {
		t.Fatalf("a reference under the limit was refused: %d %s", accepted.Code, accepted.Body.String())
	}

	waitForTerminalState(t, handler, 60*time.Second)
}

// The audio endpoint takes three numbers from the query string, and each of
// them has to be bounded: rendering is linear in duration and the whole file is
// held in memory before it is sent.
func TestRenderRejectsOutOfRangeQueries(t *testing.T) {
	handler := newFitServer(t).Handler()

	if response := startFit(t, handler, shortFit()); response.Code != http.StatusAccepted {
		t.Fatalf("start = %d: %s", response.Code, response.Body.String())
	}

	waitForTerminalState(t, handler, 60*time.Second)

	for _, query := range []string{
		"?note=128", "?note=-1", "?note=A4",
		"?velocity=200", "?velocity=loud",
		"?duration=0", "?duration=-1", "?duration=100000", "?duration=forever",
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/fit/audio"+query, nil))

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/fit/audio%s = %d, want 400", query, recorder.Code)
		}
	}

	// A render whose length is asked for explicitly must be that length.
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/fit/audio?duration=0.05&note=72&velocity=90", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}

	samples, _, err := wavio.DecodeMono(bytes.NewReader(recorder.Body.Bytes()), "explicit render")
	if err != nil {
		t.Fatalf("decode render: %v", err)
	}

	if want := int(0.05 * testSampleRate); len(samples) != want {
		t.Fatalf("rendered %d samples, want %d", len(samples), want)
	}
}

// The SSE stream carrying live progress, over a real connection. The events are
// fed from optimizer.Progress -- the same callback the CLI hangs checkpointing
// off -- so this is also the test that the reuse actually reports anything.
func TestEventStreamCarriesLiveProgressAndEndsWithTheJob(t *testing.T) {
	srv := newFitServer(t)
	httpServer := httptest.NewServer(srv.Handler())

	t.Cleanup(httpServer.Close)
	t.Cleanup(srv.Stop)

	jobID := postStart(t, httpServer.URL, endlessFit())

	stream := openEventStream(t, httpServer.URL)
	defer stream.close()

	// The opening event states the current position, so a client that attaches
	// mid-run draws something immediately.
	first := stream.next(t, 30*time.Second)
	if first.name != "progress" {
		t.Fatalf("first event is %q, want progress", first.name)
	}

	if decodeSnapshot(t, []byte(first.data)).JobID != jobID {
		t.Fatalf("the stream reports a different job than the one started")
	}

	// Two further reports prove the stream is fed by the optimizer rather than
	// only by the handler's opening snapshot, and that the cost is moving.
	var advanced bool

	for range 2 {
		event := stream.next(t, 60*time.Second)
		if event.name != "progress" {
			t.Fatalf("event is %q, want progress; the endless fit ended early", event.name)
		}

		if decodeSnapshot(t, []byte(event.data)).Evaluations > 0 {
			advanced = true
		}
	}

	if !advanced {
		t.Fatal("no streamed event reported any objective evaluation")
	}

	// Cancel, and the stream must terminate rather than hang on a job that will
	// never report again.
	cancelled, err := http.Post(httpServer.URL+"/api/fit/cancel", "", nil)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	_, _ = io.Copy(io.Discard, cancelled.Body)
	_ = cancelled.Body.Close()

	terminal := stream.until(t, "done", 60*time.Second)
	if state := decodeSnapshot(t, []byte(terminal.data)).State; state != "canceled" {
		t.Fatalf("terminal state = %q, want canceled", state)
	}

	// And the server must close it, so the connection stops being an active one.
	stream.expectClosed(t, 10*time.Second)
}

// A stream opened after the job has already finished must terminate on its own
// rather than block on a channel nothing will ever signal again.
func TestEventStreamOpenedAfterTheFitEndsFinishesImmediately(t *testing.T) {
	srv := newFitServer(t)
	httpServer := httptest.NewServer(srv.Handler())

	t.Cleanup(httpServer.Close)

	postStart(t, httpServer.URL, shortFit())
	waitForTerminalState(t, srv.Handler(), 60*time.Second)

	stream := openEventStream(t, httpServer.URL)
	defer stream.close()

	event := stream.next(t, 10*time.Second)
	if event.name != "done" {
		t.Fatalf("first event is %q, want done", event.name)
	}

	if state := decodeSnapshot(t, []byte(event.data)).State; state != "succeeded" {
		t.Fatalf("state = %q, want succeeded", state)
	}

	stream.expectClosed(t, 10*time.Second)
}

// postStart starts a fit against a real server and returns the job id.
func postStart(t *testing.T, base string, fields map[string]string) string {
	t.Helper()

	body, contentType := multipartFit(t, referenceWAV(t, testReferenceLength, testSampleRate), fields)

	response, err := http.Post(base+"/api/fit/start", contentType, body)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	payload, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()

	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("start = %d: %s", response.StatusCode, payload)
	}

	return decodeSnapshot(t, payload).JobID
}

// This is the interaction the design has to get right. http.Server.Shutdown
// waits for active connections, and an SSE response is an active connection
// forever, so a stream left open would spend the whole ShutdownTimeout on every
// Ctrl-C. Run closes the streams before it calls Shutdown; this asserts that it
// works, by giving the server a generous timeout and requiring it to finish in
// a small fraction of it.
func TestOpenEventStreamDoesNotDelayShutdown(t *testing.T) {
	const shutdownTimeout = 10 * time.Second

	log := &syncBuffer{}

	srv, err := server.New(server.Config{
		Addr:            "127.0.0.1:0",
		Version:         "test-version",
		Static:          testTree(),
		DistDir:         t.TempDir(),
		Log:             log,
		ShutdownTimeout: shutdownTimeout,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		done <- srv.Run(ctx)
	}()

	address := waitForListenAddress(t, log)
	base := "http://" + address

	// A fit that never ends on its own, so the stream stays open because the
	// job is live rather than because nothing happened yet.
	body, contentType := multipartFit(t, referenceWAV(t, testReferenceLength, testSampleRate), endlessFit())

	started, err := http.Post(base+"/api/fit/start", contentType, body)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	_, _ = io.Copy(io.Discard, started.Body)
	_ = started.Body.Close()

	if started.StatusCode != http.StatusAccepted {
		t.Fatalf("start = %d", started.StatusCode)
	}

	stream, err := http.Get(base + "/api/fit/events")
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}

	defer func() {
		_ = stream.Body.Close()
	}()

	// Read the opening event, so the connection is unambiguously established
	// and counted as active by the time the shutdown starts.
	reader := bufio.NewReader(stream.Body)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read the opening event: %v", err)
	}

	begun := time.Now()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(shutdownTimeout + 5*time.Second):
		t.Fatal("Run never returned; the open stream blocked the shutdown entirely")
	}

	elapsed := time.Since(begun)
	// A tenth of the timeout. If the stream were not closed the shutdown would
	// spend the full ten seconds waiting for it, so the margin here is between
	// "prompt" and "hit the timeout", not a tight timing assertion.
	if limit := shutdownTimeout / 10; elapsed > limit {
		t.Fatalf("shutdown took %s, which is over the %s that says the stream was closed rather than waited out",
			elapsed, limit)
	}
}

// The job manager is shared mutable state reached from every request goroutine
// and from the goroutine running the fit. Hammer all of it at once; the
// assertion this test carries is the race detector's, so it is only meaningful
// under -race.
func TestConcurrentAccessIsRaceClean(t *testing.T) {
	srv := newFitServer(t)
	httpServer := httptest.NewServer(srv.Handler())

	t.Cleanup(httpServer.Close)

	reference := referenceWAV(t, testReferenceLength, testSampleRate)

	body, contentType := multipartFit(t, reference, endlessFit())

	started, err := http.Post(httpServer.URL+"/api/fit/start", contentType, body)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	_, _ = io.Copy(io.Discard, started.Body)
	_ = started.Body.Close()

	var waiting sync.WaitGroup

	stop := make(chan struct{})

	// Readers: status, preset and audio, all racing the reporting goroutine.
	for range 4 {
		waiting.Add(1)

		go func() {
			defer waiting.Done()

			for {
				select {
				case <-stop:
					return
				default:
				}

				for _, target := range []string{"/api/fit", "/api/fit/preset", "/api/fit/audio"} {
					response, err := http.Get(httpServer.URL + target)
					if err != nil {
						return
					}

					_, _ = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
				}
			}
		}()
	}

	// Subscribers coming and going, which is what exercises the subscriber map
	// against notifyLocked.
	for range 3 {
		waiting.Add(1)

		go func() {
			defer waiting.Done()

			for {
				select {
				case <-stop:
					return
				default:
				}

				response, err := http.Get(httpServer.URL + "/api/fit/events")
				if err != nil {
					return
				}

				buffer := make([]byte, 256)
				_, _ = response.Body.Read(buffer)
				_ = response.Body.Close()
			}
		}()
	}

	// Start requests that must all be refused, racing the one that is running.
	for range 2 {
		waiting.Add(1)

		go func() {
			defer waiting.Done()

			for {
				select {
				case <-stop:
					return
				default:
				}

				attempt, contentType := multipartFit(t, reference, endlessFit())

				response, err := http.Post(httpServer.URL+"/api/fit/start", contentType, attempt)
				if err != nil {
					return
				}

				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
			}
		}()
	}

	time.Sleep(250 * time.Millisecond)

	cancelled, err := http.Post(httpServer.URL+"/api/fit/cancel", "", nil)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	cancelledBody, _ := io.ReadAll(cancelled.Body)
	_ = cancelled.Body.Close()

	close(stop)
	waiting.Wait()

	// The cancel is allowed to answer 200 or -- if one of the racing readers
	// happened to be mid-flight -- to report a state that is already terminal.
	// What it may not do is leave the job running.
	if cancelled.StatusCode != http.StatusOK {
		t.Fatalf("cancel = %d: %s", cancelled.StatusCode, cancelledBody)
	}

	if state := decodeSnapshot(t, cancelledBody).State; state == "running" {
		t.Fatalf("cancel returned with the job still running")
	}
}

// sseEvent is one parsed `event:`/`data:` pair.
type sseEvent struct {
	name string
	data string
}

// eventStream parses an SSE response incrementally on its own goroutine, so a
// test can assert on events as they arrive rather than only on a stream that
// has already ended -- which for a live progress stream would never happen.
type eventStream struct {
	events chan sseEvent
	body   io.ReadCloser
}

func openEventStream(t *testing.T, base string) *eventStream {
	t.Helper()

	response, err := http.Get(base + "/api/fit/events")
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}

	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		t.Fatalf("GET /api/fit/events = %d", response.StatusCode)
	}

	if got := response.Header.Get("Content-Type"); got != "text/event-stream" {
		_ = response.Body.Close()

		t.Fatalf("content type = %q, want text/event-stream", got)
	}

	stream := &eventStream{events: make(chan sseEvent, 64), body: response.Body}

	go stream.read()

	return stream
}

// read turns lines into events until the server closes the connection. A
// heartbeat comment is not an event and is skipped, which is what an
// EventSource does too.
func (s *eventStream) read() {
	defer close(s.events)

	var pending sseEvent

	scanner := bufio.NewScanner(s.body)
	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "event: "):
			pending.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			pending.data = strings.TrimPrefix(line, "data: ")
		case line == "":
			if pending.name != "" {
				s.events <- pending

				pending = sseEvent{}
			}
		}
	}
}

// next returns the next event, failing the test if none arrives in time or if
// the stream ended.
func (s *eventStream) next(t *testing.T, timeout time.Duration) sseEvent {
	t.Helper()

	select {
	case event, ok := <-s.events:
		if !ok {
			t.Fatal("the stream ended while an event was expected")
		}

		return event
	case <-time.After(timeout):
		t.Fatalf("no event arrived within %s", timeout)

		return sseEvent{}
	}
}

// until reads past events until one with the wanted name arrives.
func (s *eventStream) until(t *testing.T, name string, timeout time.Duration) sseEvent {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		event := s.next(t, time.Until(deadline))
		if event.name == name {
			return event
		}
	}

	t.Fatalf("no %q event arrived within %s", name, timeout)

	return sseEvent{}
}

// expectClosed requires the server to have ended the response. An SSE stream
// the server leaves open is exactly the thing that makes a graceful shutdown
// hang, so "it terminated" is an assertion worth making explicitly.
func (s *eventStream) expectClosed(t *testing.T, timeout time.Duration) {
	t.Helper()

	for {
		select {
		case _, ok := <-s.events:
			if !ok {
				return
			}
		case <-time.After(timeout):
			t.Fatalf("the server did not close the stream within %s", timeout)

			return
		}
	}
}

func (s *eventStream) close() {
	_ = s.body.Close()
}
