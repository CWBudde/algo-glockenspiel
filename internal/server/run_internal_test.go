package server

// This file is package server, not server_test: it exercises runFit's pinned
// dimension guard directly, which is unexported and reached from no HTTP
// route on its own. Every other test in this package's public surface goes
// through fit_test.go and its siblings.

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
)

// TestRunFitDropsPinnedOnCodecDisagreement pins Finding 3 of the Task 2
// review: outcome.Summary.Pinned is what fitrun's own codec counted while
// finishing the run, and runFit's codec argument is a second, separate box
// built earlier in the request. Nothing keeps the two in step; they only
// happen to agree today because buildObjective and fitrun.prepare build the
// same config from the same inputs. This test breaks that agreement on
// purpose -- the codec handed to runFit is built from a preset with one mode
// trimmed down from the four the run itself searches, so it can never
// describe the run's own result -- and checks that runFit notices and serves
// no pinned list at all, rather than the wrong one.
func TestRunFitDropsPinnedOnCodecDisagreement(t *testing.T) {
	dir := t.TempDir()

	template, err := preset.Load(filepath.FromSlash("../../testdata/presets/minimal.json"))
	if err != nil {
		t.Fatalf("load preset: %v", err)
	}

	engine, err := synth.NewSynthesizer(template, 44100)
	if err != nil {
		t.Fatalf("new synthesizer: %v", err)
	}

	reference := engine.RenderNote(69, 100, 0.05)
	if err := wavio.WriteMono(filepath.Join(dir, uploadFileName), 44100, reference); err != nil {
		t.Fatalf("write upload: %v", err)
	}

	// modes: -1 keeps the template's own four modes rather than reseeding
	// from the reference's partials, so the run's real dimension is known
	// ahead of time and the wrong codec below can be built to disagree with
	// it deliberately rather than by accident.
	settings := fitRequest{
		Note:          69,
		Velocity:      100,
		Optimizer:     fitrun.EngineSimple,
		Metric:        string(optimizer.MetricRMS),
		MaxIterations: 2,
		Modes:         -1,
	}

	job := newFitJob("job-1", dir, settings, 44100, 0.05, func() {})

	// The wrong codec: one mode fewer than the template runFit's own fitrun
	// call will actually search, so its EncodedBounds can never normalize the
	// real result and Pinned reports nothing rather than the right count.
	wrongTemplate := template.Clone()
	wrongTemplate.Parameters.Modes = wrongTemplate.Parameters.Modes[:1]

	wrongCodec, err := optimizer.NewParamCodec(&wrongTemplate.Parameters)
	if err != nil {
		t.Fatalf("new param codec: %v", err)
	}

	var log bytes.Buffer

	server := &Server{config: Config{Log: &log}}

	server.runFit(context.Background(), job, wrongCodec, template, nil)

	snapshot := job.snapshot()
	if snapshot.State != fitSucceeded {
		t.Fatalf("job state = %s, want %s (log: %s)", snapshot.State, fitSucceeded, log.String())
	}

	if len(snapshot.Pinned) != 0 {
		t.Errorf("snapshot pinned = %+v, want none once the two codecs disagree", snapshot.Pinned)
	}

	if !strings.Contains(log.String(), "pinned dimension count mismatch") {
		t.Errorf("log = %q, want a line naming the pinned dimension count mismatch", log.String())
	}
}
