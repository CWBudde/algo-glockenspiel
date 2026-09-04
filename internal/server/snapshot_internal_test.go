package server

// This file is package server, not server_test: it pins the two derived
// quantities of a snapshot against inputs an HTTP-level test cannot produce.
// A request the parser accepts always carries an iteration cap and a time
// budget, so "no budget at all" is only reachable by calling the function; and
// which backend a progress report's Restart belongs to is a decision made
// while the job is being read, not something a two-iteration fit will
// demonstrate by reaching a second round.

import (
	"context"
	"testing"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

func TestBudgetFractionIsTheTightestBindingBudget(t *testing.T) {
	minute := time.Minute

	tests := []struct {
		name          string
		iterations    int
		elapsed       time.Duration
		maxIterations int
		budget        time.Duration
		want          float64
	}{
		{
			name: "no budget at all is nothing spent",
			// Not merely unreachable through the HTTP surface: a run with
			// neither budget has no fraction to report, and reporting zero is
			// what says so.
			iterations: 40, elapsed: 20 * time.Second, want: 0,
		},
		{
			name:       "an iteration cap alone",
			iterations: 25, elapsed: time.Second, maxIterations: 100, want: 0.25,
		},
		{
			name:    "a time budget alone",
			elapsed: 15 * time.Second, budget: minute, want: 0.25,
		},
		{
			name: "the tighter of the two wins",
			// Half the iterations in a tenth of the time: the iteration cap is
			// what this run will stop on, so it is the fraction that matters.
			iterations: 50, elapsed: 6 * time.Second, maxIterations: 100, budget: minute, want: 0.5,
		},
		{
			name:       "a finished run is capped at one",
			iterations: 101, elapsed: 2 * minute, maxIterations: 100, budget: minute, want: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := budgetFraction(test.iterations, test.elapsed, test.maxIterations, test.budget)
			if got != test.want {
				t.Fatalf("budgetFraction = %v, want %v", got, test.want)
			}
		})
	}
}

func TestEvaluationsPerSecondIsZeroBeforeTheClockMoves(t *testing.T) {
	if got := evaluationsPerSecond(100, 0); got != 0 {
		t.Fatalf("evaluationsPerSecond with no elapsed time = %v, want 0", got)
	}

	if got := evaluationsPerSecond(100, 2*time.Second); got != 50 {
		t.Fatalf("evaluationsPerSecond = %v, want 50", got)
	}
}

// The mayfly tracker and the CMA-ES restart ladder both write the same field
// of optimizer.Progress, and they do not mean the same thing by it. The
// snapshot has to say which, or a reader counting restarts on a mayfly run
// would be reporting rounds as failed searches.
func TestTheRestartCounterLandsInTheFieldItsBackendMeans(t *testing.T) {
	tests := []struct {
		optimizer   string
		wantRestart int
		wantEpoch   int
	}{
		{optimizer: mayflyOptimizerName, wantEpoch: 3},
		{optimizer: cmaesOptimizerName, wantRestart: 3},
		{optimizer: "simple", wantRestart: 3},
	}

	for _, test := range tests {
		t.Run(test.optimizer, func(t *testing.T) {
			_, cancel := context.WithCancel(context.Background())
			defer cancel()

			job := newFitJob("job", "", fitRequest{Optimizer: test.optimizer}, 8000, 1, cancel)
			job.report(optimizer.Progress{Restart: 3}, nil)

			snapshot := job.snapshot()
			if snapshot.Restart != test.wantRestart || snapshot.Epoch != test.wantEpoch {
				t.Fatalf("%s reported restart=%d epoch=%d, want %d and %d",
					test.optimizer, snapshot.Restart, snapshot.Epoch, test.wantRestart, test.wantEpoch)
			}
		})
	}
}
