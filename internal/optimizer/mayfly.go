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

	// SeedFraction and SeedSigma shape a warm round's initial population: the
	// incumbent itself, then this fraction of each population drawn around it
	// with a Gaussian of this width in unit-cube units, and the rest uniform
	// over the box. Zero takes the defaults, defaultSeedFraction and
	// defaultSeedSigma. This is CircleFit's continuation profile: a single
	// seeded individual was one in ten of the swarm, and its neighbourhood --
	// which is where a seed from the analysis says the answer is -- went
	// unsearched until crossover happened to land there.
	SeedFraction float64
	SeedSigma    float64
}

// The continuation profile's defaults. Half the population around the
// incumbent keeps the other half free to find a different basin; a width of
// 0.05 in the unit cube is a few percent of a decade of frequency, which is
// wider than the analysis's error and narrower than the gap between partials.
const (
	defaultSeedFraction = 0.5
	defaultSeedSigma    = 0.05
)

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
	// Rounds is how many consecutive searches the schedule runs, warm and cold
	// together. One is a single search, which is the default.
	Rounds int
	// IterationsPerRound is the budget of the longest round. Rounds differ by
	// at most one iteration, because the remainder goes to the earliest ones.
	IterationsPerRound int
	// Workers is the number of goroutines evaluating one generation, after a
	// zero MaxWorkers has been replaced by the machine's CPU count. It mirrors
	// ResolvedCMAES.Workers so a caller can record and restore the width
	// whichever backend it ran.
	Workers int
	// Population is the male and female swarm size the run was configured
	// with, after a Population below two has taken the default and after a
	// tuning document or a preset has had its say. It is reported for the same
	// reason ResolvedCMAES.Lambda is: a preset chooses one privately, and a
	// record that repeated the flag would name a swarm that never existed.
	Population int
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

	tracker := newMayflyTracker(objective, bounds, start, opts)
	tracker.seedBaseline(initial)

	// The schedule splits the caller's total budget into consecutive searches.
	// The config is validated against the shortest of them, because that is the
	// round a convergence window actually has to fit inside.
	schedule := scheduleFor(o.Tuning)
	budgets := iterationBudgets(schedule, opts)
	// The evaluation cap is split by the same rule, and carried as a running
	// total so that a round which stops early hands its remainder to the next
	// one instead of losing it.
	evalCaps := cumulativeEvaluationCaps(opts.MaxEvaluations, len(budgets))

	cfg, err := o.buildConfig(resolved, len(initial), shortestBudget(budgets))
	if err != nil {
		return nil, err
	}

	// A preset chooses the dialect without naming it, so read it back off the
	// configuration rather than leaving the report blank. This is idempotent for
	// the paths that did name one.
	resolved.Variant = variantNameForConfig(cfg)
	resolved.Rounds = len(budgets)
	resolved.IterationsPerRound = budgets[0]
	// buildConfig is where a zero MaxWorkers becomes the machine's CPU count,
	// so the width is read back off the configuration rather than derived a
	// second time here.
	resolved.Workers = cfg.MaxWorkers
	resolved.Population = cfg.NPop

	if o.OnResolve != nil {
		o.OnResolve(resolved)
	}

	cfg.ObjectiveFunc = tracker.evaluate

	var (
		last *mayfly.Result
		// capped records whether the evaluation cap is what ended the round
		// that ended the run. A round that finishes on its own terms clears
		// it, so the reported stop reason always describes the last round.
		capped bool
	)

	for round, budget := range budgets {
		// One deadline covers the whole schedule, so a round that would start
		// after it has passed simply does not.
		if runCtx.Err() != nil {
			break
		}

		// Nor does a round with nothing left to spend. A round is cut at the
		// first iteration boundary after it reaches its cap, so an earlier
		// round can overrun by one generation, and a share that overrun
		// swallows whole would start a round with nothing to do.
		if evalCaps[round] > 0 && tracker.evaluationCount() >= evalCaps[round] {
			capped = true

			break
		}

		// Each round anneals over its own length: several variants derive their
		// schedule from MaxIterations, so a round must be told how long it is
		// rather than how long the whole run is.
		cfg.MaxIterations = budget

		// Each round also needs its own random stream. Mayfly reads
		// Config.Seed at the start of every OptimizeContext call and builds a
		// generator from it there, so passing one seed to every round would
		// replay a single search: two cold restarts would draw the same
		// uniform population and walk the same trajectory, which is the exact
		// independence a restart exists to provide.
		//
		// Round zero keeps the resolved seed, so the seed that is reported and
		// checkpointed still reproduces the run from its beginning. Later
		// rounds mix the seed with the round rather than offsetting it, which
		// keeps them clear both of the warm-population streams below and of
		// every other run's rounds; roundStream says why an offset is not
		// enough.
		roundSeed := roundStream(resolved.Seed, round)
		cfg.Seed = &roundSeed

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

			// The two populations get their own draws so the swarm does not
			// start as pairs of identical mayflies. The stream is derived from
			// the run's seed rather than taken from cfg.Rand, which mayfly
			// owns from here on.
			rng := rand.New(rand.NewSource(warmStream(resolved.Seed, round)))
			fraction, sigma := o.seedProfile()
			options = append(options, mayfly.WithInitialPopulation(
				seedPopulation(warmSeed, cfg.NPop, fraction, sigma, rng),
				seedPopulation(warmSeed, cfg.NPopF, fraction, sigma, rng),
			))
		}

		// Mayfly has no evaluation budget of its own, so the cap is enforced
		// by the progress observer, at an iteration boundary, by cancelling a
		// context only this round can see. The library then returns its
		// context error, and the round is treated as finished rather than as
		// an abort, which is what lets the next round start.
		roundCtx, cancelRound := context.WithCancel(runCtx)
		tracker.beginRound(round, evalCaps[round], cancelRound)

		res, runErr := mayfly.OptimizeContext(roundCtx, cfg, options...)

		cancelRound()

		if runErr != nil {
			// A cancellation the caller or the deadline caused outranks the
			// cap: the run really is over, and reporting it as a spent budget
			// would hide the abort.
			if runCtx.Err() != nil || !tracker.roundWasCapped() {
				return tracker.abortedResult(ctx, runCtx, runErr)
			}

			tracker.finishCappedRound()

			capped = true
			last = nil

			continue
		}

		tracker.finishRound(res)

		capped = false
		last = res

		// The target cost is the whole run's goal, not the round's: once it is
		// met, further rounds only spend audio renders on a question already
		// answered, and a cold restart could end on maximum_iterations and
		// report the run as unconverged after it had converged. Stagnation is
		// deliberately not a reason to stop -- escaping a stagnated basin is
		// exactly what the next round is for.
		if res.TerminationReason == mayfly.TerminationTargetCost {
			break
		}
	}

	result := tracker.result(last)

	// The cap is the run's own stopping rule, not mayfly's, so the reason it
	// ended has to be written here: the library only ever reports the
	// cancellation the wrapper caused. A run cut mid-search has proved
	// nothing, whatever its last completed round looked like.
	if capped {
		result.StopReason = "max_evaluations"
		result.Converged = false
	}

	return result, nil
}

// iterationBudgets is the per-round iteration cap the schedule asks for.
//
// A run given only an evaluation cap still needs one, because mayfly rejects a
// non-positive MaxIterations, and splitting a nominal single iteration across
// the rounds would end each of them after one generation. The evaluation cap
// itself is used instead: an iteration costs at least one evaluation, so no
// round can reach that many iterations before the evaluation cap binds, which
// is the point of asking for an evaluation budget in the first place.
func iterationBudgets(schedule mayflySchedule, opts OptimizeOptions) []int {
	if opts.MaxIterations <= 0 && opts.MaxEvaluations > 0 {
		budgets := make([]int, schedule.rounds())
		for i := range budgets {
			budgets[i] = opts.MaxEvaluations
		}

		return budgets
	}

	return schedule.plan(maxInt(1, opts.MaxIterations))
}

// cumulativeEvaluationCaps turns a total evaluation budget into the running
// total a round may not exceed. Round r may spend everything rounds 0 to r
// were allotted together, so a round that stops early leaves its remainder to
// its successors rather than to nobody. A zero total disables the cap, which
// is reported as a slice of zeros so the caller needs no second branch.
func cumulativeEvaluationCaps(total, rounds int) []int {
	caps := make([]int, rounds)

	if total <= 0 {
		return caps
	}

	running := 0
	for round, share := range splitEvenly(total, rounds) {
		running += share
		caps[round] = running
	}

	return caps
}

// defaultMayflyVariant is what a run uses when neither a variant nor a preset
// was named. It is DESMA for historical reasons, and changing it would move
// every default fit.
const defaultMayflyVariant = "desma"

// mayflyEvaluationsPerIteration is what one mayfly iteration costs in
// objective evaluations. Measured on 2026-09-02 against mayfly v0.7.1: DESMA
// at a population of ten, twenty iterations on the sphere with one worker,
// spends 861 evaluations, so 43.05 each. The figure is the whole-run average
// rather than the marginal cost of an iteration, which is 42.0: the initial
// population and the wrapper's baseline evaluation are part of what a budget
// has to pay for, and a campaign sizing a run in evaluations is asking about
// the run, not about its steady state.
//
// It replaces the 47.7 the sibling algo-piano project measured, which predates
// the NC and NM defaults this wrapper now leaves to the library.
// TestMayflyEvaluationsPerIterationIsRecorded fails when a library upgrade
// moves it.
const mayflyEvaluationsPerIteration = 43.05

// MayflyEvaluationsPerIteration is the measured cost of one mayfly iteration
// in objective evaluations, for callers that have to convert between the two
// budgets before a run starts. A campaign matching backends on evaluations
// needs it to size an iteration cap, which is the only budget mayfly itself
// understands.
func MayflyEvaluationsPerIteration() float64 {
	return mayflyEvaluationsPerIteration
}

func (o *MayflyOptimizer) population() int {
	if o.Population >= 2 {
		return o.Population
	}

	return 10
}

// seedProfile returns the continuation profile, defaults filled in.
func (o *MayflyOptimizer) seedProfile() (fraction, sigma float64) {
	fraction, sigma = o.SeedFraction, o.SeedSigma

	if fraction <= 0 {
		fraction = defaultSeedFraction
	}

	if sigma <= 0 {
		sigma = defaultSeedSigma
	}

	return math.Min(1, fraction), sigma
}

// seedPopulation builds the rows of a warm round's initial population in the
// unit cube: the incumbent first, then enough Gaussian draws around it to
// fill fraction of a population of size, each clamped into the cube. The
// rows mayfly is not given it draws uniformly itself, so the rest of the
// population is left to it.
func seedPopulation(incumbent []float64, size int, fraction, sigma float64, rng *rand.Rand) [][]float64 {
	rows := int(math.Ceil(fraction * float64(size)))
	if rows < 1 {
		rows = 1
	}

	if rows > size {
		rows = size
	}

	population := make([][]float64, rows)

	population[0] = append([]float64(nil), incumbent...)

	for i := 1; i < rows; i++ {
		row := make([]float64, len(incumbent))
		for j, value := range incumbent {
			row[j] = math.Max(0, math.Min(1, value+sigma*rng.NormFloat64()))
		}

		population[i] = row
	}

	return population
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
) (*mayfly.Config, error) {
	cfg, err := newMayflyConfig(resolved, o.population(), dims, iters, o.Tuning)
	if err != nil {
		return nil, err
	}

	// Config.Seed rather than Config.Rand: the two are mutually exclusive from
	// v0.7.0 on, and a seed is what Result.Seed can honestly report back. A
	// scheduled run overwrites this per round, which is why the pointer is to a
	// copy rather than into the caller's ResolvedMayfly.
	seed := resolved.Seed
	cfg.Seed = &seed

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
	_, err = o.buildConfig(resolved, 1, scheduleFor(o.Tuning).shortestRound(maxInt(1, maxIterations)))

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
) (*mayfly.Config, error) {
	cfg, variant, err := baseMayflyConfig(resolved)
	if err != nil {
		return nil, err
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
	// which is the value the whole 0.5.x behaviour was tuned against. An
	// iteration costs mayflyEvaluationsPerIteration renders at a population of
	// ten, and the sibling algo-piano project found that at a fixed evaluation
	// budget cheaper iterations win.
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
	case cfg.UseHMMA:
		return "hmma"
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

	// evalCap is the running total the current round may not exceed, and
	// cancelRound stops that round once it does. The cancel is dropped after
	// it fires so a later round cannot be cut by a stale one, which makes the
	// cap path idempotent. capped records that the cap, rather than the
	// caller, is what cancelled the round.
	evalCap     int
	cancelRound context.CancelFunc
	capped      bool

	// round is the zero-based index of the schedule round in progress, set by
	// beginRound and read back into Progress.Restart, per the field's own
	// contract ("the zero-based index of the search in progress"). Mayfly's
	// restarts are the schedule's rounds, so this is that index rather than a
	// count the tracker derives some other way.
	round int
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

// invalidCost is what a non-finite objective value is reported to mayfly as.
//
// The objective returns +Inf for a vector that fails to decode or validate, and
// from v0.7.0 on mayfly aborts a search whose entire initial population is
// non-finite with ErrNoFiniteObjectiveValue. A single non-finite candidate in
// an otherwise finite population is harmless -- the library scores it
// math.MaxFloat64 and it simply never wins -- but a cold restart draws a fresh
// uniform population, and one that lands wholly inside an invalid region would
// fail the whole run and discard everything the earlier rounds found.
//
// A large finite penalty keeps that round running and losing. The value has to
// sit strictly below math.MaxFloat64, because that exact value is the sentinel
// mayfly.go:395 tests for when it decides an initial population produced
// nothing usable; a quarter of it is far above any cost this objective can
// return, so a penalised candidate still ranks last against every real one. The
// best-so-far bookkeeping above compares the raw cost rather than this one, so
// such a candidate can never be reported as the answer either.
const invalidCost = math.MaxFloat64 / 4

func (t *mayflyTracker) evaluate(pos []float64) float64 {
	actual, err := t.bounds.Denormalize(pos)
	if err != nil {
		return invalidCost
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
		return invalidCost
	}

	return cost
}

// observe forwards mayfly's per-iteration progress. Progress.Iteration counts
// the callbacks this wrapper emits, not mayfly's iterations, per the Progress
// contract.
//
// The cadence is taken on the run's own iteration count rather than on
// mayfly's. Progress.Iteration restarts at one for every OptimizeContext call,
// and a schedule splits MaxIterations across epochs and restarts, so a round
// shorter than ReportEvery would never satisfy the modulo: 100 iterations over
// four epochs and eight restarts is twelve rounds of eight or nine, which at
// the default cadence of ten reports nothing at all for the whole fit. Gating
// on the global count is also what the simple backend does -- gonum's
// stats.MajorIterations is monotonic across the search -- so the two backends
// now mean the same thing by "every n".
func (t *mayflyTracker) observe(progress mayfly.Progress) {
	t.mu.Lock()

	t.iterations = t.completedIterations + progress.Iteration

	t.enforceEvaluationCap()

	if t.opts.Report == nil || t.opts.ReportEvery <= 0 || t.iterations%t.opts.ReportEvery != 0 {
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
		Restart:             t.round,
	}

	t.mu.Unlock()

	t.opts.Report(update)
}

// enforceEvaluationCap ends the round once it has spent its share, and must be
// called with the mutex held.
//
// It is deliberately not called from the objective adapter, which is where the
// count is kept. The adapter runs on whichever worker goroutine took the
// candidate, and mayfly checks its context before every item of a parallel
// batch, so cancelling from there cuts the round after however many candidates
// the scheduler happened to start: a run at eight workers spent 1207 of a
// 1200-evaluation budget where a run at one worker spent 1215, from the same
// seed, and with a schedule the divergence then seeds the next round. The
// progress observer instead runs synchronously on the goroutine that called
// OptimizeContext, at an iteration boundary, so the cut lands after a whole
// generation and is the same at every worker count.
//
// The overrun that buys is one iteration, which is what the shared contract
// allows. The exception is a cap smaller than a round's initial population,
// where the first iteration cannot be observed before it has been paid for; no
// budget worth running is that small.
func (t *mayflyTracker) enforceEvaluationCap() {
	if t.evalCap <= 0 || t.evals < t.evalCap || t.cancelRound == nil {
		return
	}

	t.capped = true

	t.cancelRound()

	t.cancelRound = nil
}

// beginRound arms the evaluation cap for the round about to start and records
// its index, so the progress reported during it carries the right restart
// number. The cap is the running total, so a round that inherits an unspent
// remainder is simply allowed to run further before it fires.
func (t *mayflyTracker) beginRound(round, evalCap int, cancel context.CancelFunc) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.round = round
	t.evalCap = evalCap
	t.cancelRound = cancel
	t.capped = false
}

// roundWasCapped reports whether the evaluation cap is what cancelled the
// round that just ended, as opposed to the caller or the deadline.
func (t *mayflyTracker) roundWasCapped() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.capped
}

// evaluationCount is what the run has spent so far, including the baseline
// evaluation of the caller's starting point.
func (t *mayflyTracker) evaluationCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.evals
}

// finishCappedRound folds a round the cap cut short into the run's totals.
// Mayfly returns no result for a cancelled round, so the iteration count is
// the one the progress observer last saw, and the evaluations are already
// counted by the adapter rather than read back off a result.
func (t *mayflyTracker) finishCappedRound() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.completedIterations = t.iterations
	t.cancelRound = nil
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
	t.cancelRound = nil
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
