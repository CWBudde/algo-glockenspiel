package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"slices"
	"strconv"
	"syscall"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/preset"
	"github.com/cwbudde/algo-glockenspiel/internal/synth"
	"github.com/cwbudde/algo-glockenspiel/internal/wavio"
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
	mayflyVariant   string
	mayflyPreset    string
	mayflyPop       int
	mayflySeed      int64
	cpuProfilePath  string

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

	// mayflyTuning is the document a resumed checkpoint carried. It is not a
	// flag: it is the base the current flags and --mayfly-tuning are overlaid
	// on, so a resumed run keeps the tuning it was started with.
	mayflyTuning *optimizer.MayflyTuning
}

func newFitCmd() *cobra.Command {
	options := fitOptions{
		note:            69,
		velocity:        100,
		sampleRate:      44100,
		optimizerName:   "simple",
		maxIter:         100,
		timeBudget:      30 * time.Second,
		reportEvery:     10,
		checkpointEvery: 1,
		workDir:         filepath.FromSlash("out/fit"),
		resume:          false,
		metric:          string(optimizer.MetricRMS),
		align:           true,
		normalizeGain:   false,
		mayflyVariant:   "desma",
		mayflyPop:       10,
		mayflySeed:      1,
		mayflyEpochs:    1,
		mayflyRestarts:  0,
		mayflyNC:        -1,
	}

	cmd := &cobra.Command{
		Use:   "fit",
		Short: "Fit model parameters to a reference recording",
		Long:  "Optimize model parameters against a target audio file and save the best-fitting preset.",
		Example: `  # Fit A4 from the built-in preset with the default optimizer
  glockenspiel fit --reference a4.wav --output out/a4.json

  # Fit with Mayfly, a wall-clock budget and a narrowed search box
  glockenspiel fit --reference a4.wav --output out/a4.json \
    --optimizer mayfly --mayfly-pop 20 --time-budget 10m --bounds bounds/a4.json

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
	flags.StringVar(&options.optimizerName, "optimizer", options.optimizerName, "Optimizer to use: simple|mayfly")
	flags.IntVar(&options.maxIter, "max-iter", options.maxIter, "Maximum optimizer iterations")
	flags.Var(durationFlag{value: &options.timeBudget}, "time-budget",
		"Optimization time budget as a Go duration such as 30s or 10m (a bare number is read as seconds)")
	flags.IntVar(&options.reportEvery, "report-every", options.reportEvery, "Write progress every N major iterations")
	flags.IntVar(&options.checkpointEvery, "checkpoint-interval", options.checkpointEvery, "Write checkpoint every N progress reports (0 disables checkpointing entirely)")
	flags.StringVar(&options.workDir, "work-dir", options.workDir, "Directory for checkpoints and rendered fit output, relative to the current directory")
	flags.BoolVar(&options.resume, "resume", options.resume, "Resume fit from the latest checkpoint in work-dir")
	flags.StringVar(&options.metric, "metric", options.metric, "Objective metric: rms|log|spectral")
	flags.BoolVar(&options.align, "align", options.align,
		"Time-align each candidate to the reference before scoring. Leave on for recorded "+
			"references: a few samples of offset invert the phase of a high partial, so the "+
			"correct parameters would score worse than incorrect ones")
	flags.BoolVar(&options.normalizeGain, "normalize-gain", options.normalizeGain,
		"Divide out the scalar gain that best matches the reference level. Use when the "+
			"reference level is unknown; it makes the model's amplitude parameters "+
			"unidentifiable, so leave it off when the level is meaningful")
	flags.StringVar(&options.mayflyVariant, "mayfly-variant", options.mayflyVariant,
		"Mayfly variant: ma|desma|olce|eobbma|gsasma|mpma|aoblmoa|auto. \"auto\" spends part of "+
			"the budget measuring the landscape before it picks one; the measured effect of the "+
			"dialect is small, so that budget usually buys more spent on iterations")
	flags.StringVar(&options.mayflyPreset, "mayfly-preset", options.mayflyPreset,
		"Mayfly configuration preset. A preset already selects a variant, so it cannot be "+
			"combined with --mayfly-variant")
	flags.IntVar(&options.mayflyPop, "mayfly-pop", options.mayflyPop, "Male/female population size for Mayfly")
	flags.Int64Var(&options.mayflySeed, "mayfly-seed", options.mayflySeed, "Random seed for Mayfly")
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
	flags.StringVar(&options.cpuProfilePath, "cpu-profile", options.cpuProfilePath, "Write a CPU profile for the fit command to this path")
	_ = flags.MarkHidden("cpu-profile")

	_ = cmd.MarkFlagRequired("reference")
	_ = cmd.MarkFlagRequired("output")

	return cmd
}

func runFit(cmd *cobra.Command, options fitOptions) error {
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

	if options.timeBudget <= 0 {
		return fmt.Errorf("time-budget must be positive, got %s", options.timeBudget)
	}

	if options.reportEvery < 0 {
		return fmt.Errorf("report-every must be >= 0, got %d", options.reportEvery)
	}

	if options.checkpointEvery < 0 {
		return fmt.Errorf("checkpoint-interval must be >= 0, got %d", options.checkpointEvery)
	}

	if err := os.MkdirAll(options.workDir, 0o755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}

	// A checkpoint can carry the optimizer and the metric it was written with,
	// and both decide how the objective is built. Load it before anything is
	// derived from those options, otherwise a resumed spectral run would be
	// optimized with the default RMS objective while reporting "spectral".
	var (
		checkpoint     *optimizer.Checkpoint
		checkpointPath string
	)

	if options.resume {
		loaded, path, err := loadResumeCheckpoint(cmd, &options)
		if err != nil {
			return err
		}

		checkpoint, checkpointPath = loaded, path
	}

	if options.optimizerName != "simple" && options.optimizerName != "mayfly" {
		return fmt.Errorf("unsupported optimizer %q", options.optimizerName)
	}

	if options.metric == "" {
		options.metric = string(optimizer.MetricRMS)
	}

	metric, err := optimizer.ParseMetric(options.metric)
	if err != nil {
		return err
	}

	if options.optimizerName == "mayfly" {
		if err := validateMayflyOptions(cmd, options); err != nil {
			return err
		}

		// A mayfly iteration is a whole generation -- roughly 47.7 objective
		// evaluations at a population of ten -- against about one for a simple
		// major iteration, so the shared default of ten would mean the first
		// progress line lands after some five hundred renders. The default
		// follows the backend; a cadence the caller passed is left alone.
		if !cmd.Flags().Changed("report-every") {
			options.reportEvery = 1
		}
	}

	stopCPUProfile, err := startCPUProfile(options.cpuProfilePath)
	if err != nil {
		return err
	}
	defer stopCPUProfile()

	reference, referenceRate, err := wavio.LoadMono(options.referencePath)
	if err != nil {
		return err
	}

	if referenceRate != options.sampleRate {
		return fmt.Errorf("reference sample rate %d does not match requested sample rate %d", referenceRate, options.sampleRate)
	}

	initialPreset, err := loadPresetOrDefault(options.presetPath)
	if err != nil {
		return err
	}

	bounds := optimizer.DefaultParamBounds

	explicitBounds := options.boundsPath != ""
	if explicitBounds {
		bounds, err = optimizer.LoadParamBounds(options.boundsPath)
		if err != nil {
			return err
		}
	}

	// The scalar --mayfly-* flags and the tuning document are not two ways of
	// configuring the same run: the flags become a document, the file is
	// overlaid on it, and one applier writes the result. Precedence is one
	// sentence -- the document wins over the flags, and both win over whatever
	// tuning a resumed checkpoint carried.
	tuning := options.mayflyTuning.Overlay(tuningFromFlags(cmd, options))

	if options.mayflyTuningPath != "" {
		document, err := optimizer.LoadMayflyTuning(options.mayflyTuningPath)
		if err != nil {
			return err
		}

		tuning = tuning.Overlay(document)
	}

	objectiveConfig := optimizer.DefaultObjectiveConfig(metric)
	objectiveConfig.Bounds = bounds
	// Bounds the user wrote down are a hard constraint: they must not be
	// widened to fit whatever the starting preset happens to contain, or the
	// fitted preset can violate the limits that were asked for.
	objectiveConfig.StrictBounds = explicitBounds
	objectiveConfig.Alignment = optimizer.AlignNone

	if options.align {
		objectiveConfig.Alignment = optimizer.AlignOnsetCorrelation
	}

	if options.normalizeGain {
		objectiveConfig.Gain = optimizer.GainLeastSquares
	}

	objective, err := optimizer.NewObjectiveFunctionWithConfig(
		reference, initialPreset, options.sampleRate, options.note, options.velocity, objectiveConfig,
	)
	if err != nil {
		return err
	}

	initialEncoded, err := objective.Codec().EncodeParams(&initialPreset.Parameters)
	if err != nil {
		return err
	}

	if checkpoint != nil {
		initialEncoded, err = applyResumeCheckpoint(cmd, &options, checkpoint, checkpointPath, initialEncoded)
		if err != nil {
			return err
		}
	}

	optBounds := objective.Codec().EncodedBounds()

	// With strict bounds the starting preset can sit outside the box, so pull
	// it in rather than handing the backend an infeasible initial point.
	initialEncoded, err = clampInitialPoint(cmd, optBounds, initialEncoded, explicitBounds)
	if err != nil {
		return err
	}

	bestCheckpointPath := func(iter int) string {
		return filepath.Join(options.workDir, fmt.Sprintf("checkpoint_%04d.json", iter))
	}
	lastCheckpointIteration := 0
	saveCheckpoint := func(index, optimizerIterations int, params []float64, cost float64) error {
		if len(params) == 0 {
			return nil
		}

		return optimizer.SaveCheckpoint(bestCheckpointPath(index), &optimizer.Checkpoint{
			Version:             optimizer.CheckpointVersion,
			Iteration:           index,
			OptimizerIterations: optimizerIterations,
			BestCost:            cost,
			BestParams:          append([]float64(nil), params...),
			Optimizer:           options.optimizerName,
			Metric:              options.metric,
			State:               checkpointStateForOptions(options, tuning),
		})
	}

	// Ctrl-C should stop the search and still write out the best result so far,
	// rather than losing everything since the last checkpoint.
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	var selectedOptimizer optimizer.Optimizer

	switch options.optimizerName {
	case "simple":
		selectedOptimizer = &optimizer.SimpleOptimizer{}
	case "mayfly":
		selectedOptimizer = &optimizer.MayflyOptimizer{
			Variant:    mayflyVariantFor(cmd, options, tuning),
			Preset:     options.mayflyPreset,
			Population: options.mayflyPop,
			Seed:       options.mayflySeed,
			Tuning:     tuning,
			OnResolve: func(resolved optimizer.ResolvedMayfly) {
				// Record the effective seed before the search starts, so every
				// checkpoint carries the stream the run actually used. With
				// --mayfly-seed 0 that is the difference between a resumed run
				// continuing the original stream and starting a new one.
				options.mayflySeed = resolved.Seed
				options.mayflyVariant = resolved.Variant
				options.mayflyPreset = resolved.Preset

				_, _ = fmt.Fprintln(cmd.OutOrStdout(), formatResolvedMayfly(resolved))
			},
		}
	}

	result, err := selectedOptimizer.Optimize(ctx, objective.Objective(), initialEncoded, optBounds, optimizer.OptimizeOptions{
		MaxIterations: options.maxIter,
		TimeBudget:    options.timeBudget,
		ReportEvery:   options.reportEvery,
		Report: func(progress optimizer.Progress) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"iteration %d: current=%0.6g best=%0.6g evals=%d elapsed=%s\n",
				progress.Iteration, progress.CurrentCost, progress.BestCost, progress.Evaluations, progress.Elapsed.Round(time.Millisecond))
			if shouldCheckpoint(progress.Iteration, options.checkpointEvery) {
				if saveCheckpoint(progress.Iteration, progress.OptimizerIterations, progress.BestParams, progress.BestCost) == nil {
					lastCheckpointIteration = progress.Iteration
				}
			}
		},
	})
	if err != nil {
		return err
	}

	bestParams, err := objective.Codec().DecodeParams(result.BestParams)
	if err != nil {
		return err
	}

	fittedPreset := *initialPreset

	fittedPreset.Parameters = *bestParams
	if err := preset.Save(&fittedPreset, options.outputPath); err != nil {
		return err
	}

	// The final result is usually better than the last periodic checkpoint, so
	// record it too -- but only when checkpointing is enabled at all. Its index
	// stays above the last periodic one so FindLatestCheckpoint still picks the
	// newest file. The index is a file ordering key, not an iteration count;
	// the resumable budget lives in OptimizerIterations.
	if options.checkpointEvery > 0 {
		finalIndex := maxInt(result.Iterations, lastCheckpointIteration+1)
		if err := saveCheckpoint(finalIndex, result.Iterations, result.BestParams, result.BestCost); err != nil {
			return err
		}
	}

	engine, err := synth.NewSynthesizer(&fittedPreset, options.sampleRate)
	if err != nil {
		return err
	}

	renderedDuration := float64(len(reference)) / float64(options.sampleRate)
	fittedSamples := engine.RenderNote(options.note, options.velocity, renderedDuration)

	renderedPath := filepath.Join(options.workDir, "fitted_output.wav")
	if err := wavio.WriteMono(renderedPath, options.sampleRate, fittedSamples); err != nil {
		return err
	}

	// The reported RMS/log figures describe what the rendered WAV will sound
	// like, so quantize a copy the way wavio.WriteMono will. The objective
	// itself no longer does this — quantizing every candidate made the cost
	// piecewise constant.
	reportedSamples := append([]float32(nil), fittedSamples...)
	optimizer.ProjectToPCM16Domain(reportedSamples)
	rms := optimizer.ComputeRMSError(reportedSamples, reference)
	logErr := optimizer.ComputeLogError(reportedSamples, reference, 1e-20, 0)

	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"Finished: best=%0.6g stop=%s iterations=%d evals=%d rms=%0.6g log=%0.6g\n",
		result.BestCost, result.StopReason, result.Iterations, result.Evaluations, rms, logErr)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"Saved preset to %s and rendered fit to %s\n", options.outputPath, renderedPath)

	return nil
}

// shouldCheckpoint reports whether a progress report should be checkpointed.
//
// Progress.Iteration counts progress reports and grows by one per report, so a
// plain modulo gives the requested cadence. An interval of zero disables
// checkpointing completely.
func shouldCheckpoint(iteration, checkpointEvery int) bool {
	if checkpointEvery <= 0 || iteration <= 0 {
		return false
	}

	return iteration%checkpointEvery == 0
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
	if parsed, err := time.ParseDuration(raw); err == nil {
		*d.value = parsed

		return nil
	}

	seconds, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fmt.Errorf("invalid duration %q: use a Go duration such as 30s or 10m", raw)
	}

	*d.value = time.Duration(seconds * float64(time.Second))

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

// applyResumeCheckpoint folds the already loaded checkpoint into the encoded
// starting point and the remaining iteration budget.
func applyResumeCheckpoint(
	cmd *cobra.Command,
	options *fitOptions,
	cp *optimizer.Checkpoint,
	latestPath string,
	initialEncoded []float64,
) ([]float64, error) {
	// A checkpoint from a differently shaped preset (Chebyshev toggled, other
	// harmonic count) cannot be decoded by this codec. Resuming was requested
	// explicitly, so fail loudly instead of quietly starting from scratch.
	if len(cp.BestParams) != len(initialEncoded) {
		return nil, fmt.Errorf(
			"checkpoint %s holds %d parameters but the preset encodes %d: use the preset the checkpoint was written with, or drop --resume",
			latestPath, len(cp.BestParams), len(initialEncoded),
		)
	}

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

	return append(initialEncoded[:0], cp.BestParams...), nil
}

// clampInitialPoint pulls the encoded starting point into the search box. With
// explicit --bounds the box is no longer widened to contain the starting
// preset, so a preset outside the requested range would otherwise be handed to
// the backend as an infeasible initial point.
func clampInitialPoint(
	cmd *cobra.Command,
	bounds optimizer.Bounds,
	initialEncoded []float64,
	warn bool,
) ([]float64, error) {
	clamped, err := bounds.Clamp(initialEncoded)
	if err != nil {
		return nil, err
	}

	if warn && !slices.Equal(clamped, initialEncoded) {
		_, _ = fmt.Fprint(cmd.ErrOrStderr(),
			"warning: the starting preset lies outside the --bounds box and was clamped into it\n")
	}

	return clamped, nil
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
// identical to one configured before tuning existed. The three-way flags --
// --mayfly-nc and --mayfly-target-cost, where "not given" differs from a
// written -1 or 0 -- are read through flagChanged rather than compared against
// a sentinel, because both sentinels are legal values.
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

	if options.mayflyNCRatio != 0 {
		ratio := options.mayflyNCRatio
		tuning.NCRatio = &ratio
		written = true
	}

	if options.mayflyStagnation > 0 {
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

// formatResolvedMayfly renders what a run actually settled on.
//
// variant= and seed= stay first and keep their spelling: they were the whole
// line before the tuning surface existed, and a reader -- human or script --
// already looks for them there.
func formatResolvedMayfly(resolved optimizer.ResolvedMayfly) string {
	line := fmt.Sprintf("Mayfly: variant=%s seed=%d", resolved.Variant, resolved.Seed)

	if resolved.Preset != "" {
		line += " preset=" + resolved.Preset
	}

	line += fmt.Sprintf(" rounds=%dx%d", resolved.Rounds, resolved.IterationsPerRound)

	// Recommendation is filled in only when the dialect was measured rather
	// than named, so it is what tells an auto run from a plain one.
	if resolved.Recommendation != "" {
		line += fmt.Sprintf(" (auto: %s, confidence=%0.2f, classify-evals=%d)",
			resolved.Recommendation, resolved.Confidence, resolved.ClassifyEvaluations)
	}

	return line
}

func checkpointStateForOptions(options fitOptions, tuning *optimizer.MayflyTuning) *optimizer.OptimizerState {
	state := &optimizer.OptimizerState{
		Kind: options.optimizerName,
	}
	if options.optimizerName == "mayfly" {
		// The schedule the run was given, not the one the flags asked for: a
		// tuning document may have overridden either, and the checkpoint should
		// describe what happened.
		epochs, restarts := options.mayflyEpochs, options.mayflyRestarts

		if tuning != nil && tuning.Schedule != nil {
			if tuning.Schedule.Epochs != nil {
				epochs = *tuning.Schedule.Epochs
			}

			if tuning.Schedule.Restarts != nil {
				restarts = *tuning.Schedule.Restarts
			}
		}

		state.Mayfly = &optimizer.MayflyCheckpointEnv{
			Variant:    options.mayflyVariant,
			Preset:     options.mayflyPreset,
			Population: options.mayflyPop,
			Seed:       options.mayflySeed,
			Epochs:     epochs,
			Restarts:   restarts,
			// The merged document rather than the flags: it is what the run was
			// actually configured with, so a resume reproduces it without
			// needing the tuning file to still be on disk.
			Tuning: tuning,
		}
	}

	return state
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

	if cp.State == nil {
		return
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

		if !flagChanged(cmd, "mayfly-seed") {
			options.mayflySeed = cp.State.Mayfly.Seed
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
