package server_test

// The snapshot's provenance half: the request echo, the profile, and the two
// numbers derived from the clock. They are what a results view reads to say
// what a fit was, so they are checked against a request whose every field is
// something other than the default.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A fit whose settings differ from the defaults in every field the echo is
// read for, so a snapshot that quietly reported the defaults would fail.
func distinctiveFit() map[string]string {
	return map[string]string{
		"optimizer":        "mayfly",
		"metric":           "placement",
		"note":             "72",
		"velocity":         "90",
		"mayflySeed":       "918273645",
		"mayflyPopulation": "20",
		"maxIterations":    "2",
		"reportEvery":      "1",
		"timeBudget":       "45s",
		"align":            "false",
		"normalizeGain":    "true",
	}
}

func TestAFinishedJobEchoesTheRequestItRanUnder(t *testing.T) {
	handler := newFitServer(t).Handler()

	if response := startFit(t, handler, distinctiveFit()); response.Code != 202 {
		t.Fatalf("start = %d: %s", response.Code, response.Body.String())
	}

	snapshot := waitForTerminalState(t, handler, 60*time.Second)
	echo := snapshot.Request

	if echo.Optimizer != "mayfly" || echo.Metric != "placement" {
		t.Fatalf("echo says optimizer %q metric %q, want mayfly and placement", echo.Optimizer, echo.Metric)
	}

	if echo.Note != 72 || echo.Velocity != 90 {
		t.Fatalf("echo says note %d velocity %d, want 72 and 90", echo.Note, echo.Velocity)
	}

	if echo.MayflySeed != "918273645" {
		t.Fatalf("echo says mayfly seed %q, want 918273645", echo.MayflySeed)
	}

	// The resolved seed is the one that makes the run repeatable. This
	// request named its own, so the two agree; a request that had asked the
	// backend to pick would differ here and the echo would be the only record
	// of what it picked.
	if echo.Seed != "918273645" {
		t.Fatalf("echo says the resolved seed was %q, want 918273645", echo.Seed)
	}

	if echo.Workers <= 0 {
		t.Fatalf("echo says %d workers, want the resolved count", echo.Workers)
	}

	if echo.MaxIterations != 2 || echo.TimeBudgetMS != 45_000 {
		t.Fatalf("echo says %d iterations and %d ms, want 2 and 45000", echo.MaxIterations, echo.TimeBudgetMS)
	}

	if echo.Align || !echo.NormalizeGain {
		t.Fatalf("echo says align=%v normalizeGain=%v, want false and true", echo.Align, echo.NormalizeGain)
	}

	// A default the client never sent is part of the provenance too: the
	// point of the echo is that it describes the run rather than the request
	// body.
	if echo.Downmix != "first" {
		t.Fatalf("echo says downmix %q, want the default first", echo.Downmix)
	}

	if echo.MayflyTuning {
		t.Fatal("echo claims a mayfly tuning document was uploaded")
	}

	if echo.Bounds != nil {
		t.Fatalf("echo carries a search box for a run that uploaded none: %+v", echo.Bounds)
	}
}

// The uploaded box is provenance: a pinned dimension means nothing without
// the limit it is pinned to.
func TestTheEchoCarriesTheUploadedSearchBox(t *testing.T) {
	handler := newFitServer(t).Handler()

	bounds := []byte(`{"decay_ms":[60,400],"harmonic_gain":[0,0.9]}`)

	response := startFitWithFiles(t, handler,
		referenceWAV(t, testReferenceLength, testSampleRate),
		shortFit(), map[string][]byte{"bounds": bounds})
	if response.Code != 202 {
		t.Fatalf("start = %d: %s", response.Code, response.Body.String())
	}

	snapshot := waitForTerminalState(t, handler, 60*time.Second)

	box := snapshot.Request.Bounds
	if box == nil {
		t.Fatal("the echo carries no search box for a run that uploaded one")
	}

	if box.DecayMs != [2]float64{60, 400} {
		t.Fatalf("the echoed decay box is %v, want [60 400]", box.DecayMs)
	}

	if box.HarmonicGain != [2]float64{0, 0.9} {
		t.Fatalf("the echoed harmonic gain box is %v, want [0 0.9]", box.HarmonicGain)
	}
}

// Task 8's per-term bars are scaled by these norms. They are sent rather than
// carried by the client so that the bars and the score cannot disagree.
func TestTheSnapshotCarriesTheProfileItScoredBy(t *testing.T) {
	handler := newFitServer(t).Handler()

	if response := startFit(t, handler, shortFit()); response.Code != 202 {
		t.Fatalf("start = %d: %s", response.Code, response.Body.String())
	}

	snapshot := waitForTerminalState(t, handler, 60*time.Second)

	profile := snapshot.Profile
	if profile == nil {
		t.Fatal("a composite metric reported no profile")
	}

	if profile.Name != "balanced" {
		t.Fatalf("the profile is %q, want balanced", profile.Name)
	}

	var total float64

	for _, term := range profile.Terms {
		if term.Weight <= 0 {
			t.Fatalf("term %q is listed with weight %v", term.Term, term.Weight)
		}

		if term.Norm <= 0 {
			t.Fatalf("term %q has norm %v", term.Term, term.Norm)
		}

		total += term.Weight

		if term.Term == "partial_cents" && term.Norm != 10 {
			t.Fatalf("partial_cents has norm %v, want the default 10", term.Norm)
		}
	}

	// The weights of a named profile sum to one, which is what makes a score
	// a weighted mean and a share a fraction of it.
	if total < 0.999 || total > 1.001 {
		t.Fatalf("the profile's weights sum to %v, want 1", total)
	}
}

// A single-term legacy metric has no profile, and saying so is different from
// sending an empty one.
func TestALegacyMetricReportsNoProfile(t *testing.T) {
	handler := newFitServer(t).Handler()

	settings := shortFit()
	settings["metric"] = "rms"

	if response := startFit(t, handler, settings); response.Code != 202 {
		t.Fatalf("start = %d: %s", response.Code, response.Body.String())
	}

	snapshot := waitForTerminalState(t, handler, 60*time.Second)
	if snapshot.Profile != nil {
		t.Fatalf("the rms metric reported a profile: %+v", snapshot.Profile)
	}
}

// A run that spends its iteration cap has spent its whole budget, whatever
// time it had left.
func TestABudgetFractionOfOneMeansTheCapWasReached(t *testing.T) {
	handler := newFitServer(t).Handler()

	if response := startFit(t, handler, shortMayflyFit()); response.Code != 202 {
		t.Fatalf("start = %d: %s", response.Code, response.Body.String())
	}

	snapshot := waitForTerminalState(t, handler, 60*time.Second)

	if snapshot.OptimizerIterations < 2 {
		t.Fatalf("the run did its cap of 2 iterations as %d", snapshot.OptimizerIterations)
	}

	if snapshot.BudgetFraction != 1 {
		t.Fatalf("budgetFraction = %v after the iteration cap was reached, want 1", snapshot.BudgetFraction)
	}

	if snapshot.EvaluationsPerSecond <= 0 {
		t.Fatalf("evaluationsPerSecond = %v after %d evaluations in %d ms, want a positive rate",
			snapshot.EvaluationsPerSecond, snapshot.Evaluations, snapshot.ElapsedMS)
	}
}

// The gain the objective applied is Metrics.GainDB, and the snapshot carries
// the whole breakdown from the first report rather than only at the end. A
// display that could show the terms only for a finished run would be useless
// for the thing a live fit is watched for: seeing which term is moving.
//
// The snapshot is read as raw JSON here because the mirror struct above does
// not spell optimizer.Metrics out; what matters is that the terms are there
// while the run is still going, with the applied gain among them.
func TestTheTermsAndTheAppliedGainArriveWhileTheRunIsGoing(t *testing.T) {
	handler := newFitServer(t).Handler()

	if response := startFit(t, handler, endlessFit()); response.Code != http.StatusAccepted {
		t.Fatalf("start = %d: %s", response.Code, response.Body.String())
	}

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/fit", nil))

		var body struct {
			State   string `json:"state"`
			Metrics *struct {
				GainDB       *float64 `json:"gain_db"`
				PartialCents *float64 `json:"partial_cents"`
			} `json:"metrics"`
		}

		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode the status: %v", err)
		}

		if body.State != "running" {
			t.Fatalf("the endless fit is %q before it reported any terms", body.State)
		}

		if body.Metrics == nil {
			time.Sleep(5 * time.Millisecond)

			continue
		}

		if body.Metrics.GainDB == nil {
			t.Fatal("the live breakdown carries no gain_db")
		}

		return
	}

	t.Fatal("the running fit reported no terms within the timeout")
}
