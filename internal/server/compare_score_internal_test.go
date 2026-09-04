package server

// This file is package server, not server_test: Finding 1 of the whole-phase
// review needs a job whose recorded metrics disagree with a naive render by a
// known amount, and the only way to get one without waiting on a real
// optimizer run to happen to land there is to plant it directly.
//
// The scenario is the one the review named: a preset that scores well because
// the composite objective is gain-invariant and lag-aligned, rendered raw
// beside a peak-normalised reference. Phase 8.6 recorded a refit 24.5 dB quiet
// while beating the shipped preset by 62 percent -- this is that case, made
// reproducible.

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
)

const scoreAlignedSampleRate = 44100

// scoreAlignedFixture renders one preset at two velocities: loud, written out
// as the reference the job scored against, and quiet, which is what the job's
// fitted preset (rendered at job.request's own note and velocity) produces.
// The two are the same shape and different only in level, so a gain that
// undoes the level difference is unambiguous.
type scoreAlignedFixture struct {
	job       *fitJob
	reference []float32
	rawRender []float32
}

func newScoreAlignedFixture(t *testing.T, dir string, lag int) scoreAlignedFixture {
	t.Helper()

	template, err := preset.Load(filepath.FromSlash("../../testdata/presets/minimal.json"))
	if err != nil {
		t.Fatalf("load preset: %v", err)
	}

	engine, err := synth.NewSynthesizer(template, scoreAlignedSampleRate)
	if err != nil {
		t.Fatalf("new synthesizer: %v", err)
	}

	const (
		seconds       = 0.5
		loudVelocity  = 127
		quietVelocity = 6
	)

	reference := engine.RenderNote(69, loudVelocity, seconds)
	rawRender := engine.RenderNote(69, quietVelocity, seconds)

	if err := wavio.WriteMono(filepath.Join(dir, "reference.wav"), scoreAlignedSampleRate, reference); err != nil {
		t.Fatalf("write reference: %v", err)
	}

	// The gain the composite objective would have solved for: the RMS ratio
	// over the two signals, closed form, the same formula composite.go's
	// levelGainDB uses. It is computed from the two actual renders rather
	// than picked, so the test proves the fix undoes exactly the level
	// difference the scenario has, not a convenient round number.
	gainDB := rmsGainDB(rawRender, reference)

	job := newFitJob("job-1", dir, fitRequest{Note: 69, Velocity: quietVelocity}, scoreAlignedSampleRate, seconds, func() {})
	job.result = template.Clone()
	job.metrics = &optimizer.Metrics{GainDB: gainDB, Lag: lag}

	return scoreAlignedFixture{job: job, reference: reference, rawRender: rawRender}
}

func rmsGainDB(candidate, reference []float32) float64 {
	count := min(len(candidate), len(reference))

	var candEnergy, refEnergy float64

	for i := range count {
		c, r := float64(candidate[i]), float64(reference[i])
		candEnergy += c * c
		refEnergy += r * r
	}

	if candEnergy <= 0 || refEnergy <= 0 {
		return 0
	}

	return 10 * math.Log10(refEnergy/candEnergy)
}

func peakAbs(min, max []float64) float64 {
	peak := 0.0

	for i := range min {
		peak = math.Max(peak, math.Max(math.Abs(min[i]), math.Abs(max[i])))
	}

	return peak
}

// TestTheComparisonScalesTheRenderToTheGainTheObjectiveScored pins Finding 1:
// a fit that scored well because the objective is gain-invariant must not be
// drawn as a near-flat line beside a full-scale reference. Under the old
// behaviour, which rendered the preset raw with no gain applied, this test
// fails: the render's peak stays at the quiet preset's own level, a fraction
// of the reference's.
func TestTheComparisonScalesTheRenderToTheGainTheObjectiveScored(t *testing.T) {
	dir := t.TempDir()
	fixture := newScoreAlignedFixture(t, dir, 0)

	rawPeak := peakAbs([]float64{minOf(fixture.rawRender)}, []float64{maxOf(fixture.rawRender)})
	refPeak := peakAbs([]float64{minOf(fixture.reference)}, []float64{maxOf(fixture.reference)})

	// The scenario has to actually be quiet, or the assertion below proves
	// nothing.
	if rawPeak*3 > refPeak {
		t.Fatalf("the raw render peaks at %v against a reference of %v, which is not the quiet scenario this test needs",
			rawPeak, refPeak)
	}

	srv := &Server{config: Config{Log: t.Output()}}
	srv.jobs.adopt(fixture.job)

	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		"/api/fit/jobs/job-1/compare?columns=64&frames=8", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("compare = %d: %s", recorder.Code, recorder.Body.String())
	}

	var payload fitCompare
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode the comparison: %v", err)
	}

	renderPeak := peakAbs(payload.Render.Waveform.Min, payload.Render.Waveform.Max)

	// "Comparable level" rather than an exact match: the peak-to-RMS ratio of
	// a percussive strike is not perfectly velocity-invariant, and the
	// gain solved from RMS energy is not solved from peaks. A factor of two
	// either side of the reference's peak is a world away from the raw
	// render's, which was a fraction of it, and is what a listener would call
	// "the same loudness" rather than "silent beside it".
	if renderPeak < refPeak/2 || renderPeak > refPeak*2 {
		t.Fatalf("the compared render peaks at %v against a reference of %v: not comparable levels",
			renderPeak, refPeak)
	}

	// And it has to differ from the untransformed render -- otherwise the
	// endpoint might just happen to already draw near the reference's level
	// by coincidence of the fixture, rather than because it applied the gain.
	if renderPeak < rawPeak*1.5 {
		t.Fatalf("the compared render peaks at %v, barely above the raw render's %v: the gain was not applied",
			renderPeak, rawPeak)
	}
}

// TestTheComparisonShiftsTheRenderByTheRecordedLag pins the other half of
// Finding 1: the render has to be drawn at the alignment the objective scored
// it at, not at its own onset. A negative lag means the objective found the
// candidate needed pushing later to match the reference, so the picture
// should show silence where the raw render would show its attack.
//
// Under the old behaviour, which never looked at metrics.Lag, this test
// fails: the render's attack shows up in the first column exactly where the
// reference's does, because both start at their own onset.
func TestTheComparisonShiftsTheRenderByTheRecordedLag(t *testing.T) {
	dir := t.TempDir()

	const lag = -2000 // about 45 ms at 44.1 kHz: several columns of a 0.5 s render at 64 columns.

	fixture := newScoreAlignedFixture(t, dir, lag)

	srv := &Server{config: Config{Log: t.Output()}}
	srv.jobs.adopt(fixture.job)

	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		"/api/fit/jobs/job-1/compare?columns=64&frames=8", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("compare = %d: %s", recorder.Code, recorder.Body.String())
	}

	var payload fitCompare
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode the comparison: %v", err)
	}

	const leadingColumns = 4

	renderLead := peakAbs(payload.Render.Waveform.Min[:leadingColumns], payload.Render.Waveform.Max[:leadingColumns])
	referenceLead := peakAbs(payload.Reference.Waveform.Min[:leadingColumns], payload.Reference.Waveform.Max[:leadingColumns])

	if referenceLead < 0.01 {
		t.Fatalf("the reference itself carries no attack in the first %d columns (peak %v), so this scenario proves nothing",
			leadingColumns, referenceLead)
	}

	if renderLead > 0.001 {
		t.Fatalf("the shifted render's first %d columns peak at %v, but a %d sample lag should have left them silent",
			leadingColumns, renderLead, -lag)
	}
}

// TestTheComparisonRefusesWhenNoSlotIsFree pins the concurrency half of
// Finding 3: the endpoint's cost is two signals and two full-resolution
// spectrogram views before anything is reduced, and nothing else bounds how
// many of those may run at once against a free, repeatable GET. Every slot is
// held here directly, without four real requests racing each other, so the
// refusal is deterministic rather than hoped for under load. A HEAD is
// checked too, and it has to get through even with every slot held, which is
// what proves the HEAD short-circuit in handleFitJobCompare runs before the
// slot is ever acquired.
func TestTheComparisonRefusesWhenNoSlotIsFree(t *testing.T) {
	dir := t.TempDir()
	fixture := newScoreAlignedFixture(t, dir, 0)

	srv := &Server{config: Config{Log: t.Output()}, compareSlots: make(chan struct{}, 1)}
	srv.jobs.adopt(fixture.job)

	// Hold the one slot the server has.
	srv.compareSlots <- struct{}{}
	defer func() { <-srv.compareSlots }()

	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/fit/jobs/job-1/compare", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("compare with no free slot = %d, want 503: %s", recorder.Code, recorder.Body.String())
	}

	head := httptest.NewRecorder()
	srv.Handler().ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/api/fit/jobs/job-1/compare", nil))

	if head.Code != http.StatusOK {
		t.Fatalf("HEAD compare with no free slot = %d, want 200 (HEAD must not need a slot)", head.Code)
	}
}

func minOf(signal []float32) float64 {
	lowest := 0.0

	for _, sample := range signal {
		lowest = math.Min(lowest, float64(sample))
	}

	return lowest
}

func maxOf(signal []float32) float64 {
	highest := 0.0

	for _, sample := range signal {
		highest = math.Max(highest, float64(sample))
	}

	return highest
}

// TestScoreAlignedHandlesTheDegenerateCases pins the three cases Finding 1
// calls out by name: a job whose metrics are missing, a non-finite gain, and
// a lag that shifts every sample out of range.
func TestScoreAlignedHandlesTheDegenerateCases(t *testing.T) {
	rendered := []float32{1, 2, 3, 4, 5}

	t.Run("a restored job with no metrics leaves the render as it is", func(t *testing.T) {
		got := scoreAligned(rendered, nil)

		for i := range rendered {
			if got[i] != rendered[i] {
				t.Fatalf("scoreAligned(nil metrics) = %v, want the render unchanged", got)
			}
		}
	})

	t.Run("a non-finite gain is treated as zero dB", func(t *testing.T) {
		for _, gainDB := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
			got := scoreAligned(rendered, &optimizer.Metrics{GainDB: gainDB, Lag: 0})

			for i := range rendered {
				if got[i] != rendered[i] {
					t.Fatalf("scoreAligned(gainDB=%v) = %v, want the render unscaled", gainDB, got)
				}
			}
		}
	})

	t.Run("a lag past the render is silence, not a panic", func(t *testing.T) {
		got := scoreAligned(rendered, &optimizer.Metrics{GainDB: 0, Lag: len(rendered) + 100})

		if len(got) != len(rendered) {
			t.Fatalf("scoreAligned returned %d samples, want %d", len(got), len(rendered))
		}

		for i, value := range got {
			if value != 0 {
				t.Fatalf("scoreAligned(lag past the render)[%d] = %v, want silence", i, value)
			}
		}

		// The negative side of the same case.
		got = scoreAligned(rendered, &optimizer.Metrics{GainDB: 0, Lag: -(len(rendered) + 100)})

		for i, value := range got {
			if value != 0 {
				t.Fatalf("scoreAligned(lag before the render)[%d] = %v, want silence", i, value)
			}
		}
	})

	t.Run("an ordinary gain and lag apply together", func(t *testing.T) {
		got := scoreAligned(rendered, &optimizer.Metrics{GainDB: 20, Lag: 2})

		// Gain of 20 dB is a factor of 10. Lag 2 means shifted[i] = rendered[i+2]*10.
		want := []float32{30, 40, 50, 0, 0}

		for i := range want {
			if math.Abs(float64(got[i]-want[i])) > 1e-4 {
				t.Fatalf("scoreAligned(gain=20dB, lag=2) = %v, want %v", got, want)
			}
		}
	})
}
