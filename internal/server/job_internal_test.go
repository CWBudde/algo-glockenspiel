package server

// This file is package server, not server_test: it drives jobManager
// directly, reaching states an HTTP-level test either cannot reach at all or
// can only reach by racing real timing. TestHistoryCapStopsAtTheOldestRunningJob
// pushes recordLocked's cap-trimming loop past a still-running job, which
// would otherwise mean running enough real fits through the HTTP surface to
// cross maxStoredJobs. TestStartRefusesAJobThatFinishesSetupAfterStopAll pins
// a start/stopAll interleaving down to a channel handshake instead of hoping
// a sleep lands in the right place.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
)

// syntheticJob returns a fitJob with no run directory and no goroutine ever
// running it, fit only for exercising jobManager's bookkeeping. Leaving
// terminal false keeps it "running" in the sense recordLocked cares about:
// its done channel is never closed, because nothing ever calls finish.
func syntheticJob(id string, terminal bool) *fitJob {
	_, cancel := context.WithCancel(context.Background())
	job := newFitJob(id, "", fitRequest{}, 0, 0, cancel)

	if terminal {
		job.finish(fitSucceeded, nil, nil, nil, nil, nil)
	}

	return job
}

// TestHistoryCapStopsAtTheOldestRunningJob pins Finding 5 of the Task 3
// review: the trim loop in recordLocked is a while, not a slice expression,
// because it must never forget a job that is still going to report into it.
// The consequence, exercised here, is that the loop stops dead at the first
// still-running entry: everything older than it is still trimmed away, but
// nothing at or behind it is, however far past the cap the history grows,
// until that job finishes and frees the loop to resume.
func TestHistoryCapStopsAtTheOldestRunningJob(t *testing.T) {
	manager := &jobManager{}

	// The oldest entries: ordinary terminal jobs the trim loop should have no
	// trouble evicting once the history overflows the cap.
	const stale = 5
	for i := range stale {
		manager.adopt(syntheticJob(fmt.Sprintf("stale-%d", i), true))
	}

	// The one job the loop must refuse to drop, however long the history
	// grows behind it.
	running := syntheticJob("running", false)
	manager.adopt(running)

	// Enough fresh terminal jobs behind it to overflow the cap and then some,
	// so the loop is given real work and still cannot reach past running.
	const fresh = maxStoredJobs + 20
	for i := range fresh {
		manager.adopt(syntheticJob(fmt.Sprintf("fresh-%d", i), true))
	}

	if got, want := len(manager.jobs), 1+fresh; got != want {
		t.Fatalf("history length = %d, want %d (the running job plus every fresh one, never trimmed)", got, want)
	}

	if manager.jobs[0] != running {
		t.Fatal("the running job was not left as the oldest surviving entry")
	}

	for i := range stale {
		id := fmt.Sprintf("stale-%d", i)
		if _, ok := manager.byID[id]; ok {
			t.Errorf("stale job %q is still indexed after the history overflowed the cap", id)
		}
	}

	if _, ok := manager.byID["running"]; !ok {
		t.Fatal("the running job dropped out of byID despite staying in history")
	}

	// Finishing it frees the loop: the next arrival must trim the history
	// straight back down to the cap, running included.
	running.finish(fitCanceled, nil, nil, nil, nil, nil)
	manager.adopt(syntheticJob("last", true))

	if got := len(manager.jobs); got != maxStoredJobs {
		t.Fatalf("history length after the block cleared = %d, want the cap %d", got, maxStoredJobs)
	}

	if _, ok := manager.byID["running"]; ok {
		t.Fatal("the now-finished job is still indexed after the history was trimmed back to the cap")
	}
}

// TestStartRefusesAJobThatFinishesSetupAfterStopAll pins Finding 1 of the
// fix-round-1 re-review: start now does its Mkdir and setup with mu released,
// so stopAll can run its entire drain -- find the queue empty, the job not
// yet recorded, nothing to cancel -- while a start is still mid-setup on
// another goroutine. Without the m.stopped check that start's second
// critical section takes afterwards, that start would go on to record and
// enqueue a job, and spawn a worker for it, after the manager had already
// told the world it had stopped everything.
//
// setup is held open on a channel rather than raced against a sleep, so the
// interleaving this test needs -- stopAll landing inside setup's window,
// every time -- is guaranteed rather than merely likely.
func TestStartRefusesAJobThatFinishesSetupAfterStopAll(t *testing.T) {
	manager := &jobManager{}
	workDir := t.TempDir()

	setupStarted := make(chan struct{})
	releaseSetup := make(chan struct{})

	var (
		startedJob *fitJob
		startErr   error
	)

	done := make(chan struct{})

	go func() {
		defer close(done)

		startedJob, startErr = manager.start(jobDetails{}, workDir,
			func(dir string) error {
				close(setupStarted)
				<-releaseSetup

				return nil
			},
			func(ctx context.Context, job *fitJob) {})
	}()

	<-setupStarted
	manager.stopAll()
	close(releaseSetup)
	<-done

	if !errors.Is(startErr, errServerStopped) {
		t.Fatalf("start error = %v, want errServerStopped", startErr)
	}

	if startedJob != nil {
		t.Fatalf("start returned job %q for a server that had already stopped", startedJob.id)
	}

	if len(manager.jobs) != 0 {
		t.Fatalf("history has %d jobs, want none: a stopped server must never record a job it will never run or cancel", len(manager.jobs))
	}

	if manager.working {
		t.Fatal("a worker was started for a job the manager should have refused")
	}

	entries, err := os.ReadDir(workDir)
	if err != nil {
		t.Fatalf("read work dir: %v", err)
	}

	if len(entries) != 0 {
		t.Fatalf("the refused job's run directory was left behind: %v", entries)
	}
}

// TestFinishUsesTheSummarysScoreOverTheOptimizersOwnBestCost pins Finding 6 of
// the whole-phase review: fitJobListing.Score comes from Summary.Score, which
// is the post-polish cost fitrun wrote to result.json, while finish used to
// take progress.BestCost straight from optimizer.Result.BestCost, the
// pre-polish number. The two only ever agreed because nothing in this package
// sets spec.Polish yet -- the moment it does, the run list and the status
// panel would report two different scores for the same finished run.
//
// recordSummary is called before finish for every real run (see run.go), so
// this pins that ordering too: finish has to read j.summary, not take a
// second copy of the score as an argument, or the two could still drift.
func TestFinishUsesTheSummarysScoreOverTheOptimizersOwnBestCost(t *testing.T) {
	job := syntheticJob("job-1", false)

	const (
		prePolishCost  = 0.5
		postPolishCost = 0.2 // what polish actually improved the preset to.
	)

	job.recordSummary(fitrun.Summary{Score: postPolishCost})

	final := &optimizer.Result{BestCost: prePolishCost, StopReason: "max_iterations"}
	job.finish(fitSucceeded, nil, final, nil, nil, nil)

	snapshot := job.snapshot()

	if snapshot.BestCost != postPolishCost {
		t.Fatalf("snapshot.BestCost = %v, want the summary's %v (the same one the job listing reports)",
			snapshot.BestCost, postPolishCost)
	}

	if snapshot.CurrentCost != postPolishCost {
		t.Fatalf("snapshot.CurrentCost = %v, want the summary's %v", snapshot.CurrentCost, postPolishCost)
	}
}

// TestHistoryCapEvictsAnAbandonedFollowedJob is the other half of
// TestHistoryCapStopsAtTheOldestRunningJob, and the exception that keeps that
// rule from being a leak.
//
// A followed job stands for a run directory, not for a search in this process.
// One whose writer dies without leaving a result.json stays running for good,
// because following is deliberate about not guessing from a quiet trace -- so
// if the trim loop stopped at it the way it stops at a job of this server's,
// a single abandoned directory would pin every job behind it in memory for as
// long as the process lived. It is evicted instead: it is still on disk and
// still comes back on a restart, which a real running job cannot say.
func TestHistoryCapEvictsAnAbandonedFollowedJob(t *testing.T) {
	manager := &jobManager{}

	abandoned := syntheticJob("abandoned", false)
	abandoned.followed = true
	manager.adopt(abandoned)

	const fresh = maxStoredJobs + 20
	for i := range fresh {
		manager.adopt(syntheticJob(fmt.Sprintf("fresh-%d", i), true))
	}

	if got := len(manager.jobs); got != maxStoredJobs {
		t.Fatalf("history length = %d, want the cap %d: the abandoned followed job blocked the trim", got, maxStoredJobs)
	}

	if _, ok := manager.byID["abandoned"]; ok {
		t.Error("the abandoned followed job is still indexed after the history overflowed the cap")
	}

	// A running job this server started keeps its protection, which is what
	// makes this an exception rather than a repeal.
	own := syntheticJob("own", false)
	manager.adopt(own)

	for i := range fresh {
		manager.adopt(syntheticJob(fmt.Sprintf("later-%d", i), true))
	}

	if _, ok := manager.byID["own"]; !ok {
		t.Error("a running job this server started was trimmed away")
	}
}

// TestForgetDropsAFollowedJob pins what a reused run directory does to the job
// standing for its previous life: it is removed rather than left beside the
// new one under a duplicate id, and anything waiting on it is released rather
// than left waiting for a report that can never come.
func TestForgetDropsAFollowedJob(t *testing.T) {
	manager := &jobManager{}

	job := syntheticJob("run", false)
	job.followed = true
	manager.adopt(job)

	manager.forget("run")

	if manager.lookup("run") != nil {
		t.Error("a forgotten job is still in the history")
	}

	select {
	case <-job.done:
	default:
		t.Error("a forgotten job's done channel is still open, so a watcher would wait for good")
	}

	// Forgetting what is not there is not an error: the scan calls it for a
	// directory it is re-reading, which the history may already have trimmed.
	manager.forget("run")
}
