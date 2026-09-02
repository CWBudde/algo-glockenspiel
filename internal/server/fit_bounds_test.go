package server_test

import (
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/preset"
)

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
		minFilterHz  = 300.0
		maxFilterHz  = 3000.0
		// Encoding and decoding a bound is a round trip through a logarithm,
		// so a value sitting on a boundary comes back a few ulps outside it.
		tolerance = 1e-6
	)

	bounds := []byte(`{"filter_freq": [300.0, 3000.0], "decay_ms": [50.0, 400.0], "amplitude": [-1.0, 1.0]}`)

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

	filter := fitted.Parameters.FilterFrequency
	if filter < minFilterHz-tolerance || filter > maxFilterHz+tolerance {
		t.Fatalf("fitted filter frequency %g is outside the requested [%g,%g]", filter, minFilterHz, maxFilterHz)
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
// inside a narrow range are free to sit outside it. The starting preset's own
// modes are kept for this, because a seeded mode is the reference's and says
// nothing about the box.
func TestFitWithoutBoundsKeepsTheDefaultBox(t *testing.T) {
	handler := newFitServer(t).Handler()

	fields := shortFit()
	fields["modes"] = "-1"

	response := startFit(t, handler, fields)
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
