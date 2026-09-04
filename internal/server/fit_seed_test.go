package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

// TestFitSeedsTheModesFromTheReferenceAndReportsIt pins the service's half
// of Phase 8.3: the starting modes come from the uploaded reference's
// partials unless the request says otherwise, and the snapshot says how many
// did and which dimensions of the result sit on a bound.
func TestFitSeedsTheModesFromTheReferenceAndReportsIt(t *testing.T) {
	handler := newFitServer(t).Handler()

	response := startFit(t, handler, shortFit())
	if response.Code != http.StatusAccepted {
		t.Fatalf("start = %d, want 202: %s", response.Code, response.Body.String())
	}

	final := waitForTerminalState(t, handler, 60*time.Second)
	if final.State != "succeeded" {
		t.Fatalf("state = %q (error %q), want succeeded", final.State, final.Error)
	}

	// The test reference is two decaying sines, so the analysis lists two
	// partials and the fit searches two modes.
	if final.SeededModes != 2 {
		t.Fatalf("seededModes = %d, want 2", final.SeededModes)
	}

	presetResponse := httptest.NewRecorder()
	handler.ServeHTTP(presetResponse, httptest.NewRequest(http.MethodGet, "/api/fit/preset", nil))

	fitted, err := preset.Decode(presetResponse.Body.Bytes(), "the fitted preset")
	if err != nil {
		t.Fatalf("the fitted preset does not validate: %v", err)
	}

	if len(fitted.Parameters.Modes) != 2 || fitted.Version != preset.CurrentVersion {
		t.Fatalf("fitted preset has %d modes in version %q, want 2 in %q", len(fitted.Parameters.Modes), fitted.Version, preset.CurrentVersion)
	}

	for _, pinned := range final.Pinned {
		if pinned.Name == "" || (pinned.Bound != "min" && pinned.Bound != "max") {
			t.Fatalf("malformed pinned dimension %+v", pinned)
		}
	}

	fields := shortFit()
	fields["modes"] = "-1"

	response = startFit(t, handler, fields)
	if response.Code != http.StatusAccepted {
		t.Fatalf("start with modes=-1 = %d, want 202: %s", response.Code, response.Body.String())
	}

	final = waitForTerminalState(t, handler, 60*time.Second)
	if final.State != "succeeded" || final.SeededModes != 0 {
		t.Fatalf("state %q seededModes %d, want succeeded with the template's modes", final.State, final.SeededModes)
	}

	fields["modes"] = "-2"

	if response = startFit(t, handler, fields); response.Code != http.StatusBadRequest {
		t.Fatalf("modes=-2 = %d, want 400", response.Code)
	}
}
