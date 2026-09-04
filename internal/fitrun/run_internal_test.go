package fitrun

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// reference is the short synthetic fixture the package's other tests use: half
// a second of a bar the model itself can render.
const reference = "../../testdata/reference/legacy_synth_a4.wav"

func TestShouldCheckpoint(t *testing.T) {
	tests := []struct {
		name                string
		optimizerIterations int
		lastCheckpointed    int
		checkpointEvery     int
		want                bool
	}{
		{name: "every iteration", optimizerIterations: 3, lastCheckpointed: 2, checkpointEvery: 1, want: true},
		{name: "interval reached", optimizerIterations: 4, lastCheckpointed: 2, checkpointEvery: 2, want: true},
		{name: "interval overshot", optimizerIterations: 9, lastCheckpointed: 2, checkpointEvery: 5, want: true},
		{name: "interval not reached", optimizerIterations: 5, lastCheckpointed: 4, checkpointEvery: 2, want: false},
		{name: "disabled", optimizerIterations: 4, checkpointEvery: 0, want: false},
		{name: "never", optimizerIterations: 4, checkpointEvery: CheckpointNever, want: false},
		{name: "backend reports no iteration count", optimizerIterations: 0, checkpointEvery: 1, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldCheckpoint(tc.optimizerIterations, tc.lastCheckpointed, tc.checkpointEvery)
			if got != tc.want {
				t.Fatalf("shouldCheckpoint(%d, %d, %d) = %v, want %v",
					tc.optimizerIterations, tc.lastCheckpointed, tc.checkpointEvery, got, tc.want)
			}
		})
	}
}

// TestRunKeepsTheSearchResultWhenThePolishStageFails pins the ruling that the
// refinement is optional by construction: the search has already run by the
// time it starts, so returning its error would throw away the whole fit -- no
// preset, no render, no summary -- over a stage that may not have helped.
//
// The failure is forced through the polishRun seam because every input the
// stage takes is validated before the search starts, so there is no fixture
// that provokes a real one.
func TestRunKeepsTheSearchResultWhenThePolishStageFails(t *testing.T) {
	previous := polishRun

	t.Cleanup(func() { polishRun = previous })

	polishRun = func(context.Context, *optimizer.ObjectiveFunction, []float64, optimizer.PolishOptions) (*optimizer.PolishResult, error) {
		return nil, errors.New("engine exploded")
	}

	dir := t.TempDir()
	log := &strings.Builder{}

	outcome, err := Run(context.Background(), Spec{
		Dir:             dir,
		ReferencePath:   reference,
		Note:            69,
		Engine:          Engine{Name: EngineSimple},
		MaxIterations:   3,
		Workers:         2,
		CheckpointEvery: 1,
		Polish: &optimizer.PolishOptions{
			Engine:        optimizer.PolishEngineNelderMead,
			Sigma:         0.02,
			MaxIterations: 5,
		},
	}, log)
	if err != nil {
		t.Fatalf("expected a failed polish to be survivable, got %v", err)
	}

	if outcome.Summary.Polish != nil {
		t.Errorf("summary records a polish result the stage never produced: %+v", outcome.Summary.Polish)
	}

	if !strings.Contains(log.String(), "polish (nelder-mead) failed: engine exploded; keeping the search result") {
		t.Errorf("expected the failure to be reported, got %q", log.String())
	}

	if !strings.Contains(log.String(), "Finished: best=") {
		t.Errorf("expected the run to report its result, got %q", log.String())
	}
}
