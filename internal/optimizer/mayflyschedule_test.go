package optimizer

import (
	"context"
	"strings"
	"testing"
)

func TestMayflySchedulePlanSpendsTheWholeBudget(t *testing.T) {
	tests := []struct {
		name     string
		schedule mayflySchedule
		total    int
		want     []int
	}{
		{name: "default is one round", schedule: mayflySchedule{epochs: 1}, total: 100, want: []int{100}},
		{name: "zero epochs still runs once", schedule: mayflySchedule{}, total: 40, want: []int{40}},
		{name: "even split", schedule: mayflySchedule{epochs: 4}, total: 40, want: []int{10, 10, 10, 10}},
		{name: "remainder goes to the earliest", schedule: mayflySchedule{epochs: 4}, total: 42, want: []int{11, 11, 10, 10}},
		{name: "restarts are appended", schedule: mayflySchedule{epochs: 2, restarts: 2}, total: 40, want: []int{10, 10, 10, 10}},
		{name: "more rounds than iterations", schedule: mayflySchedule{epochs: 10}, total: 3, want: []int{1, 1, 1}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.schedule.plan(test.total)
			if len(got) != len(test.want) {
				t.Fatalf("round count: got %v want %v", got, test.want)
			}

			sum := 0

			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("budgets: got %v want %v", got, test.want)
				}

				sum += got[i]
			}

			if sum != test.total && test.total >= len(test.want) {
				t.Fatalf("budget not spent exactly: got %d want %d", sum, test.total)
			}
		})
	}
}

func TestMayflyScheduleWarmRounds(t *testing.T) {
	schedule := mayflySchedule{epochs: 2, restarts: 2}

	for round, want := range []bool{true, true, false, false} {
		if got := schedule.warm(round); got != want {
			t.Fatalf("round %d: warm=%v want %v", round, got, want)
		}
	}
}

// TestMayflyEpochsKeepCumulativeCounters is the regression test for the
// counters. mayfly numbers every round's iterations from one and counts every
// round's evaluations from zero, so a scheduled run that forwarded those
// straight through would report the last round as if it were the whole run --
// and Progress.OptimizerIterations would walk backwards, which is the number
// the fit command subtracts from --max-iter when resuming.
func TestMayflyEpochsKeepCumulativeCounters(t *testing.T) {
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}, {Min: -10, Max: 10}}}
	initial := []float64{5, 5}
	objective := func(x []float64) float64 { return square(x[0]-1.25) + square(x[1]+2.5) }

	epochs := 4
	tuning := &MayflyTuning{Schedule: &MayflySchedule{Epochs: &epochs}}

	var (
		reported []int
		resolved ResolvedMayfly
	)

	result, err := (&MayflyOptimizer{
		Variant: "desma", Population: 6, Seed: 1, MaxWorkers: 1, Tuning: tuning,
		OnResolve: func(r ResolvedMayfly) { resolved = r },
	}).Optimize(context.Background(), objective, initial, bounds, OptimizeOptions{
		MaxIterations: 40,
		ReportEvery:   1,
		Report:        func(p Progress) { reported = append(reported, p.OptimizerIterations) },
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}

	if resolved.Rounds != 4 || resolved.IterationsPerRound != 10 {
		t.Fatalf("unexpected plan: rounds=%d perRound=%d", resolved.Rounds, resolved.IterationsPerRound)
	}

	if result.Iterations != 40 {
		t.Fatalf("expected the whole budget to be spent: got %d want 40", result.Iterations)
	}

	if len(reported) != 40 {
		t.Fatalf("expected one report per iteration: got %d", len(reported))
	}

	for i := range reported {
		if reported[i] != i+1 {
			t.Fatalf("iteration counter is not cumulative at %d: got %v", i, reported[:i+1])
		}
	}
}

// TestMayflyEpochsCarryTheIncumbentForward distinguishes a warm round from a
// cold one. A warm schedule can never do worse than a single round of the same
// total budget, because every round starts from what the last one found.
func TestMayflyEpochsCarryTheIncumbentForward(t *testing.T) {
	bounds := Bounds{Ranges: []Range{{Min: -10, Max: 10}, {Min: -10, Max: 10}}}
	initial := []float64{9, 9}
	objective := func(x []float64) float64 { return square(x[0]-1.25) + square(x[1]+2.5) }

	run := func(t *testing.T, tuning *MayflyTuning) *Result {
		t.Helper()

		result, err := (&MayflyOptimizer{
			Variant: "desma", Population: 6, Seed: 7, MaxWorkers: 1, Tuning: tuning,
		}).Optimize(context.Background(), objective, initial, bounds, OptimizeOptions{
			MaxIterations: 60,
		})
		if err != nil {
			t.Fatalf("Optimize failed: %v", err)
		}

		return result
	}

	single := run(t, nil)

	epochs := 6
	warm := run(t, &MayflyTuning{Schedule: &MayflySchedule{Epochs: &epochs}})

	if warm.Iterations != single.Iterations {
		t.Fatalf("the schedule changed the budget: %d vs %d", warm.Iterations, single.Iterations)
	}

	// Both must at least beat the starting point; the baseline is retained, so
	// neither can report worse than it was handed.
	if warm.BestCost > objective(initial) || single.BestCost > objective(initial) {
		t.Fatalf("a run reported worse than its starting point: warm=%g single=%g start=%g",
			warm.BestCost, single.BestCost, objective(initial))
	}

	restarts := 3

	cold := run(t, &MayflyTuning{Schedule: &MayflySchedule{Epochs: &epochs, Restarts: &restarts}})
	if cold.Iterations != single.Iterations {
		t.Fatalf("restarts changed the budget: %d vs %d", cold.Iterations, single.Iterations)
	}
}

// TestMayflyStagnationWindowWiderThanARoundIsRejected pins the audit's trap.
// The window is counted within a round, so one at least as wide as the round
// can never fire and the run silently spends its whole budget.
func TestMayflyStagnationWindowWiderThanARoundIsRejected(t *testing.T) {
	window, epochs := 20, 4

	err := (&MayflyOptimizer{
		Variant: "desma",
		Tuning: &MayflyTuning{
			Convergence: &MayflyConvergence{StagnationIterations: &window},
			Schedule:    &MayflySchedule{Epochs: &epochs},
		},
	}).Validate(40)
	if err == nil {
		t.Fatal("expected a window of 20 inside rounds of 10 to be refused")
	}

	// The message has to name both numbers: the point is that the round, not
	// the run, is what the window has to fit inside.
	for _, want := range []string{"20", "10", "round"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("message %q does not mention %q", err, want)
		}
	}

	// The same window is fine once the rounds are long enough to reach it.
	if err := (&MayflyOptimizer{
		Variant: "desma",
		Tuning:  &MayflyTuning{Convergence: &MayflyConvergence{StagnationIterations: &window}},
	}).Validate(40); err != nil {
		t.Fatalf("a window of 20 inside a single round of 40 must be accepted: %v", err)
	}
}
