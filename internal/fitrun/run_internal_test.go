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

// TestWithDefaultsLeavesTheCallersReferencesAlone. Spec is passed by value,
// which makes it look as though a method on it cannot reach the caller -- but
// a slice header carries a pointer, so sorting References in place reordered
// the caller's own slice as a side effect of calling Run. A caller that built
// its references deliberately, or reads them back afterwards, has no way to
// see that happen.
func TestWithDefaultsLeavesTheCallersReferencesAlone(t *testing.T) {
	original := []ReferenceSpec{{Note: 96}, {Note: 84}, {Note: 90}}

	spec := Spec{References: original}
	resolved := spec.withDefaults()

	if original[0].Note != 96 || original[1].Note != 84 || original[2].Note != 90 {
		t.Fatalf("the caller's slice was reordered to %v", []int{
			original[0].Note, original[1].Note, original[2].Note,
		})
	}

	// And the copy is still sorted, which is what the rest of the package and
	// every table read from the run directory depend on.
	if resolved.References[0].Note != 84 || resolved.References[2].Note != 96 {
		t.Fatalf("the resolved references are not in pitch order: %v", []int{
			resolved.References[0].Note, resolved.References[1].Note, resolved.References[2].Note,
		})
	}
}
