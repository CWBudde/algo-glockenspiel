package optimizer

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/cwbudde/mayfly"
)

// mayflyAutoVariant asks the library to measure the landscape and pick a
// dialect, instead of naming one.
const mayflyAutoVariant = "auto"

// defaultClassifyEvaluations bounds what that measurement is allowed to spend.
//
// A bound is not optional here. mayfly.ClassifyProblem draws 50 samples,
// estimates a gradient at 25 more points across every dimension, and then runs
// three short searches through testConvergenceStability -- which calls Optimize
// rather than OptimizeContext, so it observes neither cancellation nor the
// caller's time budget. At this codebase's 19 encoded parameters that is a few
// thousand real audio renders before the fit has started, and nothing in the
// library would stop them.
//
// Four hundred is enough for the sampling to be meaningful while costing a
// small fraction of a typical fit.
const defaultClassifyEvaluations = 400

// classifyFraction is the share of a time budget the measurement may use, on
// top of the evaluation bound. Whichever runs out first ends it.
const classifyFraction = 0.1

// classifyMayfly measures the landscape and recommends a dialect for it.
//
// Every evaluation goes through the tracker, so the renders it spends appear in
// the run's evaluation count rather than vanishing: a caller comparing two runs
// should be able to see what choosing the dialect cost.
//
// Worth saying plainly, because the feature invites the opposite belief:
// algo-piano's audit compared all seven dialects on real audio objectives and
// found the choice to be a small effect, with OLCE only marginally ahead of
// DESMA. This budget is usually better spent on iterations.
func classifyMayfly(
	ctx context.Context,
	tracker *mayflyTracker,
	dims int,
	budget int,
	timeBudget time.Duration,
) (mayfly.ProblemCharacteristics, mayfly.AlgorithmRecommendation, int) {
	if budget <= 0 {
		budget = defaultClassifyEvaluations
	}

	limiter := &classifyLimiter{
		ctx:      ctx,
		tracker:  tracker,
		budget:   budget,
		last:     math.Inf(1),
		deadline: classifyDeadline(timeBudget),
	}

	characteristics := mayfly.ClassifyProblem(limiter.evaluate, dims, 0.0, 1.0)
	recommendation := mayfly.NewAlgorithmSelector().RecommendBest(characteristics)

	return characteristics, recommendation, limiter.spent()
}

func classifyDeadline(timeBudget time.Duration) time.Time {
	if timeBudget <= 0 {
		return time.Time{}
	}

	return time.Now().Add(time.Duration(float64(timeBudget) * classifyFraction))
}

// classifyLimiter caps what the landscape measurement may spend.
//
// Past the cap it stops rendering and returns the last cost it saw. Returning a
// constant is deliberate: it makes the remaining samples look flat, so the
// estimates degrade towards "smooth and unimodal" rather than towards noise,
// and a truncated measurement recommends a conservative dialect instead of a
// wrong one. There is no way to stop ClassifyProblem itself -- it takes no
// context -- so the objective is the only place a limit can live.
type classifyLimiter struct {
	ctx      context.Context
	tracker  *mayflyTracker
	budget   int
	deadline time.Time

	mu    sync.Mutex
	used  int
	last  float64
	stale bool
}

func (l *classifyLimiter) evaluate(position []float64) float64 {
	l.mu.Lock()

	exhausted := l.used >= l.budget || l.ctx.Err() != nil ||
		(!l.deadline.IsZero() && time.Now().After(l.deadline))
	if exhausted {
		l.stale = true
		last := l.last
		l.mu.Unlock()

		return last
	}

	l.used++
	l.mu.Unlock()

	cost := l.tracker.evaluate(position)

	l.mu.Lock()
	l.last = cost
	l.mu.Unlock()

	return cost
}

func (l *classifyLimiter) spent() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.used
}
