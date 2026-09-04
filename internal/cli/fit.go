package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/cwbudde/algo-glockenspiel/internal/fitschema"
	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/spf13/cobra"
)

type fitOptions struct {
	referencePath   string
	presetPath      string
	boundsPath      string
	align           bool
	normalizeGain   bool
	outputPath      string
	note            int
	velocity        int
	sampleRate      int
	optimizerName   string
	maxIter         int
	timeBudget      time.Duration
	reportEvery     int
	checkpointEvery int
	workDir         string
	resume          bool
	metric          string
	modes           int
	mayflyVariant   string
	mayflyPreset    string
	mayflyPop       int
	cpuProfilePath  string

	// seed is the one random stream selector every backend uses: Mayfly,
	// CMA-ES and the polish stage. Zero means "choose one and report it", and
	// the OnResolve callbacks replace it with the value the run drew, so the
	// checkpoint records the stream that actually ran. --mayfly-seed and
	// --cmaes-seed are deprecated aliases bound to this same field.
	seed int64

	// workers bounds parallel objective evaluation in every backend. Zero
	// follows the machine's CPU count, and the resolved width is written into
	// the checkpoint so a resume on another machine keeps it.
	workers int

	// mayflyTuningPath names an optional tuning document. The scalar flags
	// below are folded into a document of their own and the file is overlaid on
	// top, so every knob is written in exactly one place.
	mayflyTuningPath string

	mayflyEpochs     int
	mayflyRestarts   int
	mayflyStagnation int
	mayflyTargetCost float64
	mayflyNC         int
	mayflyNCRatio    float64
	mayflySelection  string

	// maxEvals caps the objective evaluations the whole search may spend, which
	// is the budget a campaign matches arms on: an evaluation is one render
	// whichever backend spends it, while an iteration means a generation to one
	// backend and a swarm round to another. Zero leaves the search bounded by
	// --max-iter and --time-budget alone.
	maxEvals int

	// The CMA-ES knobs. The seed is not among them: it is the shared --seed
	// above, which every backend and the polish stage read.
	cmaesCovariance string
	cmaesLambda     int
	cmaesSigma      float64
	cmaesRestarts   int

	// cmaesRunEvals and cmaesLambdaGrowth are the restart ladder's shape, the
	// two knobs a campaign arm is written in: a per-run cap with no restart
	// limit restarts cold until the budget is spent, and a growth of two on the
	// same loop is IPOP.
	cmaesRunEvals     int
	cmaesLambdaGrowth float64

	// The polish stage. It runs after the main search, under the polish
	// profile, and keeps its result only when the primary cost drops.
	polish           string
	polishIterations int
	polishBudget     time.Duration
	polishSigma      float64

	// mayflyTuning is the document a resumed checkpoint carried. It is not a
	// flag: it is the base the current flags and --mayfly-tuning are overlaid
	// on, so a resumed run keeps the tuning it was started with.
	mayflyTuning *optimizer.MayflyTuning

	// reference is how the reference file is read: the loader's downmix and
	// window, and an optional analysis document for the partial term.
	reference referenceOptions
}

func newFitCmd() *cobra.Command {
	options := fitOptions{
		note:       69,
		velocity:   100,
		sampleRate: 44100,
		// Mayfly in the engine-shape arm's round schedule is the default
		// because Phase 8.6's campaign measured it: it beat separable CMA-ES
		// by 0.040 of score over twelve paired blocks on the C5 recording
		// (p = 0.002 after Holm) and, on both references, its spread across
		// seeds is a fraction of CMA-ES's, which is what a blind default is
		// judged on. docs/training.md holds the tables.
		optimizerName: "mayfly",
		// Sixteen rounds need room to anneal: the round schedule splits this
		// evenly, so a hundred iterations would give each round six. The
		// default time budget usually stops a run before this binds.
		maxIter:         640,
		timeBudget:      30 * time.Second,
		reportEvery:     10,
		checkpointEvery: 1,
		workDir:         filepath.FromSlash("out/fit"),
		resume:          false,
		metric:          string(optimizer.MetricBalanced),
		align:           true,
		normalizeGain:   false,
		mayflyVariant:   "desma",
		mayflyPop:       10,
		mayflyEpochs:    1,
		// One warm round from the analysis seed plus fifteen cold restarts:
		// the engine-shape arm that won. The cold rounds are what find the
		// answer -- the warm round held the best in one block of twelve.
		mayflyRestarts:   15,
		mayflyNC:         -1,
		cmaesCovariance:  "separable",
		cmaesSigma:       0.3,
		polish:           optimizer.PolishEngineNone,
		polishIterations: 200,
		polishSigma:      0.02,
	}

	cmd := &cobra.Command{
		Use:   "fit",
		Short: "Fit model parameters to a reference recording",
		Long:  "Optimize model parameters against a target audio file and save the best-fitting preset.",
		Example: `  # Fit A4 from the built-in preset with the default optimizer: Mayfly in
  # one warm round plus fifteen cold restarts, the shape Phase 8.6 measured
  glockenspiel fit --reference a4.wav --output out/a4.json

  # Reproduce a campaign arm: no clock, so the evaluation cap is what stops it
  glockenspiel fit --reference c5.wav --output out/c5.json --note 72 \
    --time-budget 0 --max-evals 24000

  # Follow the search with a local polish stage under the polish profile
  glockenspiel fit --reference a4.wav --output out/a4.json \
    --time-budget 10m --polish cmaes

  # Fit with separable CMA-ES instead, restarting until the budget is spent
  glockenspiel fit --reference a4.wav --output out/a4.json \
    --optimizer cmaes --cmaes-run-evals 4800 --time-budget 10m

  # Continue an interrupted run from its work directory
  glockenspiel fit --reference a4.wav --output out/a4.json --work-dir out/fit-a4 --resume`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFit(cmd, options)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&options.referencePath, "reference", options.referencePath, "Path to reference WAV file")
	flags.StringVar(&options.presetPath, "preset", options.presetPath, "Path to initial preset JSON file (default: built-in preset)")
	flags.StringVar(&options.boundsPath, "bounds", options.boundsPath, boundsFlagHelp)
	flags.StringVar(&options.outputPath, "output", options.outputPath, "Path to output fitted preset JSON file")
	flags.IntVar(&options.note, "note", options.note, "MIDI note number to fit")
	flags.IntVar(&options.velocity, "velocity", options.velocity, "MIDI velocity (0-127)")
	flags.IntVar(&options.sampleRate, "sample-rate", options.sampleRate, "Reference/render sample rate in Hz")
	flags.StringVar(&options.optimizerName, "optimizer", options.optimizerName, "Optimizer to use: simple|mayfly|cmaes")
	flags.IntVar(&options.maxIter, "max-iter", options.maxIter, "Maximum optimizer iterations")
	flags.IntVar(&options.maxEvals, "max-evals", options.maxEvals,
		"Maximum objective evaluations for the whole search (0 is unlimited). It is the budget "+
			"two backends can be compared on, because an evaluation is one render whichever "+
			"backend spends it. A run may overrun it by at most one generation")
	flags.Var(durationFlag{value: &options.timeBudget}, "time-budget",
		"Optimization time budget as a Go duration such as 30s or 10m (a bare number is read as seconds). "+
			"Zero removes the clock, which is what makes --max-evals reproducible across machines; "+
			"--max-iter or --max-evals must then bound the run")
	flags.IntVar(&options.reportEvery, "report-every", options.reportEvery, "Write progress every N major iterations")
	flags.IntVar(&options.checkpointEvery, "checkpoint-interval", options.checkpointEvery,
		"Write a checkpoint once this many optimizer iterations have passed since the last one, "+
			"independently of --report-every (0 disables checkpointing entirely)")
	flags.Int64Var(&options.seed, "seed", options.seed,
		"Random seed for every backend, Mayfly, CMA-ES and the polish stage alike "+
			"(0 picks one and reports it)")
	flags.IntVar(&options.workers, "workers", options.workers,
		"Goroutines evaluating candidates in parallel (0 follows the machine's CPU count). "+
			"The resolved width is recorded in the checkpoint and reused by --resume, so a run "+
			"continued on another machine keeps the width it started with. "+
			"The simple (Nelder-Mead) backend is serial and ignores it")
	flags.StringVar(&options.workDir, "work-dir", options.workDir,
		"The run directory, relative to the current directory. The fit writes its configuration, "+
			"the reference it scored, its trace, its checkpoint, the fitted preset, a render and a "+
			"summary there: the same files a campaign job and a served fit leave, so the run can be "+
			"collected and browsed like any other")
	flags.BoolVar(&options.resume, "resume", options.resume, "Resume fit from the latest checkpoint in work-dir")
	flags.StringVar(&options.metric, "metric", options.metric,
		"Objective: a composite profile (balanced|placement|polish) or a single legacy term (rms|log|spectral)")
	flags.IntVar(&options.modes, "modes", options.modes,
		"Modes to seed from the reference's partials: 0 for every partial the analysis lists, N for the strongest N, -1 to keep the preset's own modes")
	addReferenceFlags(flags, &options.reference)
	flags.BoolVar(&options.align, "align", options.align,
		"Time-align each candidate to the reference before scoring. Leave on for recorded "+
			"references: a few samples of offset invert the phase of a high partial, so the "+
			"correct parameters would score worse than incorrect ones")
	flags.BoolVar(&options.normalizeGain, "normalize-gain", options.normalizeGain,
		"Divide out the scalar gain that best matches the reference level. Use when the "+
			"reference level is unknown; it makes the model's amplitude parameters "+
			"unidentifiable, so leave it off when the level is meaningful")
	flags.StringVar(&options.mayflyVariant, "mayfly-variant", options.mayflyVariant,
		"Mayfly variant: ma|desma|olce|eobbma|gsasma|hmma|mpma|aoblmoa")
	flags.StringVar(&options.mayflyPreset, "mayfly-preset", options.mayflyPreset,
		"Mayfly configuration preset. A preset already selects a variant, so it cannot be "+
			"combined with --mayfly-variant")
	flags.IntVar(&options.mayflyPop, "mayfly-pop", options.mayflyPop, "Male/female population size for Mayfly")
	// The per-backend seed flags are bound to the shared field rather than to
	// fields of their own, so a script that still passes one configures the
	// same run --seed would. runFit refuses an alias combined with --seed.
	flags.Int64Var(&options.seed, "mayfly-seed", options.seed, "Random seed for Mayfly")
	_ = flags.MarkDeprecated("mayfly-seed", "use --seed")
	flags.StringVar(&options.mayflyTuningPath, "mayfly-tuning", options.mayflyTuningPath, mayflyTuningFlagHelp)
	flags.IntVar(&options.mayflyEpochs, "mayfly-epochs", options.mayflyEpochs,
		"Number of warm rounds, each reseeded from the running best")
	flags.IntVar(&options.mayflyRestarts, "mayfly-restarts", options.mayflyRestarts,
		"Number of cold rounds appended after the warm ones, each from a fresh population")
	flags.IntVar(&options.mayflyStagnation, "mayfly-stagnation", options.mayflyStagnation,
		"Stop a round after this many iterations without progress (0 disables the rule)")
	flags.Float64Var(&options.mayflyTargetCost, "mayfly-target-cost", options.mayflyTargetCost,
		"Stop once the best cost reaches this value. Leaving the flag out disables the target, "+
			"which is why zero is a usable target rather than \"off\"")
	flags.IntVar(&options.mayflyNC, "mayfly-nc", options.mayflyNC,
		"Crossover offspring per iteration: -1 derives the count from --mayfly-nc-ratio, "+
			"0 disables crossover, a positive value is taken literally")
	flags.Float64Var(&options.mayflyNCRatio, "mayfly-nc-ratio", options.mayflyNCRatio,
		"Offspring count as a multiple of the male population, used only when --mayfly-nc is -1 "+
			"(0 keeps the variant's own ratio)")
	flags.StringVar(&options.mayflySelection, "mayfly-selection", options.mayflySelection,
		"How crossover pairs parents: rank|tournament")
	flags.StringVar(&options.cmaesCovariance, "cmaes-covariance", options.cmaesCovariance,
		"Covariance CMA-ES learns: separable for the diagonal only, block for a dense matrix "+
			"per mode")
	flags.IntVar(&options.cmaesLambda, "cmaes-lambda", options.cmaesLambda,
		"Population size per generation (0 takes Hansen's default, 4 + floor(3 ln n))")
	flags.Float64Var(&options.cmaesSigma, "cmaes-sigma", options.cmaesSigma,
		"Initial step size, as a fraction of the normalized search box (0 takes the default, 0.3)")
	flags.Int64Var(&options.seed, "cmaes-seed", options.seed,
		"Random seed for CMA-ES (0 picks one and reports it)")
	_ = flags.MarkDeprecated("cmaes-seed", "use --seed")
	flags.IntVar(&options.cmaesRestarts, "cmaes-restarts", options.cmaesRestarts,
		"Number of cold runs (0 restarts until the budget is spent)")
	flags.IntVar(&options.cmaesRunEvals, "cmaes-run-evals", options.cmaesRunEvals,
		"Objective evaluations one cold run may spend before the next restart "+
			"(0 gives every run whatever is left of --max-evals)")
	flags.Float64Var(&options.cmaesLambdaGrowth, "cmaes-lambda-growth", options.cmaesLambdaGrowth,
		"Factor the population is multiplied by on every restart (0 or 1 keeps it fixed, "+
			"2 is IPOP)")
	flags.StringVar(&options.polish, "polish", options.polish,
		"Local refinement stage after the main search: none|nelder-mead|cmaes. It searches "+
			"under the polish profile but keeps its result only when the primary cost drops")
	flags.IntVar(&options.polishIterations, "polish-iterations", options.polishIterations,
		"Maximum iterations for the polish stage")
	flags.Var(durationFlag{value: &options.polishBudget}, "polish-budget",
		"Time budget for the polish stage as a Go duration (0 leaves it uncapped)")
	flags.Float64Var(&options.polishSigma, "polish-sigma", options.polishSigma,
		"Initial polish step as a fraction of the normalized search box")
	flags.StringVar(&options.cpuProfilePath, "cpu-profile", options.cpuProfilePath, "Write a CPU profile for the fit command to this path")
	_ = flags.MarkHidden("cpu-profile")

	_ = cmd.MarkFlagRequired("reference")
	_ = cmd.MarkFlagRequired("output")

	return cmd
}

// runFit is the command's half of a fit: it turns flags into a spec, hands the
// run to internal/fitrun, and reports what came back.
//
// Everything the run itself does -- loading the reference, seeding the modes,
// searching, polishing, checkpointing and writing every file -- belongs to
// fitrun, which is what the campaign runner and the server already call. That
// is what makes --work-dir a run directory rather than a scratch space: a fit
// started from a terminal leaves the same files a campaign job leaves, so the
// tooling that reads them by name cannot tell which of the three started it.
func runFit(cmd *cobra.Command, options fitOptions) error {
	if err := validateFitOptions(cmd, &options); err != nil {
		return err
	}

	if err := os.MkdirAll(options.workDir, 0o755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}

	// A checkpoint can carry the optimizer, the metric and the mode count it
	// was written with, and all three decide how the objective and the backend
	// are built. It is read before anything is derived from those options,
	// otherwise a resumed spectral run would be optimized with the default
	// objective while reporting "spectral".
	var checkpoint *optimizer.Checkpoint

	if options.resume {
		loaded, path, err := loadResumeCheckpoint(cmd, &options)
		if err != nil {
			return err
		}

		if loaded != nil {
			applyResumeBudget(cmd, &options, loaded, path)
		}

		checkpoint = loaded
	}

	if err := validateResolvedFitOptions(cmd, &options); err != nil {
		return err
	}

	metric, err := optimizer.ParseMetric(options.metric)
	if err != nil {
		return err
	}

	stopCPUProfile, err := startCPUProfile(options.cpuProfilePath)
	if err != nil {
		return err
	}

	defer stopCPUProfile()

	spec, err := fitSpec(cmd, options, metric, checkpoint)
	if err != nil {
		return err
	}

	// Ctrl-C should stop the search and still write out the best result so far,
	// rather than losing everything since the last checkpoint. The signal
	// handling is the command's own job: fitrun takes the context and writes
	// its whole run directory whatever cut the search short.
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The run's log is the terminal as well as the directory's log.txt, so the
	// reference line, the resolve line, every progress line and the polish and
	// Finished lines are printed by the run that produced them rather than
	// restated here.
	outcome, err := fitrun.Run(ctx, spec, cmd.OutOrStdout())
	if err != nil {
		return err
	}

	return reportFit(cmd, options, outcome)
}

// validateFitOptions rejects what the command line can be judged on before
// anything is read. It runs before the checkpoint is loaded, because the resume
// rules ask whether a seed was written on the command line and an ambiguous
// pair of seed flags has no answer to give them.
func validateFitOptions(cmd *cobra.Command, options *fitOptions) error {
	if options.referencePath == "" {
		return fmt.Errorf("reference is required")
	}

	if options.outputPath == "" {
		return fmt.Errorf("output is required")
	}

	if options.note < 0 || options.note > 127 {
		return fmt.Errorf("note must be in [0,127], got %d", options.note)
	}

	if options.velocity < 0 || options.velocity > 127 {
		return fmt.Errorf("velocity must be in [0,127], got %d", options.velocity)
	}

	if options.sampleRate <= 0 {
		return fmt.Errorf("sample-rate must be positive, got %d", options.sampleRate)
	}

	if options.maxIter <= 0 {
		return fmt.Errorf("max-iter must be positive, got %d", options.maxIter)
	}

	if options.maxEvals < 0 {
		return fmt.Errorf("max-evals must not be negative, got %d", options.maxEvals)
	}

	if options.cmaesRunEvals < 0 {
		return fmt.Errorf("cmaes-run-evals must not be negative, got %d", options.cmaesRunEvals)
	}

	// Zero means "no clock", which is how a campaign job runs and therefore the
	// only way a hand-run fit reproduces one: a run the clock can stop spends a
	// different number of evaluations on every machine, so --max-evals cannot
	// bind while a time budget is also running. The search stays bounded
	// whatever this is, because --max-iter is required to be positive above.
	if options.timeBudget < 0 {
		return fmt.Errorf("time-budget must not be negative, got %s", options.timeBudget)
	}

	if options.reportEvery < 0 {
		return fmt.Errorf("report-every must be >= 0, got %d", options.reportEvery)
	}

	if options.checkpointEvery < 0 {
		return fmt.Errorf("checkpoint-interval must be >= 0, got %d", options.checkpointEvery)
	}

	if options.workers < 0 {
		return fmt.Errorf("workers must be >= 0, got %d", options.workers)
	}

	return checkSeedFlags(cmd)
}

// validateResolvedFitOptions checks what only the resumed options can be judged
// on: a checkpoint may have chosen the backend, the metric and the Mayfly
// environment, and it is that choice the run is made with.
func validateResolvedFitOptions(cmd *cobra.Command, options *fitOptions) error {
	switch options.optimizerName {
	case fitrun.EngineSimple, fitrun.EngineMayfly, fitrun.EngineCMAES:
	default:
		return fmt.Errorf("unsupported optimizer %q", options.optimizerName)
	}

	switch options.polish {
	case "", optimizer.PolishEngineNone, optimizer.PolishEngineNelderMead, optimizer.PolishEngineCMAES:
	default:
		return fmt.Errorf("unsupported polish engine %q: use none, nelder-mead or cmaes", options.polish)
	}

	// Only a run that actually polishes is held to the iteration count: the
	// flag has a positive default, and a caller that leaves the stage off has
	// no reason to be told about it.
	if polishEnabled(options.polish) && options.polishIterations <= 0 {
		return fmt.Errorf("polish-iterations must be positive, got %d", options.polishIterations)
	}

	// The step is a fraction of the normalized box for either engine, so a
	// sigma above one is a global search rather than a polish. Checking it here
	// rather than in the stage keeps a whole fit from running before the flag
	// is refused.
	if polishEnabled(options.polish) && (options.polishSigma <= 0 || options.polishSigma > 1) {
		return fmt.Errorf("polish-sigma must be in (0, 1], got %g", options.polishSigma)
	}

	if options.metric == "" {
		options.metric = string(optimizer.MetricBalanced)
	}

	if options.optimizerName != fitrun.EngineMayfly {
		return nil
	}

	if err := validateMayflyOptions(cmd, *options); err != nil {
		return err
	}

	// A mayfly iteration is a whole generation -- roughly 43 objective
	// evaluations at a population of ten, the figure
	// optimizer.MayflyEvaluationsPerIteration records -- against about one for
	// a simple major iteration, so the shared default of ten would mean the
	// first progress line lands after some four hundred renders. The default
	// follows the backend; a cadence the caller passed is left alone.
	if !flagChanged(cmd, "report-every") {
		options.reportEvery = 1
	}

	return nil
}

// fitSpec turns the flags into the run fitrun performs.
//
// Every flag the command has that the spec did not already model got a field of
// its own there rather than a second code path here: an analysis document to
// fit against, a checkpoint to continue from, and a cadence that means "write
// no checkpoint at all". A flag handled here instead is one that is not a
// property of the run -- where a copy of the preset is filed, whether a profile
// is taken -- and the run directory would be wrong to record it.
func fitSpec(
	cmd *cobra.Command,
	options fitOptions,
	metric optimizer.Metric,
	checkpoint *optimizer.Checkpoint,
) (fitrun.Spec, error) {
	template, err := loadPresetOrDefault(options.presetPath)
	if err != nil {
		return fitrun.Spec{}, err
	}

	loadOptions, measurement, err := fitLoadOptions(options.reference)
	if err != nil {
		return fitrun.Spec{}, err
	}

	// Bounds the operator wrote down are a hard constraint: they must not be
	// widened to fit whatever the starting preset happens to contain, or the
	// fitted preset can violate the limits that were asked for. Without a
	// document the box is fitrun's own, drawn around the reference's measured
	// fundamental and widened to hold the template.
	var bounds *optimizer.ParamBounds

	if options.boundsPath != "" {
		loaded, err := optimizer.LoadParamBounds(options.boundsPath)
		if err != nil {
			return fitrun.Spec{}, err
		}

		bounds = &loaded
	}

	// AlignNone is the enum's zero value, so the mode is passed by pointer:
	// leaving the field unset would silently give a run started with
	// --align=false the onset correlation it asked not to have.
	alignment := optimizer.AlignNone
	if options.align {
		alignment = optimizer.AlignOnsetCorrelation
	}

	engine, err := fitEngine(cmd, options)
	if err != nil {
		return fitrun.Spec{}, err
	}

	spec := fitrun.Spec{
		Dir:             options.workDir,
		ReferencePath:   options.referencePath,
		Reference:       loadOptions,
		Template:        template,
		Analysis:        measurement,
		Modes:           options.modes,
		Note:            options.note,
		Velocity:        options.velocity,
		SampleRate:      options.sampleRate,
		Metric:          metric,
		Engine:          engine,
		MaxIterations:   options.maxIter,
		MaxEvaluations:  options.maxEvals,
		TimeBudget:      options.timeBudget,
		Seed:            options.seed,
		Workers:         options.workers,
		ReportEvery:     specReportEvery(options.reportEvery),
		CheckpointEvery: specCheckpointEvery(options.checkpointEvery),
		Polish:          fitPolish(options),
		GeneratedBy:     fitGeneratedBy,
		Resume:          checkpoint,
		Bounds:          bounds,
		StrictBounds:    bounds != nil,
		Alignment:       &alignment,
		OnProgress: func(_ optimizer.Progress, metrics *optimizer.Metrics) {
			// The breakdown of the best point so far, one line under the
			// progress line the run itself printed. It costs no extra render:
			// the terms were measured for the trace line this report wrote.
			if metrics != nil {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", formatMetricsLine(*metrics))
			}
		},
	}

	if options.normalizeGain {
		spec.Gain = optimizer.GainLeastSquares
	}

	return spec, nil
}

// fitGeneratedBy is the marker a preset this command fitted carries, so a
// preset found on its own says which of the three fit paths produced it.
const fitGeneratedBy = "glockenspiel fit"

// fitEngine describes the selected backend in the spec's own vocabulary.
func fitEngine(cmd *cobra.Command, options fitOptions) (fitrun.Engine, error) {
	// The scalar --mayfly-* flags and the tuning document are not two ways of
	// configuring the same run: the flags become a document, the file is
	// overlaid on it, and one applier writes the result. Precedence is one
	// sentence -- the document wins over the flags, and both win over whatever
	// tuning a resumed checkpoint carried.
	tuning := options.mayflyTuning.Overlay(tuningFromFlags(cmd, options))

	if options.mayflyTuningPath != "" {
		document, err := optimizer.LoadMayflyTuning(options.mayflyTuningPath)
		if err != nil {
			return fitrun.Engine{}, err
		}

		tuning = tuning.Overlay(document)
	}

	engine := fitrun.Engine{Name: options.optimizerName}

	switch options.optimizerName {
	case fitrun.EngineMayfly:
		engine.Mayfly = fitrun.MayflySettings{
			Variant:    mayflyVariantFor(cmd, options, tuning),
			Preset:     options.mayflyPreset,
			Population: options.mayflyPop,
			Epochs:     options.mayflyEpochs,
			Restarts:   options.mayflyRestarts,
			Tuning:     tuning,
		}
	case fitrun.EngineCMAES:
		engine.CMAES = fitrun.CMAESSettings{
			Covariance:     options.cmaesCovariance,
			Lambda:         options.cmaesLambda,
			Sigma:          options.cmaesSigma,
			RestartLimit:   options.cmaesRestarts,
			RunEvaluations: options.cmaesRunEvals,
			LambdaGrowth:   options.cmaesLambdaGrowth,
		}
	}

	return engine, nil
}

// fitPolish describes the optional refinement stage, or nothing when none was
// asked for. The seed and the worker count are left out: the run replaces them
// with the ones the search resolved, so a polish is as repeatable as the search
// it follows even when both were told to choose for themselves.
func fitPolish(options fitOptions) *optimizer.PolishOptions {
	if !polishEnabled(options.polish) {
		return nil
	}

	return &optimizer.PolishOptions{
		Engine:        options.polish,
		Sigma:         options.polishSigma,
		MaxIterations: options.polishIterations,
		TimeBudget:    options.polishBudget,
	}
}

// specReportEvery and specCheckpointEvery translate the flags' "off" into the
// spec's. The two spell it differently: a flag's zero is "no reports" and "no
// checkpoints at all", while a spec's zero asks for the default -- a report
// every iteration, because the trace is what a campaign scores from, and the
// final checkpoint only.
func specReportEvery(cadence int) int {
	if cadence <= 0 {
		return fitrun.ReportNever
	}

	return cadence
}

func specCheckpointEvery(cadence int) int {
	if cadence <= 0 {
		return fitrun.CheckpointNever
	}

	return cadence
}

// reportFit prints the result and files the fitted preset where --output asked
// for it.
//
// The preset is written twice on purpose: the run directory's preset.json is
// the record beside the trace and the render, and --output is where the
// operator wanted the preset itself, which is usually outside any run
// directory. It is the same bytes either way, provenance block included.
func reportFit(cmd *cobra.Command, options fitOptions, outcome *fitrun.Outcome) error {
	if err := preset.Save(outcome.Preset, options.outputPath); err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	writeMetrics(out, outcome.Metrics, outcome.Profile)
	writePinned(out, outcome.Pinned, outcome.Summary.Dimension)

	_, _ = fmt.Fprintf(out, "Saved preset to %s and the run to %s\n", options.outputPath, options.workDir)

	return nil
}

// polishEnabled reports whether a polish engine was asked for.
func polishEnabled(engine string) bool {
	return engine != "" && engine != optimizer.PolishEngineNone
}

// durationFlag parses Go durations and, for compatibility with the earlier
// float-seconds flag, reads a bare number as seconds.
type durationFlag struct {
	value *time.Duration
}

func (d durationFlag) String() string {
	if d.value == nil {
		return time.Duration(0).String()
	}

	return d.value.String()
}

func (d durationFlag) Set(raw string) error {
	parsed, err := fitschema.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("invalid duration %q: use a Go duration such as 30s or 10m", raw)
	}

	*d.value = parsed

	return nil
}

func (d durationFlag) Type() string {
	return "duration"
}

// loadResumeCheckpoint reads the latest checkpoint in the work dir and folds
// the options it carries -- optimizer, metric, Mayfly environment -- back into
// options. It runs before the objective is built because those options decide
// how the objective is built. A missing checkpoint is not an error: --resume on
// a fresh work dir simply starts from the initial preset.
func loadResumeCheckpoint(cmd *cobra.Command, options *fitOptions) (*optimizer.Checkpoint, string, error) {
	latestPath, err := optimizer.FindLatestCheckpoint(options.workDir)

	if errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"warning: --resume found no checkpoint in %s, starting from the initial preset\n", options.workDir)

		return nil, "", nil
	}

	if err != nil {
		return nil, "", err
	}

	cp, err := optimizer.LoadCheckpoint(latestPath)
	if err != nil {
		return nil, "", err
	}

	applyCheckpointResume(cmd, options, cp)

	return cp, latestPath, nil
}

// applyResumeBudget charges the resumed run for the work the checkpoint
// already did and says so on the terminal.
//
// The vector itself is not touched here: it travels in the spec, and the run is
// where it can be checked against the codec that will decode it.
func applyResumeBudget(cmd *cobra.Command, options *fitOptions, cp *optimizer.Checkpoint, latestPath string) {
	// Only OptimizerIterations is in the same unit as --max-iter.
	// Checkpoint.Iteration is a file index derived from the progress report
	// count, so subtracting it would charge a run with --report-every 10 a
	// tenth of the work it actually did.
	switch {
	case cp.OptimizerIterations > 0:
		remaining := options.maxIter - cp.OptimizerIterations
		if remaining < 1 {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"warning: checkpoint %s already completed %d optimizer iterations, which exhausts --max-iter %d; continuing with 1 iteration, raise --max-iter to search further\n",
				latestPath, cp.OptimizerIterations, options.maxIter)
		}

		options.maxIter = maxInt(1, remaining)
	case cp.Iteration > 0:
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"warning: checkpoint %s records no optimizer iteration count, so --max-iter %d is granted in full\n",
			latestPath, options.maxIter)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"Resuming from %s written %s (iteration=%d optimizer-iterations=%d best=%0.6g optimizer=%s metric=%s remaining-iter=%d)\n",
		latestPath, cp.Timestamp.UTC().Format(time.RFC3339), cp.Iteration, cp.OptimizerIterations, cp.BestCost,
		options.optimizerName, options.metric, options.maxIter)
}

// validateMayflyOptions rejects the flag combinations the CLI can see before
// anything is loaded.
//
// It only covers what the engine cannot judge on its own or would reject too
// late to be useful. Every range on a tuning knob, and a stagnation window
// wider than a round, are checked by the optimizer against the budget it is
// actually given, so restating them here would be a second place for them to
// drift.
func validateMayflyOptions(cmd *cobra.Command, options fitOptions) error {
	if options.mayflyPop < 2 {
		return fmt.Errorf("mayfly-pop must be >= 2, got %d", options.mayflyPop)
	}

	// --mayfly-variant has a default, so only a written one is a real conflict.
	// The engine refuses the pair as well; catching it here means the run is
	// rejected before a reference file is read.
	if options.mayflyPreset != "" && flagChanged(cmd, "mayfly-variant") {
		return fmt.Errorf("mayfly-preset %q already selects a variant, so it cannot be combined with mayfly-variant %q",
			options.mayflyPreset, options.mayflyVariant)
	}

	// The flag defaults to one, so only a written value can be below it.
	// Callers that build fitOptions directly leave it zero to mean "unset".
	if flagChanged(cmd, "mayfly-epochs") && options.mayflyEpochs < 1 {
		return fmt.Errorf("mayfly-epochs must be >= 1, got %d", options.mayflyEpochs)
	}

	if options.mayflyRestarts < 0 {
		return fmt.Errorf("mayfly-restarts must be >= 0, got %d", options.mayflyRestarts)
	}

	return nil
}

// mayflyVariantFor picks the variant the optimizer is configured with.
//
// --mayfly-variant always carries a default, and the engine refuses a dialect
// named twice while preferring its Variant field over the tuning document. So
// passing the default on would make every --mayfly-preset run look like the
// conflict validateMayflyOptions rejects, and would silently override a
// document that named its own dialect -- running desma while the file said
// gsasma. A default nobody wrote must not decide either, so it yields to a
// preset and to a document that chooses for itself.
func mayflyVariantFor(cmd *cobra.Command, options fitOptions, tuning *optimizer.MayflyTuning) string {
	if flagChanged(cmd, "mayfly-variant") {
		return options.mayflyVariant
	}

	if options.mayflyPreset != "" || tuning.NamesDialect() {
		return ""
	}

	return options.mayflyVariant
}

// tuningFromFlags renders the scalar --mayfly-* flags as a tuning document.
//
// It returns nil when nothing was set, which is what keeps an untuned run
// identical to one configured before tuning existed. Every knob whose "off" or
// "derive it" value a caller can also write on purpose -- --mayfly-nc,
// --mayfly-nc-ratio, --mayfly-stagnation and --mayfly-target-cost -- is read
// through flagChanged rather than compared against a sentinel. Comparing
// against one made an explicit --mayfly-stagnation 0 unwritable: it read as
// "not given", so a preset's own stagnation rule stayed on when the caller had
// just asked for it off.
func tuningFromFlags(cmd *cobra.Command, options fitOptions) *optimizer.MayflyTuning {
	tuning := &optimizer.MayflyTuning{}
	written := false

	if options.mayflySelection != "" {
		selection := options.mayflySelection
		tuning.Selection = &selection
		written = true
	}

	if flagChanged(cmd, "mayfly-nc") {
		count := options.mayflyNC
		tuning.NC = &count
		written = true
	}

	if flagChanged(cmd, "mayfly-nc-ratio") {
		ratio := options.mayflyNCRatio
		tuning.NCRatio = &ratio
		written = true
	}

	if flagChanged(cmd, "mayfly-stagnation") {
		stagnation := options.mayflyStagnation
		tuning.Convergence = &optimizer.MayflyConvergence{StagnationIterations: &stagnation}
		written = true
	}

	if flagChanged(cmd, "mayfly-target-cost") {
		cost := options.mayflyTargetCost

		if tuning.Convergence == nil {
			tuning.Convergence = &optimizer.MayflyConvergence{}
		}

		tuning.Convergence.TargetCost = &cost
		written = true
	}

	// One epoch and no restarts is what a single search already is, so writing
	// them would turn an untouched flag into a schedule block for nothing.
	if options.mayflyEpochs > 1 {
		epochs := options.mayflyEpochs
		tuning.Schedule = &optimizer.MayflySchedule{Epochs: &epochs}
		written = true
	}

	if options.mayflyRestarts > 0 {
		restarts := options.mayflyRestarts

		if tuning.Schedule == nil {
			tuning.Schedule = &optimizer.MayflySchedule{}
		}

		tuning.Schedule.Restarts = &restarts
		written = true
	}

	if !written {
		return nil
	}

	return tuning
}

func applyCheckpointResume(cmd *cobra.Command, options *fitOptions, cp *optimizer.Checkpoint) {
	if options == nil || cp == nil {
		return
	}

	if !flagChanged(cmd, "optimizer") && cp.Optimizer != "" {
		options.optimizerName = cp.Optimizer
	}

	if !flagChanged(cmd, "metric") && cp.Metric != "" {
		options.metric = cp.Metric
	}

	// The modes are the checkpoint's choice unless the flag says otherwise:
	// a different choice builds a codec the checkpoint's vector does not fit.
	// A checkpoint without the field was written before seeding existed and
	// used the template's modes.
	if !flagChanged(cmd, "modes") {
		options.modes = optimizer.KeepTemplateModes

		if cp.State != nil && cp.State.Modes != 0 {
			options.modes = cp.State.Modes
		}
	}

	if cp.State == nil {
		return
	}

	// The width the checkpoint recorded, so a fit continued on a machine with
	// a different CPU count evaluates the same number of candidates at a time
	// as the run it is continuing. A written --workers still wins.
	if !flagChanged(cmd, "workers") && cp.State.Workers > 0 {
		options.workers = cp.State.Workers
	}

	if !flagChanged(cmd, "optimizer") && cp.State.Kind != "" {
		options.optimizerName = cp.State.Kind
	}

	if cp.State.Mayfly != nil {
		if !flagChanged(cmd, "mayfly-variant") && cp.State.Mayfly.Variant != "" {
			options.mayflyVariant = cp.State.Mayfly.Variant
		}

		if !flagChanged(cmd, "mayfly-pop") && cp.State.Mayfly.Population > 0 {
			options.mayflyPop = cp.State.Mayfly.Population
		}

		if !seedFlagChanged(cmd) {
			options.seed = cp.State.Mayfly.Seed
		}

		if !flagChanged(cmd, "mayfly-preset") && cp.State.Mayfly.Preset != "" {
			options.mayflyPreset = cp.State.Mayfly.Preset
		}

		if !flagChanged(cmd, "mayfly-epochs") && cp.State.Mayfly.Epochs > 0 {
			options.mayflyEpochs = cp.State.Mayfly.Epochs
		}

		if !flagChanged(cmd, "mayfly-restarts") && cp.State.Mayfly.Restarts > 0 {
			options.mayflyRestarts = cp.State.Mayfly.Restarts
		}

		// The document becomes the base the current flags are overlaid on, so a
		// resumed run keeps every knob it was started with while a flag written
		// on the resume command still wins.
		options.mayflyTuning = cp.State.Mayfly.Tuning
	}

	if cp.State.CMAES != nil {
		if !flagChanged(cmd, "cmaes-covariance") && cp.State.CMAES.Covariance != "" {
			options.cmaesCovariance = cp.State.CMAES.Covariance
		}

		// Zero is what the checkpoint records for a knob nobody set, and it
		// means the same thing here as it did there: take the default. So it
		// is restored as it stands, unlike the guarded fields above, which
		// have a default of their own that a zero would silently undo.
		if !flagChanged(cmd, "cmaes-lambda") {
			options.cmaesLambda = cp.State.CMAES.Lambda
		}

		if !flagChanged(cmd, "cmaes-sigma") && cp.State.CMAES.Sigma > 0 {
			options.cmaesSigma = cp.State.CMAES.Sigma
		}

		if !seedFlagChanged(cmd) {
			options.seed = cp.State.CMAES.Seed
		}

		// The ladder's shape, restored for the reason the checkpoint records it:
		// a resume that dropped it would continue as a different optimizer.
		if !flagChanged(cmd, "cmaes-run-evals") {
			options.cmaesRunEvals = cp.State.CMAES.RunEvaluations
		}

		if !flagChanged(cmd, "cmaes-lambda-growth") {
			options.cmaesLambdaGrowth = cp.State.CMAES.LambdaGrowth
		}

		if !flagChanged(cmd, "cmaes-restarts") {
			options.cmaesRestarts = cp.State.CMAES.Restarts
		}
	}
}

// deprecatedSeedFlags names the per-backend seed flags --seed replaced. They
// are still bound to fitOptions.seed, so passing one configures the same run.
var deprecatedSeedFlags = []string{"mayfly-seed", "cmaes-seed"}

// checkSeedFlags rejects a command line that names the seed more than once.
//
// The deprecated aliases write the same option as --seed, so a second flag
// silently overwrites the first depending on the order pflag happened to parse
// them in. There is no reading of "--seed 1 --cmaes-seed 2" that is not a
// mistake, so it is refused rather than resolved.
func checkSeedFlags(cmd *cobra.Command) error {
	written := make([]string, 0, len(deprecatedSeedFlags)+1)

	if flagChanged(cmd, "seed") {
		written = append(written, "--seed")
	}

	for _, name := range deprecatedSeedFlags {
		if flagChanged(cmd, name) {
			written = append(written, "--"+name)
		}
	}

	if len(written) < 2 {
		return nil
	}

	return fmt.Errorf(
		"%s and %s both set the run's seed, and %s is a deprecated alias for --seed: pass only one of them",
		written[0], written[1], written[len(written)-1],
	)
}

// seedFlagChanged reports whether the command line named the seed at all,
// under --seed or either deprecated alias. A resume takes the checkpoint's
// seed unless it did.
func seedFlagChanged(cmd *cobra.Command) bool {
	if flagChanged(cmd, "seed") {
		return true
	}

	for _, name := range deprecatedSeedFlags {
		if flagChanged(cmd, name) {
			return true
		}
	}

	return false
}

func flagChanged(cmd *cobra.Command, name string) bool {
	if cmd == nil {
		return false
	}

	flags := cmd.Flags()
	if flags == nil {
		return false
	}

	flag := flags.Lookup(name)
	if flag == nil {
		return false
	}

	return flag.Changed
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}

	return b
}

func startCPUProfile(path string) (func(), error) {
	if path == "" {
		return func() {}, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create cpu profile directory: %w", err)
	}

	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create cpu profile %q: %w", path, err)
	}

	if err := pprof.StartCPUProfile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("start cpu profile %q: %w", path, err)
	}

	return func() {
		pprof.StopCPUProfile()

		_ = file.Close()
	}, nil
}
