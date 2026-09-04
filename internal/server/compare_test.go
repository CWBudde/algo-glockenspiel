package server_test

// The comparison payload and the reference the A/B auditions against.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/server"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
)

// compareLength is long enough that the coarse analysis frame fits more times
// over than the frame count the test asks for: at the test rate a fifth of a
// second is fewer samples than one frame, and a signal with fewer frames than
// were asked for keeps what it has, which would make the resolution the test
// is checking unreachable.
const compareLength = 2.0

// fitCompare mirrors the comparison payload, re-spelled here for the reason
// the snapshot is: a renamed field must fail a test rather than vanish from
// every client at once.
type fitCompare struct {
	SampleRate int      `json:"sampleRate"`
	Seconds    float64  `json:"seconds"`
	Columns    int      `json:"columns"`
	Frames     int      `json:"frames"`
	FloorDB    *float64 `json:"floorDb"`

	Reference fitCompareSide `json:"reference"`
	Render    fitCompareSide `json:"render"`
}

type fitCompareSide struct {
	Samples int `json:"samples"`

	Waveform struct {
		Columns int       `json:"columns"`
		Min     []float64 `json:"min"`
		Max     []float64 `json:"max"`
	} `json:"waveform"`

	Spectrogram *struct {
		Frames    int         `json:"frames"`
		Bins      int         `json:"bins"`
		FrameSize int         `json:"frameSize"`
		Hop       int         `json:"hop"`
		PeakDB    float64     `json:"peakDb"`
		MaxHz     float64     `json:"maxHz"`
		DB        [][]float64 `json:"db"`
	} `json:"spectrogram"`
}

// finishedFitFor runs one fit to its end and returns its job id.
func finishedFitFor(t *testing.T, handler http.Handler, seconds float64) string {
	t.Helper()

	response := startFitWithReference(t, handler,
		referenceWAV(t, seconds, testSampleRate), shortFit())
	if response.Code != http.StatusAccepted {
		t.Fatalf("start = %d: %s", response.Code, response.Body.String())
	}

	snapshot := waitForTerminalState(t, handler, 60*time.Second)
	if snapshot.State != "succeeded" {
		t.Fatalf("the fit ended as %q: %s", snapshot.State, snapshot.Error)
	}

	return snapshot.JobID
}

// getCompare asks for one job's comparison at a resolution.
func getCompare(t *testing.T, handler http.Handler, id, query string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/fit/jobs/%s/compare%s", id, query), nil))

	return recorder
}

func TestTheComparisonHoldsBothSignalsAtTheRequestedResolution(t *testing.T) {
	handler := newFitServer(t).Handler()
	id := finishedFitFor(t, handler, compareLength)

	recorder := getCompare(t, handler, id, "?columns=64&frames=8")
	if recorder.Code != http.StatusOK {
		t.Fatalf("compare = %d: %s", recorder.Code, recorder.Body.String())
	}

	var payload fitCompare
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode the comparison: %v", err)
	}

	if payload.SampleRate != testSampleRate {
		t.Fatalf("the comparison is at %d Hz, want %d", payload.SampleRate, testSampleRate)
	}

	for name, side := range map[string]fitCompareSide{
		"reference": payload.Reference,
		"render":    payload.Render,
	} {
		// Both sides are drawn on one axis, so a difference in either
		// resolution would put the two signals at different scales.
		if side.Waveform.Columns != 64 {
			t.Fatalf("the %s waveform has %d columns, want the 64 asked for", name, side.Waveform.Columns)
		}

		if len(side.Waveform.Min) != 64 || len(side.Waveform.Max) != 64 {
			t.Fatalf("the %s envelope is %d low and %d high values, want 64 of each",
				name, len(side.Waveform.Min), len(side.Waveform.Max))
		}

		if side.Samples == 0 {
			t.Fatalf("the %s side is %d samples", name, side.Samples)
		}

		if side.Spectrogram == nil {
			t.Fatalf("the %s side carries no spectrogram", name)
		}

		if side.Spectrogram.Frames != 8 || len(side.Spectrogram.DB) != 8 {
			t.Fatalf("the %s spectrogram has %d frames and %d rows, want the 8 asked for",
				name, side.Spectrogram.Frames, len(side.Spectrogram.DB))
		}

		if side.Spectrogram.MaxHz != testSampleRate/2 {
			t.Fatalf("the %s spectrogram tops out at %v Hz, want the Nyquist rate", name, side.Spectrogram.MaxHz)
		}

		for _, row := range side.Spectrogram.DB {
			if len(row) != side.Spectrogram.Bins {
				t.Fatalf("a %s spectrogram row is %d values, want %d", name, len(row), side.Spectrogram.Bins)
			}
		}

		if payload.FloorDB == nil {
			t.Fatalf("the %s side carries a spectrogram but the comparison has no floor", name)
		}

		if side.Spectrogram.PeakDB < *payload.FloorDB {
			t.Fatalf("the %s spectrogram peaks at %v, below the shared floor of %v",
				name, side.Spectrogram.PeakDB, *payload.FloorDB)
		}
	}

	// One time axis, so one span: both sides cover it and the payload states
	// it once.
	if payload.Seconds < compareLength-0.01 || payload.Seconds > compareLength+0.01 {
		t.Fatalf("the comparison spans %v seconds, want %v", payload.Seconds, compareLength)
	}

	if payload.Reference.Samples != payload.Render.Samples {
		t.Fatalf("the two sides are %d and %d samples, which is not one axis",
			payload.Reference.Samples, payload.Render.Samples)
	}

	// The two are the same rate by construction: the render is made at the
	// rate the reference declared, which is the rate the fit was scored at.
	// Comparing signals at two rates would put the render's partials at the
	// wrong place on a shared frequency axis.
	if payload.Reference.Spectrogram.Bins != payload.Render.Spectrogram.Bins {
		t.Fatalf("the two spectrograms have %d and %d bins",
			payload.Reference.Spectrogram.Bins, payload.Render.Spectrogram.Bins)
	}
}

// The payload's size has to follow what was asked for and not how long the
// reference is, so the resolutions are bounded rather than trusted.
func TestTheComparisonRefusesAResolutionPastItsCap(t *testing.T) {
	handler := newFitServer(t).Handler()
	id := finishedFitFor(t, handler, testReferenceLength)

	for _, query := range []string{"?columns=100000", "?frames=100000", "?columns=0", "?columns=x"} {
		recorder := getCompare(t, handler, id, query)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("compare%s = %d, want 400", query, recorder.Code)
		}
	}
}

// A signal shorter than one analysis frame is one the objective measures no
// spectral term for, and the payload says so by leaving the spectrogram out
// rather than sending an empty picture.
func TestAReferenceShorterThanAFrameHasNoSpectrogram(t *testing.T) {
	handler := newFitServer(t).Handler()
	id := finishedFitFor(t, handler, testReferenceLength)

	recorder := getCompare(t, handler, id, "?columns=32&frames=4")
	if recorder.Code != http.StatusOK {
		t.Fatalf("compare = %d: %s", recorder.Code, recorder.Body.String())
	}

	var payload fitCompare
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode the comparison: %v", err)
	}

	if payload.Reference.Spectrogram != nil {
		t.Fatalf("a %v second reference at %d Hz produced a spectrogram",
			testReferenceLength, testSampleRate)
	}

	if payload.Reference.Waveform.Columns != 32 {
		t.Fatalf("the waveform is %d columns, want the 32 asked for", payload.Reference.Waveform.Columns)
	}
}

// The A/B auditions against the signal the objective scored, not the upload:
// the difference a listener hears has to be the fit's, not the loader's.
func TestTheReferenceEndpointServesTheSignalTheFitScored(t *testing.T) {
	handler := newFitServer(t).Handler()
	id := finishedFitFor(t, handler, testReferenceLength)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		"/api/fit/jobs/"+id+"/reference", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("reference = %d: %s", recorder.Code, recorder.Body.String())
	}

	if got := recorder.Header().Get("Content-Type"); got != "audio/wav" {
		t.Fatalf("the reference is served as %q, want audio/wav", got)
	}

	if !bytes.HasPrefix(recorder.Body.Bytes(), []byte("RIFF")) {
		t.Fatalf("the reference does not begin with a RIFF header: %q", recorder.Body.Bytes()[:4])
	}
}

// Finding 3 of the whole-phase review: restoreJob adopts any directory under
// the work directory that carries a config.json, with no bound on what its
// reference.wav holds, so the upload limit that bounds a live job's own
// reference does nothing for one rebuilt from disk. The reference is
// rewritten past the configured cap here, exactly as
// TestALongReferenceIsCutToTheSpanTheRenderCovers rewrites it past the render
// cap, to exercise that path without an actual oversized upload.
func TestTheComparisonRefusesAReferenceOverTheByteCap(t *testing.T) {
	const limit = 64 << 10

	workDir := t.TempDir()

	srv, err := server.New(server.Config{
		Addr:              "127.0.0.1:0",
		Version:           "test-version",
		Static:            testTree(),
		DistDir:           t.TempDir(),
		WorkDir:           workDir,
		Log:               io.Discard,
		MaxReferenceBytes: limit,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	t.Cleanup(srv.Stop)

	handler := srv.Handler()
	id := finishedFitFor(t, handler, testReferenceLength)

	// Comfortably over the limit, written straight to the run directory: a
	// campaign's own reference.wav never passes through the upload reader
	// that enforces the cap on a live job.
	oversized := referenceWAV(t, 8, 44100)
	if len(oversized) <= limit {
		t.Fatalf("the oversized reference is only %d bytes, which is under the %d byte limit", len(oversized), limit)
	}

	path := filepath.Join(workDir, id, fitrun.FileReference)
	if err := os.WriteFile(path, oversized, 0o644); err != nil {
		t.Fatalf("write the oversized reference: %v", err)
	}

	recorder := getCompare(t, handler, id, "?columns=32&frames=4")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("compare = %d, want 500: %s", recorder.Code, recorder.Body.String())
	}
}

// Finding 3: a HEAD is an admitted method, and the endpoint's whole cost is
// the render and the two full-resolution spectrogram transforms below it --
// none of which has anything to contribute to a response whose body is
// discarded. This does not prove the work was skipped, only that a HEAD is
// answered with an empty, successful body; TestTheComparisonRefusesWhenNoSlotIsFree
// in the internal test file is what actually proves the heavy path is not
// reached, by holding every concurrency slot and checking a HEAD still gets
// through.
func TestTheComparisonAnswersHEADWithNoBody(t *testing.T) {
	handler := newFitServer(t).Handler()
	id := finishedFitFor(t, handler, testReferenceLength)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodHead,
		"/api/fit/jobs/"+id+"/compare?columns=32&frames=4", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("HEAD compare = %d: %s", recorder.Code, recorder.Body.String())
	}

	if recorder.Body.Len() != 0 {
		t.Fatalf("HEAD compare answered %d bytes of body, want none", recorder.Body.Len())
	}
}

func TestTheCompareAndReferenceEndpointsRefuseAnUnusableJobID(t *testing.T) {
	handler := newFitServer(t).Handler()

	for _, path := range []string{
		"/api/fit/jobs/..%2f..%2fetc/compare",
		"/api/fit/jobs/..%2f..%2fetc/reference",
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("GET %s = %d, want 400", path, recorder.Code)
		}
	}
}

// The render is capped at a minute and the reference is not capped anywhere:
// the upload limit allows about three minutes at 44.1 kHz, and the window is
// off by default. A reference left at its own length beside a clamped render
// would put the same column at two different moments on the two sides, which
// is a misalignment nothing in the payload would confess to. So the reference
// is cut to the same span.
//
// The long reference is written over a finished job's own reference.wav
// rather than uploaded, because the objective renders and scores the whole
// reference once per evaluation: fitting against a minute of audio would take
// this test into the minutes as well, and what is under test is the payload,
// not the fit.
func TestALongReferenceIsCutToTheSpanTheRenderCovers(t *testing.T) {
	workDir := t.TempDir()
	handler := newFitServerIn(t, workDir).Handler()
	id := finishedFitFor(t, handler, testReferenceLength)

	// Five seconds past the render cap, so the cut is unambiguous.
	const overlong = 65.0

	path := filepath.Join(workDir, id, fitrun.FileReference)
	if err := os.WriteFile(path, referenceWAV(t, overlong, testSampleRate), 0o644); err != nil {
		t.Fatalf("write the long reference: %v", err)
	}

	recorder := getCompare(t, handler, id, "?columns=32&frames=4")
	if recorder.Code != http.StatusOK {
		t.Fatalf("compare = %d: %s", recorder.Code, recorder.Body.String())
	}

	var payload fitCompare
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode the comparison: %v", err)
	}

	if payload.Seconds != 60 {
		t.Fatalf("a %v second reference produced a %v second comparison, want the 60 second cap",
			overlong, payload.Seconds)
	}

	if payload.Reference.Samples != payload.Render.Samples {
		t.Fatalf("the two sides are %d and %d samples, so the same column is two different moments",
			payload.Reference.Samples, payload.Render.Samples)
	}

	// The transform runs at full resolution before anything is reduced, so
	// the cut is also what bounds the work the endpoint does per request.
	if payload.Reference.Samples != int(60*testSampleRate) {
		t.Fatalf("the reference side is %d samples, want the %d of a 60 second cut",
			payload.Reference.Samples, 60*testSampleRate)
	}
}

// Both pictures have to be painted against the reference's floor, because
// that is what the objective scores both signals against: spectrogram.errorDB
// clamps the candidate and the reference alike to it. A render given a floor
// of its own would show detail the score counted as nothing, which is the one
// disagreement between picture and score this payload exists to prevent.
func TestBothSpectrogramsArePaintedAgainstTheReferencesFloor(t *testing.T) {
	workDir := t.TempDir()
	handler := newFitServerIn(t, workDir).Handler()
	id := finishedFitFor(t, handler, compareLength)

	samples, rate, err := wavio.LoadMono(filepath.Join(workDir, id, fitrun.FileReference))
	if err != nil {
		t.Fatalf("load the reference the run scored: %v", err)
	}

	view := optimizer.ComputeSpectrogram(samples, rate, optimizer.SpectrogramCoarseFrameSize)
	if view == nil {
		t.Fatal("the reference is too short to have a floor")
	}

	recorder := getCompare(t, handler, id, "?columns=32&frames=8")
	if recorder.Code != http.StatusOK {
		t.Fatalf("compare = %d: %s", recorder.Code, recorder.Body.String())
	}

	var payload fitCompare
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode the comparison: %v", err)
	}

	if payload.FloorDB == nil {
		t.Fatal("the comparison carries no floor")
	}

	want := math.Round(view.FloorDB*10) / 10
	if *payload.FloorDB != want {
		t.Fatalf("the comparison floor is %v, want the reference's own %v", *payload.FloorDB, want)
	}

	for name, side := range map[string]fitCompareSide{
		"reference": payload.Reference,
		"render":    payload.Render,
	} {
		if side.Spectrogram == nil {
			t.Fatalf("the %s side carries no spectrogram", name)
		}

		for column, row := range side.Spectrogram.DB {
			for bin, value := range row {
				if value < *payload.FloorDB {
					t.Fatalf("the %s spectrogram holds %v at column %d bin %d, below the reference floor %v",
						name, value, column, bin, *payload.FloorDB)
				}
			}
		}
	}
}
