package optimizer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cwbudde/mayfly"
)

// MayflyOptimizer wraps github.com/cwbudde/mayfly behind the shared optimizer interface.
type MayflyOptimizer struct {
	Variant    string
	Population int

	// Seed selects the random stream. Zero means "pick one and report it"
	// rather than "be unreproducible": see resolveSeed.
	Seed int64

	// Preset names one of mayfly's ConfigPresets, which picks a dialect and a
	// starting set of knobs together. Empty selects none. It is mutually
	// exclusive with an explicit Variant, because a preset already chose one.
	Preset string

	// Tuning overrides individual knobs on top of whatever the variant factory
	// or the preset produced. Nil applies nothing, which is what keeps an
	// untuned run identical to one configured before tuning existed.
	Tuning *MayflyTuning

	// MaxWorkers bounds parallel objective evaluation. Zero selects
	// runtime.NumCPU(); one disables parallelism entirely. Parallel evaluation
	// is safe because ObjectiveFunction.Objective hands out per-goroutine
	// render state.
	MaxWorkers int

	// OnResolve reports the settings a run actually chose, once they are known
	// and before the search starts. A nil callback disables the report.
	//
	// It exists because those choices were invisible: a zero seed was resolved
	// inside the library and discarded here, so the run could not be repeated
	// and a resumed run had no stream to continue.
	OnResolve func(ResolvedMayfly)
}

// ResolvedMayfly is what a run settled on once every "choose one for me" input
// has been resolved. The CLI prints it and records the seed in the checkpoint,
// and the HTTP and WASM front ends echo it in their progress snapshots.
type ResolvedMayfly struct {
	// Variant is the dialect the run uses, after defaulting, alias resolution
	// and any preset.
	Variant string
	// Preset is the mayfly ConfigPreset the run started from, or empty.
	Preset string
	// Seed is the value the run's generator was constructed from, never zero.
	Seed int64
	// Recommendation is why an automatically chosen dialect was chosen, and is
	// empty unless the variant was "auto".
	Recommendation string
	// Confidence is how sure the selector was, in [0,1].
	Confidence float64
	// ClassifyEvaluations is what measuring the landscape cost. Those
	// evaluations are included in the run's total.
	ClassifyEvaluations int
	// Rounds is how many consecutive searches the schedule runs, warm and cold
	// together. One is a single search, which is the default.
	Rounds int
	// IterationsPerRound is the budget of the longest round. Rounds differ by
	// at most one iteration, because the remainder goes to the earliest ones.
	IterationsPerRound int
}

// Optimize runs Mayfly in a normalized [0,1] search space and maps candidates back into bounds.
func (o *MayflyOptimizer) Optimize(ctx context.Context, objective ObjectiveFunc, initial []float64, bounds Bounds, opts OptimizeOptions) (*Result, error) {
	if objective == nil {
		return nil, fmt.Errorf("objective cannot be nil")
	}

	if len(initial) == 0 {
		return nil, fmt.Errorf("initial parameters cannot be empty")
	}

	if err := bounds.CheckVector(initial); err != nil {
		return nil, err
	}

	if ctx == nil {
		ctx = context.Background()
	}

	start := time.Now()

	initial, err := bounds.Clamp(initial)
	if err != nil {
		return nil, err
	}

	seed, err := bounds.Normalize(initial)
	if err != nil {
		return nil, err
	}

	// Resolve every "choose one for me" input before the config is built, so
	// the run and the report cannot disagree about what it used.
	resolved, err := o.resolve()
	if err != nil {
		return nil, err
	}

	// The time budget is expressed as a derived context so that mayfly stops
	// the run itself. The previous approach - returning bestCost+1 from the
	// objective past the deadline - fed a moving, fabricated cost back into
	// DESMA's elite selection and search-range adaptation.
	runCtx := ctx

	if opts.TimeBudget > 0 {
		var cancel context.CancelFunc

		runCtx, cancel = context.WithTimeout(ctx, opts.TimeBudget)
		defer cancel()
	}

	// The tracker is created before the configuration because choosing a
	// dialect automatically evaluates the objective, and those evaluations have
	// to be counted like any other.
	tracker := newMayflyTracker(objective, bounds, start, opts)
	tracker.seedBaseline(initial)

	var characteristics *mayfly.ProblemCharacteristics

	if resolved.Variant == mayflyAutoVariant {
		measured, recommendation, spent := classifyMayfly(
			runCtx, tracker, len(initial), o.classifyEvaluations(), opts.TimeBudget,
		)

		characteristics = &measured
		resolved.Variant = strings.ToLower(recommendation.Variant.Name())
		resolved.Recommendation = recommendation.Reasoning
		resolved.Confidence = recommendation.Confidence
		resolved.ClassifyEvaluations = spent
	}

	// The schedule splits the caller's total budget into consecutive searches.
	// The config is validated against the shortest of them, because that is the
	// round a convergence window actually has to fit inside.
	schedule := scheduleFor(o.Tuning)
	budgets := schedule.plan(maxInt(1, opts.MaxIterations))

	cfg, err := o.buildConfig(resolved, len(initial), schedule.shortestRound(maxInt(1, opts.MaxIterations)), characteristics)
	if err != nil {
		return nil, err
	}

	resolved.Rounds = len(budgets)
	resolved.IterationsPerRound = budgets[0]

	if o.OnResolve != nil {
		o.OnResolve(resolved)
	}

	cfg.ObjectiveFunc = tracker.evaluate

	var last *mayfly.Result

	for round, budget := range budgets {
		// One deadline covers the whole schedule, so a round that would start
		// after it has passed simply does not.
		if runCtx.Err() != nil {
			break
		}

		// Each round anneals over its own length: several variants derive their
		// schedule from MaxIterations, so a round must be told how long it is
		// rather than how long the whole run is.
		cfg.MaxIterations = budget

		options := []mayfly.RunOption{mayfly.WithProgressObserver(tracker.observe)}

		// A cold round starts from a uniformly random population on purpose --
		// that is what makes it independent of the basin the warm rounds are
		// in. Warm rounds carry the incumbent: the first round carries the
		// caller's starting point, without which the preset or the resumed
		// checkpoint would be thrown away, and every later one carries the best
		// found so far.
		if schedule.warm(round) {
			warmSeed := seed
			if round > 0 {
				warmSeed, err = tracker.normalizedBest()
				if err != nil {
					return nil, err
				}
			}

			options = append(options, mayfly.WithInitialPopulation(
				[][]float64{warmSeed}, [][]float64{warmSeed},
			))
		}

		res, runErr := mayfly.OptimizeContext(runCtx, cfg, options...)
		if runErr != nil {
			return tracker.abortedResult(ctx, runCtx, runErr)
		}

		tracker.finishRound(res)

		last = res
	}

	return tracker.result(last), nil
}

// defaultMayflyVariant is what a run uses when neither a variant nor a preset
// was named. It is DESMA for historical reasons, and changing it would move
// every default fit.
const defaultMayflyVariant = "desma"

// classifyEvaluations is the cap the tuning document set on automatic dialect
// selection, or zero to use the default.
func (o *MayflyOptimizer) classifyEvaluations() int {
	if o.Tuning == nil || o.Tuning.Schedule == nil || o.Tuning.Schedule.ClassifyEvals == nil {
		return 0
	}

	return *o.Tuning.Schedule.ClassifyEvals
}

func (o *MayflyOptimizer) population() int {
	if o.Population >= 2 {
		return o.Population
	}

	return 10
}

// resolveSeed turns a zero seed into a concrete one.
//
// Leaving cfg.Rand nil and letting mayfly pick is not equivalent: it reports
// its choice in Result.Seed, which upstream documents as the time-based
// fallback and which therefore says nothing about a caller-supplied generator.
// Resolving here gives one value that is both used and reportable.
func (o *MayflyOptimizer) resolveSeed() int64 {
	if o.Seed != 0 {
		return o.Seed
	}

	return time.Now().UnixNano()
}

// resolve settles the variant, the preset and the seed.
//
// The tuning document may name a variant or a preset so that a single file
// describes a whole run, but an explicitly configured one wins: the front ends
// set those fields only when the caller asked for them by name, so a struct
// value is always a deliberate choice and a document value is a default.
func (o *MayflyOptimizer) resolve() (ResolvedMayfly, error) {
	resolved := ResolvedMayfly{
		Variant: strings.ToLower(strings.TrimSpace(o.Variant)),
		Preset:  strings.ToLower(strings.TrimSpace(o.Preset)),
		Seed:    o.resolveSeed(),
	}

	if t := o.Tuning; t != nil {
		if resolved.Variant == "" && t.Variant != nil {
			resolved.Variant = strings.ToLower(strings.TrimSpace(*t.Variant))
		}

		if resolved.Preset == "" && t.Preset != nil {
			resolved.Preset = strings.ToLower(strings.TrimSpace(*t.Preset))
		}
	}

	// A preset picks a dialect of its own, so honouring both would apply half
	// of each and match neither.
	if resolved.Preset != "" && resolved.Variant != "" {
		return ResolvedMayfly{}, fmt.Errorf(
			"mayfly preset %q already selects a variant, so it cannot be combined with variant %q",
			resolved.Preset, resolved.Variant,
		)
	}

	if resolved.Preset == "" && resolved.Variant == "" {
		resolved.Variant = defaultMayflyVariant
	}

	return resolved, nil
}

func (o *MayflyOptimizer) buildConfig(
	resolved ResolvedMayfly,
	dims, iters int,
	characteristics *mayfly.ProblemCharacteristics,
) (*mayfly.Config, error) {
	cfg, err := newMayflyConfig(resolved, o.population(), dims, iters, o.Tuning, characteristics)
	if err != nil {
		return nil, err
	}

	cfg.Rand = rand.New(rand.NewSource(resolved.Seed))

	workers := o.MaxWorkers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	cfg.MaxWorkers = workers
	cfg.EnableParallel = workers > 1

	return cfg, nil
}

// Validate builds the configuration a run would use and checks it, without
// running anything.
//
// It exists for callers that decide whether to accept a request before they
// book the work it names. Without it the request is first checked inside
// Optimize, which for the HTTP fit API means a malformed request is accepted,
// claims the single fit slot, and fails asynchronously a moment later instead
// of being rejected as the bad request it always was.
//
// It takes the iteration budget because upstream validates the convergence
// block against it -- a minimum-iterations setting above the cap is an error --
// so a configuration cannot be checked without knowing the budget. The problem
// size is not needed: no validated setting depends on dimensionality, and the
// callers that need this answer know the budget but not yet the encoded
// parameter count.
func (o *MayflyOptimizer) Validate(maxIterations int) error {
	resolved, err := o.resolve()
	if err != nil {
		return err
	}

	// Validate against the shortest round for the same reason the run does: a
	// convergence window has to fit inside a round, not inside the whole
	// budget, so checking against the total would accept a window that can
	// never fire.
	// A request for "auto" is accepted without measuring anything: the dialect
	// is chosen from the objective, which does not exist yet. Every setting
	// that is not the dialect is still checked, against the default dialect.
	if resolved.Variant == mayflyAutoVariant {
		resolved.Variant = defaultMayflyVariant
	}

	_, err = o.buildConfig(resolved, 1, scheduleFor(o.Tuning).shortestRound(maxInt(1, maxIterations)), nil)

	return err
}

// resolveVariant looks a variant up in the upstream registry, which is the
// single source of truth for variant names, so new variants are picked up
// without touching this wrapper.
func resolveVariant(name string) (mayfly.AlgorithmVariant, error) {
	selected := mayfly.NewVariant(name)
	if selected == nil {
		// ListVariants ranges over a map, so the order it returns is not
		// stable. Sorting keeps the error message reproducible, which matters
		// because it is asserted on in tests and read by users.
		names := mayfly.ListVariants()
		sort.Strings(names)

		return nil, fmt.Errorf("unsupported mayfly variant %q, want one of %s",
			name, strings.Join(names, ", "))
	}

	return selected, nil
}

// newMayflyConfig builds the configuration for one run.
//
// The order matters and is the whole contract of the tuning surface:
//
//  1. the base, from a preset or from the variant factory;
//  2. the problem shape, which describes the search space and the budget
//     rather than the search, and so is not tunable at all;
//  3. the scalar settings the caller passed as fields;
//  4. the tuning document, last, so one sentence describes precedence: a
//     written key wins over everything above it.
//
// Step 2 lands after step 1, so a preset's own MaxIterations and population --
// fast_convergence sets 300 iterations, high_dimensional a population of 40 --
// are replaced by the run's budget and --mayfly-pop. A preset selects a dialect
// and its knobs here, not the size of the run.
func newMayflyConfig(
	resolved ResolvedMayfly,
	pop, dims, iters int,
	tuning *MayflyTuning,
	characteristics *mayfly.ProblemCharacteristics,
) (*mayfly.Config, error) {
	cfg, variant, err := baseMayflyConfig(resolved)
	if err != nil {
		return nil, err
	}

	// Auto-tuning runs here, between the base and the problem shape, so its
	// per-dialect adjustments land while its population and iteration
	// adjustments are overwritten by the run's own budget below. Those two are
	// not the library's to choose: the caller asked for a budget.
	if characteristics != nil {
		mayfly.AutoTuneConfig(cfg, *characteristics)
	}

	cfg.ProblemSize = dims
	cfg.LowerBound = 0.0
	cfg.UpperBound = 1.0
	cfg.MaxIterations = iters
	cfg.NPop = pop
	cfg.NPopF = pop

	// NC and NM are deliberately left alone. This used to force NC = 2*pop and
	// NM = 5% of pop, which predate mayfly v0.5.0 giving NC a default of NCAuto
	// with an NCRatio of 1.0.
	//
	// NC = 2*pop sits exactly on the limit validateOffspring enforces -- it
	// means every male mates every iteration -- and so doubled the offspring
	// evaluations of an iteration against the library's own considered choice,
	// which is the value the whole 0.5.x behaviour was tuned against. The
	// sibling algo-piano project measured the cost of an iteration at roughly
	// 47.7 evaluations at a population of ten, and found that at a fixed
	// evaluation budget cheaper iterations win.
	//
	// NM differed from mayfly's own 5% rule only in rounding up to one, and
	// only below a population of ten. A run that wants the old numbers back
	// asks for them: {"nc": 20, "nm": 1} at a population of ten.

	if err := tuning.Apply(cfg, variant); err != nil {
		return nil, err
	}

	if err := validateConvergenceWindow(cfg, iters); err != nil {
		return nil, err
	}

	if err := validateMayflyConfig(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// baseMayflyConfig produces the starting configuration and the canonical short
// name of the dialect it selected. The name is what decides which of the
// document's per-variant knobs are in scope, so it has to come from whatever
// actually chose the dialect rather than from the caller's spelling of it.
func baseMayflyConfig(resolved ResolvedMayfly) (*mayfly.Config, string, error) {
	if resolved.Preset != "" {
		cfg, err := mayfly.NewPresetConfig(mayfly.ConfigPreset(resolved.Preset))
		if err != nil {
			return nil, "", fmt.Errorf("%w, want one of %s", err, strings.Join(sortedMayflyPresets(), ", "))
		}

		return cfg, variantNameForConfig(cfg), nil
	}

	selected, err := resolveVariant(resolved.Variant)
	if err != nil {
		return nil, "", err
	}

	cfg := selected.GetConfig()

	return cfg, variantNameForConfig(cfg), nil
}

// variantNameForConfig reads back the dialect a config selected. Mayfly encodes
// the choice as one Use* flag rather than a name, and a preset never reports
// which dialect it picked, so this is the only way to learn it.
func variantNameForConfig(cfg *mayfly.Config) string {
	switch {
	case cfg.UseDESMA:
		return "desma"
	case cfg.UseOLCE:
		return "olce"
	case cfg.UseEOBBMA:
		return "eobbma"
	case cfg.UseGSASMA:
		return "gsasma"
	case cfg.UseMPMA:
		return "mpma"
	case cfg.UseAOBLMOA:
		return "aoblmoa"
	default:
		return "ma"
	}
}

// sortedMayflyPresets names the presets a run may select. ListPresets returns a
// map, so the order it iterates in is not stable and an error message built
// straight from it would differ between runs.
func sortedMayflyPresets() []string {
	presets := mayfly.ListPresets()

	names := make([]string, 0, len(presets))
	for name := range presets {
		names = append(names, string(name))
	}

	sort.Strings(names)

	return names
}

// validateConvergenceWindow rejects a stagnation window that cannot be reached.
//
// The window is counted within a single round, so one at least as wide as the
// round is not a conservative setting: it can never fire, and the run silently
// spends its whole budget while the caller believes early stopping is armed.
// algo-piano shipped exactly that for a while -- its audit records the flag as
// a measured non-effect, because its rounds were always too short.
//
// Upstream also rejects a minimum above the cap, but its message names
// max_iterations, which is the round's budget here rather than the run's. The
// difference is the whole point of the schedule, so it is worth saying.
func validateConvergenceWindow(cfg *mayfly.Config, iters int) error {
	if cfg.Convergence == nil {
		return nil
	}

	if window := cfg.Convergence.StagnationIterations; window >= iters {
		return fmt.Errorf(
			"convergence stagnation_iterations %d can never fire inside a round of %d iterations: "+
				"lower it, raise the iteration budget, or use fewer rounds",
			window, iters,
		)
	}

	if minimum := cfg.Convergence.MinIterations; minimum > iters {
		return fmt.Errorf(
			"convergence min_iterations %d exceeds the %d iterations of a round: "+
				"lower it, raise the iteration budget, or use fewer rounds",
			minimum, iters,
		)
	}

	return nil
}

// validateMayflyConfig rejects configurations up front.
//
// Upstream's ValidateConfig owns every range in mayfly.Config -- the shared
// coefficients, the per-variant knobs, the convergence block, and the mating
// rules in validateOffspring that the check here used to approximate -- so the
// only thing left to police is what upstream deliberately allows and this
// wrapper does not: a population of one. A swarm that cannot mate is not a
// search, and the fit command already refuses it at the flag, so accepting it
// here would only let the HTTP and WASM front ends book a run that can never
// make progress.
//
// ValidateConfig tolerates a nil ObjectiveFunc, which is what lets it run at
// config-build time, before Optimize installs the tracker's evaluator.
func validateMayflyConfig(cfg *mayfly.Config) error {
	if cfg.NPop < 2 || cfg.NPopF < 2 {
		return fmt.Errorf("population must be at least 2, got %d males and %d females", cfg.NPop, cfg.NPopF)
	}

	return mayfly.ValidateConfig(cfg)
}

// mayflyTracker owns every mutable value shared with the library. Parallel
// evaluation calls evaluate from several goroutines at once, so the best-so-far
// state and the evaluation counter must be guarded.
type mayflyTracker struct {
	objective ObjectiveFunc
	bounds    Bounds
	start     time.Time
	opts      OptimizeOptions

	mu         sync.Mutex
	bestParams []float64
	bestCost   float64
	evals      int
	iterations int
	reports    int

	// completedIterations and libraryEvals accumulate what earlier rounds
	// already spent. mayfly numbers each round's iterations from one and counts
	// each round's evaluations from zero, so without these a scheduled run
	// would report the last round's figures as if they were the whole run's.
	completedIterations int
	libraryEvals        int
}

func newMayflyTracker(objective ObjectiveFunc, bounds Bounds, start time.Time, opts OptimizeOptions) *mayflyTracker {
	return &mayflyTracker{
		objective: objective,
		bounds:    bounds,
		start:     start,
		opts:      opts,
		bestCost:  math.Inf(1),
	}
}

// seedBaseline evaluates the caller's starting point so a run can never report
// a result worse than what it was given.
func (t *mayflyTracker) seedBaseline(initial []float64) {
	cost := t.objective(initial)

	t.mu.Lock()
	defer t.mu.Unlock()

	t.evals++

	t.bestParams = append([]float64(nil), initial...)
	t.bestCost = cost
}

func (t *mayflyTracker) evaluate(pos []float64) float64 {
	actual, err := t.bounds.Denormalize(pos)
	if err != nil {
		return math.Inf(1)
	}

	cost := t.objective(actual)

	t.mu.Lock()
	t.evals++

	if cost < t.bestCost {
		t.bestCost = cost
		t.bestParams = append(t.bestParams[:0], actual...)
	}

	t.mu.Unlock()

	if !isFinite(cost) {
		return math.Inf(1)
	}

	return cost
}

// observe forwards mayfly's per-iteration progress. Progress.Iteration counts
// the callbacks this wrapper emits, not mayfly's iterations, per the Progress
// contract.
func (t *mayflyTracker) observe(progress mayfly.Progress) {
	t.mu.Lock()

	t.iterations = t.completedIterations + progress.Iteration

	if t.opts.Report == nil || t.opts.ReportEvery <= 0 || progress.Iteration%t.opts.ReportEvery != 0 {
		t.mu.Unlock()

		return
	}

	t.reports++
	update := Progress{
		Iteration:           t.reports,
		OptimizerIterations: t.iterations,
		CurrentCost:         progress.Best.Cost,
		BestCost:            t.bestCost,
		BestParams:          append([]float64(nil), t.bestParams...),
		Elapsed:             time.Since(t.start),
		Evaluations:         t.evals,
	}

	t.mu.Unlock()

	t.opts.Report(update)
}

// finishRound folds one completed round's totals into the run's, so the next
// round's per-round numbering continues where this one stopped.
func (t *mayflyTracker) finishRound(res *mayfly.Result) {
	if res == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.completedIterations += res.IterationCount
	t.libraryEvals += res.FuncEvalCount
	t.iterations = t.completedIterations
}

// normalizedBest is the running best expressed in the unit cube mayfly
// searches, ready to seed the next warm round.
func (t *mayflyTracker) normalizedBest() ([]float64, error) {
	t.mu.Lock()
	best := append([]float64(nil), t.bestParams...)
	t.mu.Unlock()

	return t.bounds.Normalize(best)
}

func (t *mayflyTracker) result(res *mayfly.Result) *Result {
	t.mu.Lock()
	defer t.mu.Unlock()

	iterations := t.iterations
	evals := t.evals
	reason := "unknown"
	converged := false

	// The totals are the run's, not the last round's: a scheduled run spans
	// several searches and the caller asked for one budget, not several.
	if t.libraryEvals > evals {
		evals = t.libraryEvals
	}

	if res != nil {
		reason = string(res.TerminationReason)

		// A metaheuristic never proves convergence; the only honest signal is
		// that the run stopped for a convergence criterion instead of
		// exhausting its iteration budget. With a schedule this describes the
		// final round, which is the one that decided when the run ended.
		converged = res.TerminationReason == mayfly.TerminationTargetCost ||
			res.TerminationReason == mayfly.TerminationStagnation
	}

	return &Result{
		BestParams:  append([]float64(nil), t.bestParams...),
		BestCost:    t.bestCost,
		Iterations:  iterations,
		Elapsed:     time.Since(t.start),
		Converged:   converged,
		StopReason:  reason,
		Evaluations: evals,
	}
}

// abortedResult reports the best solution found before cancellation. Mayfly
// returns a nil result plus the context error in that case, but the caller
// still wants whatever the truncated run achieved.
func (t *mayflyTracker) abortedResult(ctx, runCtx context.Context, runErr error) (*Result, error) {
	if runCtx.Err() == nil {
		return nil, runErr
	}

	res := t.result(nil)

	switch {
	case ctx.Err() != nil:
		res.StopReason = "context_canceled"
	case errors.Is(runErr, context.DeadlineExceeded):
		res.StopReason = "time_budget"
	default:
		res.StopReason = "canceled"
	}

	return res, nil
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}

	return b
}
