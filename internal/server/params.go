package server

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// parseFitRequest folds the multipart form's scalar fields onto the defaults
// and validates every one of them.
//
// Absent fields keep their default rather than becoming a zero value, so a
// client that sends only a reference gets the same fit `glockenspiel fit`
// performs with no flags. Every bound is checked here, before a job slot is
// claimed, so an invalid request is a 400 and not a job that fails a
// millisecond after it started.
func parseFitRequest(request *http.Request, tuning *optimizer.MayflyTuning) (fitRequest, error) {
	settings := defaultFitRequest()
	settings.MayflyTuning = tuning

	var err error

	if settings.Note, err = formInt(request, "note", settings.Note, 0, 127); err != nil {
		return settings, err
	}

	if settings.Velocity, err = formInt(request, "velocity", settings.Velocity, 0, 127); err != nil {
		return settings, err
	}

	if settings.MaxIterations, err = formInt(request, "maxIterations", settings.MaxIterations, 1, maxFitIterations); err != nil {
		return settings, err
	}

	if settings.ReportEvery, err = formInt(request, "reportEvery", settings.ReportEvery, 0, maxFitIterations); err != nil {
		return settings, err
	}

	if settings.MayflyPopulation, err = formInt(request, "mayflyPopulation", settings.MayflyPopulation, 2, maxMayflyPopulation); err != nil {
		return settings, err
	}

	if settings.MayflySeed, err = formInt64(request, "mayflySeed", settings.MayflySeed); err != nil {
		return settings, err
	}

	if settings.MayflyEpochs, err = formInt(request, "mayflyEpochs", settings.MayflyEpochs, 1, 1000); err != nil {
		return settings, err
	}

	if settings.MayflyRestarts, err = formInt(request, "mayflyRestarts", settings.MayflyRestarts, 0, 1000); err != nil {
		return settings, err
	}

	settings.MayflyStagnation, err = formInt(request, "mayflyStagnation", settings.MayflyStagnation, 0, maxFitIterations)
	if err != nil {
		return settings, err
	}

	// A cost is whatever the metric produces, so the only thing worth refusing
	// here is a value that is not a number at all; the engine owns the rest.
	if settings.MayflyTargetCost, err = formFloat64Ptr(request, "mayflyTargetCost", -maxFitTargetCost, maxFitTargetCost); err != nil {
		return settings, err
	}

	// mayfly.NCAuto is -1, so -1 is the floor rather than zero, and the upper
	// bound is the population cap: offspring are produced by pairing the
	// population, so a count above it can never be reached.
	if settings.MayflyNC, err = formIntPtr(request, "mayflyNc", -1, maxMayflyPopulation); err != nil {
		return settings, err
	}

	if settings.MayflyNCRatio, err = formFloat64Ptr(request, "mayflyNcRatio", 0, maxMayflyPopulation); err != nil {
		return settings, err
	}

	budget, err := formDuration(request, "timeBudget", settings.timeBudget())
	if err != nil {
		return settings, err
	}

	if budget <= 0 || budget > maxFitTimeBudget {
		return settings, fmt.Errorf("timeBudget must be above zero and at most %s, got %s", maxFitTimeBudget, budget)
	}

	settings.TimeBudgetMS = budget.Milliseconds()

	if settings.Align, err = formBool(request, "align", settings.Align); err != nil {
		return settings, err
	}

	if settings.NormalizeGain, err = formBool(request, "normalizeGain", settings.NormalizeGain); err != nil {
		return settings, err
	}

	if value := request.FormValue("metric"); value != "" {
		settings.Metric = value
	}

	if value := request.FormValue("optimizer"); value != "" {
		settings.Optimizer = value
	}

	// The preset and the selection strategy are passed through unchecked for
	// the same reason the variant is: internal/optimizer and mayfly own those
	// name lists, and a second copy here would drift out of step with them and
	// produce a worse message while it did.
	if value := request.FormValue("mayflyPreset"); value != "" {
		settings.MayflyPreset = value
	}

	if value := request.FormValue("mayflySelection"); value != "" {
		settings.MayflySelection = value
	}

	switch value := request.FormValue("mayflyVariant"); {
	case value != "":
		settings.MayflyVariant = value

	// A preset selects a dialect of its own, and a tuning document may name
	// one so that a single file describes a whole run. The engine refuses a
	// dialect named twice, and it prefers the field over the document -- so the
	// default variant, which the client never asked for, must not be what
	// refuses or overrides either of them.
	case settings.MayflyPreset != "" || tuning.NamesDialect():
		settings.MayflyVariant = ""
	}

	// The metric and the optimizer name are validated by the packages that own
	// their vocabularies -- optimizer.ParseMetric and selectOptimizer -- rather
	// than by a second list here that could fall out of step with them.
	if _, err := selectOptimizer(settings); err != nil {
		return settings, err
	}

	return settings, nil
}

// formInt reads an optional integer form field and holds it to an inclusive
// range.
func formInt(request *http.Request, name string, fallback, low, high int) (int, error) {
	raw := strings.TrimSpace(request.FormValue(name))
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback, fmt.Errorf("%s must be a whole number, got %q", name, raw)
	}

	if value < low || value > high {
		return fallback, fmt.Errorf("%s must be in [%d,%d], got %d", name, low, high, value)
	}

	return value, nil
}

// formInt64 reads an optional 64-bit integer. A seed is unbounded on purpose:
// every value of it is as valid as every other.
func formInt64(request *http.Request, name string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(request.FormValue(name))
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback, fmt.Errorf("%s must be a whole number, got %q", name, raw)
	}

	return value, nil
}

// formIntPtr reads an optional integer that has to stay distinguishable from a
// written zero, so an absent field is nil rather than a value.
//
// It is the pointer twin of formInt rather than a variation of it: formInt's
// fallback answers "what should this be when nobody said", which is exactly the
// question the mayfly knobs cannot answer, because their zero already means
// something.
func formIntPtr(request *http.Request, name string, low, high int) (*int, error) {
	raw := strings.TrimSpace(request.FormValue(name))
	if raw == "" {
		return nil, nil //nolint:nilnil // an absent field is not an error: the knob stays unwritten.
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be a whole number, got %q", name, raw)
	}

	if value < low || value > high {
		return nil, fmt.Errorf("%s must be in [%d,%d], got %d", name, low, high, value)
	}

	return &value, nil
}

// formFloat64Ptr reads an optional number that has to stay distinguishable from
// a written zero. A target cost of zero is a target, not the absence of one.
func formFloat64Ptr(request *http.Request, name string, low, high float64) (*float64, error) {
	raw := strings.TrimSpace(request.FormValue(name))
	if raw == "" {
		return nil, nil //nolint:nilnil // an absent field is not an error: the knob stays unwritten.
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, fmt.Errorf("%s must be a number, got %q", name, raw)
	}

	// NaN passes every range check below, because every comparison against it
	// is false, and mayfly sanitises a non-finite knob mid-run -- so it would
	// not fail, it would quietly become something else.
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil, fmt.Errorf("%s must be a finite number, got %q", name, raw)
	}

	if value < low || value > high {
		return nil, fmt.Errorf("%s must be in [%g,%g], got %g", name, low, high, value)
	}

	return &value, nil
}

// formBool reads an optional boolean, accepting the spellings a browser form
// and a shell client each produce.
func formBool(request *http.Request, name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(request.FormValue(name))
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback, fmt.Errorf("%s must be true or false, got %q", name, raw)
	}

	return value, nil
}

// formDuration reads an optional Go duration, and -- exactly as the fit
// command's --time-budget flag does -- reads a bare number as seconds, so the
// two front ends accept the same spellings.
func formDuration(request *http.Request, name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(request.FormValue(name))
	if raw == "" {
		return fallback, nil
	}

	if parsed, err := time.ParseDuration(raw); err == nil {
		return parsed, nil
	}

	seconds, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback, fmt.Errorf("%s must be a duration such as 30s or 10m, got %q", name, raw)
	}

	// ParseFloat accepts "NaN" and "Inf", and converting either to a Duration
	// is undefined in the language spec -- so the bounds check that follows
	// would be deciding on whatever the hardware happened to produce.
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return fallback, fmt.Errorf("%s must be a finite duration, got %q", name, raw)
	}

	return time.Duration(seconds * float64(time.Second)), nil
}

// queryInt reads an optional query parameter and holds it to an inclusive range.
func queryInt(query url.Values, name string, fallback, low, high int) (int, error) {
	raw := strings.TrimSpace(query.Get(name))
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback, fmt.Errorf("%s must be a whole number, got %q", name, raw)
	}

	if value < low || value > high {
		return fallback, fmt.Errorf("%s must be in [%d,%d], got %d", name, low, high, value)
	}

	return value, nil
}

// queryFloat reads an optional query parameter and holds it to an exclusive
// lower and inclusive upper bound. A zero-length render is rejected rather than
// answered with an empty file.
func queryFloat(query url.Values, name string, fallback, low, high float64) (float64, error) {
	raw := strings.TrimSpace(query.Get(name))
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback, fmt.Errorf("%s must be a number, got %q", name, raw)
	}

	// NaN has to be rejected before the range check rather than by it: every
	// comparison against NaN is false, so the bounds below would pass it
	// through to a render that silently produces an empty file. The infinities
	// the range check does catch, but naming all three here is what keeps that
	// from being a coincidence.
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fallback, fmt.Errorf("%s must be a finite number, got %q", name, raw)
	}

	if value <= low || value > high {
		return fallback, fmt.Errorf("%s must be in (%g,%g], got %g", name, low, high, value)
	}

	return value, nil
}

// writeJSON sends one JSON body.
//
// It encodes into a buffer first: an encoder writing straight to the
// ResponseWriter would have committed the status line before it could discover
// that the value does not marshal, leaving a 200 with a truncated body.
func writeJSON(writer http.ResponseWriter, status int, body any) {
	payload, err := json.Marshal(body)
	if err != nil {
		http.Error(writer, "the response could not be encoded", http.StatusInternalServerError)

		return
	}

	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Every one of these describes a job that is changing under the caller.
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)

	_, _ = writer.Write(payload)
}

// writeJSONError sends a failure in the same shape as every success, so a
// client parses one thing.
func writeJSONError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, struct {
		Error string `json:"error"`
	}{Error: message})
}

// allowPostMethod gates the mutating fit endpoints.
//
// It sits beside allowReadMethods rather than replacing it. Loosening that one
// to admit POST would open every static route to writes at the same time --
// the embedded tree, the wasm, the version endpoint -- which is the opposite of
// what adding a write surface should do. Two gates, each naming exactly what
// its routes accept.
func allowPostMethod(writer http.ResponseWriter, request *http.Request) bool {
	if request.Method == http.MethodPost {
		return true
	}

	writer.Header().Set("Allow", "POST")
	writeJSONError(writer, http.StatusMethodNotAllowed, "this endpoint only accepts POST")

	return false
}
