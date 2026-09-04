package server_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/fitschema"
)

// TestMaxIterationsAtTheSchemaCapIsAcceptedOneAboveIsRefused pins
// parseFitRequest's range check to internal/fitschema.Fields rather than to a
// literal copied into this test. internal/fitschema is the one table the
// server, internal/browserfit and cmd/gen-fit-schema all read; reading the
// cap from it here, instead of writing 100000 a second time, is what makes
// this test fail the moment the table's cap and the server's own idea of it
// come apart -- which a literal copy would not have caught.
func TestMaxIterationsAtTheSchemaCapIsAcceptedOneAboveIsRefused(t *testing.T) {
	handler := newFitServer(t).Handler()

	_, max := fitschema.IntLimit("maxIterations")

	fields := shortFit()
	fields["maxIterations"] = strconv.Itoa(max)
	fields["timeBudget"] = "1s"

	accepted := startFit(t, handler, fields)
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("start at the cap (%d) = %d, want 202: %s", max, accepted.Code, accepted.Body.String())
	}

	// The job is cancelled rather than awaited: a request at the cap is
	// still 100000 iterations, and this test only cares that it was
	// accepted, not that it finishes. Cancelling frees the slot immediately,
	// which is what lets the next start below reuse it right away.
	postCancel(t, handler, "")

	fields["maxIterations"] = strconv.Itoa(max + 1)

	refused := startFit(t, handler, fields)
	if refused.Code != http.StatusBadRequest {
		t.Fatalf("start above the cap (%d) = %d, want 400: %s", max+1, refused.Code, refused.Body.String())
	}

	if !strings.Contains(refused.Body.String(), "maxIterations") {
		t.Fatalf("refusal above the cap does not mention maxIterations: %s", refused.Body.String())
	}
}
