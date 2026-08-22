package server_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cwbudde/glockenspiel/internal/wavio"
)

// wavAtDeclaredRate rewrites the sample rate a canonical WAV header states,
// without touching a sample. A header is 44 bytes with the rate at offset 24
// and the byte rate -- which a decoder may cross-check -- at offset 28.
//
// This is what an upload is: bytes whose header says whatever the sender chose.
func wavAtDeclaredRate(t *testing.T, declared uint32) []byte {
	t.Helper()

	encoded := referenceWAV(t, testReferenceLength, testSampleRate)

	binary.LittleEndian.PutUint32(encoded[24:28], declared)
	binary.LittleEndian.PutUint32(encoded[28:32], declared*2)

	return encoded
}

// A WAV header's sample rate is an unsigned 32-bit number that nothing
// downstream questions: it becomes the job's rate, and the audition endpoint
// sizes its render as duration * rate. A one-second upload declaring two
// gigahertz therefore asks for 1.2e11 float32 samples on the next
// /api/fit/audio?duration=60 -- a 480 GB allocation, which Go reports as
// "fatal error: out of memory" and cannot recover from. The upload is the only
// place to stop it.
func TestAReferenceWithAnUnusableSampleRateIsRefused(t *testing.T) {
	for name, declared := range map[string]uint32{
		"two gigahertz":   2_000_000_000,
		"above the cap":   1_000_000,
		"below the floor": 100,
	} {
		t.Run(name, func(t *testing.T) {
			// A server each: a refusal that nevertheless started a job would
			// otherwise show up as the next case's 409 rather than as its own
			// failure.
			handler := newFitServer(t).Handler()

			response := startFitWithReference(t, handler, wavAtDeclaredRate(t, declared), shortFit())
			if response.Code != http.StatusBadRequest {
				t.Fatalf("start with a %d Hz reference = %d, want 400: %s",
					declared, response.Code, response.Body.String())
			}

			if !bytes.Contains(response.Body.Bytes(), []byte("sample rate")) {
				t.Fatalf("the refusal does not mention the sample rate: %s", response.Body.String())
			}

			// Nothing may have been started by it.
			status := httptest.NewRecorder()
			handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/fit", nil))

			if status.Code != http.StatusNotFound {
				t.Fatalf("GET /api/fit after the refusal = %d, want 404 -- the slot was claimed", status.Code)
			}
		})
	}
}

// The render cap has to hold for the request that does not name a duration as
// well. The upload limit allows several minutes of audio, so a reference longer
// than the cap would otherwise render in full on a plain GET -- the very render
// the cap exists to refuse.
func TestTheDefaultRenderDurationHonoursTheCap(t *testing.T) {
	const referenceSeconds = 75.0

	handler := newFitServer(t).Handler()

	fields := shortFit()
	// Alignment correlates over the whole reference, which is pure cost for a
	// test about the render length.
	fields["align"] = "false"
	fields["maxIterations"] = "1"

	reference := referenceWAV(t, referenceSeconds, testSampleRate)

	if response := startFitWithReference(t, handler, reference, fields); response.Code != http.StatusAccepted {
		t.Fatalf("start = %d: %s", response.Code, response.Body.String())
	}

	final := waitForTerminalState(t, handler, 120*time.Second)
	if final.State != "succeeded" {
		t.Fatalf("state = %q (error %q), want succeeded", final.State, final.Error)
	}

	if final.ReferenceSeconds < referenceSeconds-1 {
		t.Fatalf("reference length = %g, want about %g", final.ReferenceSeconds, referenceSeconds)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/fit/audio", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/fit/audio = %d: %s", recorder.Code, recorder.Body.String())
	}

	samples, _, err := wavio.DecodeMono(bytes.NewReader(recorder.Body.Bytes()), "default render")
	if err != nil {
		t.Fatalf("decode render: %v", err)
	}

	if want := 60 * testSampleRate; len(samples) != want {
		t.Fatalf("the default render is %d samples, want it capped at %d", len(samples), want)
	}
}

// strconv.ParseFloat accepts "NaN", and every comparison against NaN is false,
// so a range check alone lets it through -- into a render that quietly produces
// an empty file and answers 200.
func TestNonFiniteQueryAndFormValuesAreRefused(t *testing.T) {
	handler := newFitServer(t).Handler()

	for _, budget := range []string{"NaN", "Inf", "-Inf"} {
		fields := shortFit()
		fields["timeBudget"] = budget

		response := startFit(t, handler, fields)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("start with timeBudget=%s = %d, want 400: %s", budget, response.Code, response.Body.String())
		}
	}

	if response := startFit(t, handler, shortFit()); response.Code != http.StatusAccepted {
		t.Fatalf("start = %d: %s", response.Code, response.Body.String())
	}

	waitForTerminalState(t, handler, 60*time.Second)

	for _, query := range []string{"?duration=NaN", "?duration=nan", "?duration=Inf", "?duration=-Inf"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/fit/audio"+query, nil))

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/fit/audio%s = %d, want 400: %s", query, recorder.Code, recorder.Body.String())
		}
	}
}

// A name the optimizer cannot resolve is a bad request, not a job. Accepting it
// would claim the single fit slot for a run whose only remaining act is to
// fail.
func TestAnUnknownMayflyVariantIsRefusedBeforeAJobStarts(t *testing.T) {
	handler := newFitServer(t).Handler()

	fields := shortFit()
	fields["optimizer"] = "mayfly"
	fields["mayflyVariant"] = "unknown"

	response := startFit(t, handler, fields)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("start with mayflyVariant=unknown = %d, want 400: %s", response.Code, response.Body.String())
	}

	if !bytes.Contains(response.Body.Bytes(), []byte("variant")) {
		t.Fatalf("the refusal does not name the variant: %s", response.Body.String())
	}

	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/fit", nil))

	if status.Code != http.StatusNotFound {
		t.Fatalf("GET /api/fit after the refusal = %d, want 404 -- the slot was claimed", status.Code)
	}
}

// The terminal snapshot has to state the optimizer's own final numbers.
// reportEvery=0 is an accepted setting and turns the progress callback off
// entirely, so a job whose metrics came only from those callbacks would report
// a finished run that evaluated nothing.
func TestTheTerminalSnapshotCarriesTheOptimizersFinalMetrics(t *testing.T) {
	handler := newFitServer(t).Handler()

	fields := shortFit()
	fields["reportEvery"] = "0"

	if response := startFit(t, handler, fields); response.Code != http.StatusAccepted {
		t.Fatalf("start = %d: %s", response.Code, response.Body.String())
	}

	final := waitForTerminalState(t, handler, 60*time.Second)
	if final.State != "succeeded" {
		t.Fatalf("state = %q (error %q), want succeeded", final.State, final.Error)
	}

	if final.Iteration != 0 {
		t.Fatalf("iteration = %d, want 0: reportEvery=0 means no progress callbacks", final.Iteration)
	}

	if final.OptimizerIterations == 0 {
		t.Fatal("the finished fit reports zero optimizer iterations")
	}

	if final.Evaluations == 0 {
		t.Fatal("the finished fit reports zero objective evaluations")
	}

	if final.BestCost <= 0 {
		t.Fatalf("best cost = %g, want the optimizer's own figure", final.BestCost)
	}

	if final.StopReason == "" {
		t.Fatal("the finished fit reports no stop reason")
	}
}

// HEAD is admitted by the read gate, and Go suppresses the body for it without
// stopping the handler -- so a HEAD probe that entered the SSE loop would sit
// there until the fit ended, up to the whole time budget, holding a connection
// that shutdown then waits for.
func TestHeadOnTheEventStreamReturnsImmediately(t *testing.T) {
	srv := newFitServer(t)
	handler := srv.Handler()

	t.Cleanup(srv.Stop)

	if response := startFit(t, handler, endlessFit()); response.Code != http.StatusAccepted {
		t.Fatalf("start = %d: %s", response.Code, response.Body.String())
	}

	returned := make(chan *httptest.ResponseRecorder, 1)

	go func() {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodHead, "/api/fit/events", nil))

		returned <- recorder
	}()

	select {
	case recorder := <-returned:
		if recorder.Code != http.StatusOK {
			t.Fatalf("HEAD /api/fit/events = %d, want 200", recorder.Code)
		}

		if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
			t.Fatalf("content type = %q, want text/event-stream", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("HEAD /api/fit/events did not return while a fit was running")
	}
}

// presetCopy dereferences the job's result, which is nil until a fit produces
// one. It is nil-safe because preset.Clone is: a nil receiver clones to nil,
// which is what makes the check in fittedPreset the one that answers. This
// pins that, so a Clone that grew a dereference would fail here rather than
// panic in a handler.
func TestReadingThePresetOfAFreshJobIsNilSafeRatherThanAPanic(t *testing.T) {
	srv := newFitServer(t)
	handler := srv.Handler()

	t.Cleanup(srv.Stop)

	if response := startFit(t, handler, endlessFit()); response.Code != http.StatusAccepted {
		t.Fatalf("start = %d: %s", response.Code, response.Body.String())
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/fit/preset", nil))

	if recorder.Code != http.StatusConflict {
		t.Fatalf("GET /api/fit/preset before any preset = %d, want 409", recorder.Code)
	}

	var body struct {
		Error string `json:"error"`
	}

	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the refusal: %v", err)
	}

	if body.Error == "" {
		t.Fatal("the refusal carries no message")
	}
}
