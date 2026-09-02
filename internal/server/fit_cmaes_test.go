package server_test

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// shortCMAESFit is a CMA-ES run that finishes on its own within a test's
// patience: two generations of four candidates, and a single run, so the
// restart loop stops as soon as that run is over.
func shortCMAESFit() map[string]string {
	return map[string]string{
		"optimizer":       "cmaes",
		"cmaesCovariance": "separable",
		"cmaesLambda":     "4",
		"cmaesSigma":      "0.3",
		"cmaesSeed":       "11",
		"cmaesRestarts":   "1",
		"maxIterations":   "2",
		"reportEvery":     "1",
		"timeBudget":      "20s",
	}
}

func TestFitRunsWithTheCMAESBackend(t *testing.T) {
	handler := newFitServer(t).Handler()

	response := startFit(t, handler, shortCMAESFit())
	if response.Code != http.StatusAccepted {
		t.Fatalf("start = %d, want 202: %s", response.Code, response.Body.String())
	}

	final := waitForTerminalState(t, handler, 60*time.Second)
	if final.State != "succeeded" {
		t.Fatalf("state = %q (error %q), want succeeded", final.State, final.Error)
	}

	if final.Optimizer != "cmaes" {
		t.Fatalf("snapshot names optimizer %q, want cmaes", final.Optimizer)
	}

	if final.Evaluations == 0 {
		t.Fatal("the fit reports zero objective evaluations, so nothing was searched")
	}

	if !final.HasPreset {
		t.Fatal("a succeeded fit reports no preset")
	}
}

// The dense covariance mode the library offers is deliberately not exposed, so
// a request for it is a bad request rather than a job that claims the single
// fit slot and then fails.
func TestFitRejectsAnUnsupportedCMAESCovariance(t *testing.T) {
	handler := newFitServer(t).Handler()

	fields := shortCMAESFit()
	fields["cmaesCovariance"] = "full"

	response := startFit(t, handler, fields)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("start = %d, want 400: %s", response.Code, response.Body.String())
	}

	if !strings.Contains(response.Body.String(), "covariance") {
		t.Fatalf("the rejection does not name the covariance: %s", response.Body.String())
	}
}
