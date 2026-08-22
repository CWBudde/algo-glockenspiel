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

	"github.com/cwbudde/glockenspiel/internal/optimizer"
	"github.com/cwbudde/glockenspiel/internal/preset"
	"github.com/cwbudde/glockenspiel/internal/synth"
	"github.com/cwbudde/glockenspiel/internal/wavio"
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
	mayflyPop       int
	mayflySeed      int64
	cpuProfilePath  string
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
	flags.StringVar(&options.mayflyVariant, "mayfly-variant", options.mayflyVariant, "Mayfly variant: ma|desma|olce|eobbma|gsasma|mpma|aoblmoa")
	flags.IntVar(&options.mayflyPop, "mayfly-pop", options.mayflyPop, "Male/female population size for Mayfly")
	flags.Int64Var(&options.mayflySeed, "mayfly-seed", options.mayflySeed, "Random seed for Mayfly")
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

	if options.optimizerName == "mayfly" && options.mayflyPop < 2 {
		return fmt.Errorf("mayfly-pop must be >= 2, got %d", options.mayflyPop)
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
			State:               checkpointStateForOptions(options),
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
			Variant:    options.mayflyVariant,
			Population: options.mayflyPop,
			Seed:       options.mayflySeed,
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

func checkpointStateForOptions(options fitOptions) *optimizer.OptimizerState {
	state := &optimizer.OptimizerState{
		Kind: options.optimizerName,
	}
	if options.optimizerName == "mayfly" {
		state.Mayfly = &optimizer.MayflyCheckpointEnv{
			Variant:    options.mayflyVariant,
			Population: options.mayflyPop,
			Seed:       options.mayflySeed,
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
