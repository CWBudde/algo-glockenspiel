package cli

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/cwbudde/glockenspiel/internal/optimizer"
	"github.com/cwbudde/glockenspiel/internal/preset"
	"github.com/cwbudde/glockenspiel/internal/synth"
	"github.com/go-audio/wav"
	"github.com/spf13/cobra"
)

type fitOptions struct {
	referencePath   string
	presetPath      string
	outputPath      string
	note            int
	velocity        int
	sampleRate      int
	optimizerName   string
	maxIter         int
	timeBudget      float64
	reportEvery     int
	checkpointEvery int
	workDir         string
	resume          bool
	metric          string
	mayflyVariant   string
	mayflyPop       int
	mayflySeed      int64
}

func newFitCmd() *cobra.Command {
	options := fitOptions{
		presetPath:      filepath.FromSlash("assets/presets/default.json"),
		note:            69,
		velocity:        100,
		sampleRate:      44100,
		optimizerName:   "simple",
		maxIter:         100,
		timeBudget:      30,
		reportEvery:     10,
		checkpointEvery: 1,
		workDir:         filepath.FromSlash("out/fit"),
		resume:          false,
		metric:          string(optimizer.MetricRMS),
		mayflyVariant:   "desma",
		mayflyPop:       10,
		mayflySeed:      1,
	}

	cmd := &cobra.Command{
		Use:   "fit",
		Short: "Fit model parameters to a reference recording",
		Long:  "Optimize model parameters against a target audio file and save the best-fitting preset.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFit(cmd, options)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&options.referencePath, "reference", options.referencePath, "Path to reference WAV file")
	flags.StringVar(&options.presetPath, "preset", options.presetPath, "Path to initial preset JSON file")
	flags.StringVar(&options.outputPath, "output", options.outputPath, "Path to output fitted preset JSON file")
	flags.IntVar(&options.note, "note", options.note, "MIDI note number to fit")
	flags.IntVar(&options.velocity, "velocity", options.velocity, "MIDI velocity (0-127)")
	flags.IntVar(&options.sampleRate, "sample-rate", options.sampleRate, "Reference/render sample rate in Hz")
	flags.StringVar(&options.optimizerName, "optimizer", options.optimizerName, "Optimizer to use: simple")
	flags.IntVar(&options.maxIter, "max-iter", options.maxIter, "Maximum optimizer iterations")
	flags.Float64Var(&options.timeBudget, "time-budget", options.timeBudget, "Optimization time budget in seconds")
	flags.IntVar(&options.reportEvery, "report-every", options.reportEvery, "Write progress every N major iterations")
	flags.IntVar(&options.checkpointEvery, "checkpoint-interval", options.checkpointEvery, "Write checkpoint every N progress iterations (0 disables intermediate checkpoints)")
	flags.StringVar(&options.workDir, "work-dir", options.workDir, "Directory for checkpoints and rendered fit output")
	flags.BoolVar(&options.resume, "resume", options.resume, "Resume fit from the latest checkpoint in work-dir")
	flags.StringVar(&options.metric, "metric", options.metric, "Objective metric: rms|log|spectral")
	flags.StringVar(&options.mayflyVariant, "mayfly-variant", options.mayflyVariant, "Mayfly variant: ma|desma|olce|eobbma|gsasma|mpma|aoblmoa")
	flags.IntVar(&options.mayflyPop, "mayfly-pop", options.mayflyPop, "Male/female population size for Mayfly")
	flags.Int64Var(&options.mayflySeed, "mayfly-seed", options.mayflySeed, "Random seed for Mayfly")

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
		return fmt.Errorf("time-budget must be positive, got %f", options.timeBudget)
	}

	if options.reportEvery < 0 {
		return fmt.Errorf("report-every must be >= 0, got %d", options.reportEvery)
	}
	if options.checkpointEvery < 0 {
		return fmt.Errorf("checkpoint-interval must be >= 0, got %d", options.checkpointEvery)
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

	if err := os.MkdirAll(options.workDir, 0o755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}

	reference, referenceRate, err := loadMonoWAVFloat32(options.referencePath)
	if err != nil {
		return err
	}

	if referenceRate != options.sampleRate {
		return fmt.Errorf("reference sample rate %d does not match requested sample rate %d", referenceRate, options.sampleRate)
	}

	initialPreset, err := preset.Load(options.presetPath)
	if err != nil {
		return err
	}

	objective, err := optimizer.NewObjectiveFunction(reference, initialPreset, options.sampleRate, options.note, options.velocity, metric)
	if err != nil {
		return err
	}

	initialEncoded, err := objective.Codec().EncodeParams(&initialPreset.Parameters)
	if err != nil {
		return err
	}
	if options.resume {
		latestPath, err := optimizer.FindLatestCheckpoint(options.workDir)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			cp, err := optimizer.LoadCheckpoint(latestPath)
			if err != nil {
				return err
			}
			if len(cp.BestParams) == len(initialEncoded) {
				initialEncoded = append(initialEncoded[:0], cp.BestParams...)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(),
					"Resuming from %s (iteration=%d best=%0.6g)\n",
					latestPath, cp.Iteration, cp.BestCost)
			}
		}
	}

	optBounds := objective.Codec().EncodedBounds()

	bestCheckpointPath := func(iter int) string {
		return filepath.Join(options.workDir, fmt.Sprintf("checkpoint_%04d.json", iter))
	}
	wroteCheckpoint := false
	saveCheckpoint := func(iteration int, params []float64, cost float64) error {
		if len(params) == 0 {
			return nil
		}
		return optimizer.SaveCheckpoint(bestCheckpointPath(iteration), &optimizer.Checkpoint{
			Version:    "1.0",
			Iteration:  iteration,
			BestCost:   cost,
			BestParams: append([]float64(nil), params...),
			Optimizer:  options.optimizerName,
			Metric:     options.metric,
		})
	}
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

	result, err := selectedOptimizer.Optimize(objective.Objective(), initialEncoded, optBounds, optimizer.OptimizeOptions{
		MaxIterations: options.maxIter,
		TimeBudget:    time.Duration(options.timeBudget * float64(time.Second)),
		ReportEvery:   options.reportEvery,
		Report: func(progress optimizer.Progress) {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"iteration %d: current=%0.6g best=%0.6g evals=%d elapsed=%s\n",
				progress.Iteration, progress.CurrentCost, progress.BestCost, progress.Evaluations, progress.Elapsed.Round(time.Millisecond))
			if options.checkpointEvery > 0 && progress.Iteration%options.checkpointEvery == 0 {
				if saveCheckpoint(progress.Iteration, progress.BestParams, progress.BestCost) == nil {
					wroteCheckpoint = true
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

	if (!wroteCheckpoint && options.checkpointEvery > 0) || options.resume {
		if err := saveCheckpoint(result.Iterations, result.BestParams, result.BestCost); err != nil {
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
	if err := writeWAV(renderedPath, options.sampleRate, fittedSamples); err != nil {
		return err
	}

	reportedSamples := append([]float32(nil), fittedSamples...)
	projectToPCM16Domain(reportedSamples)
	rms := optimizer.ComputeRMSError(reportedSamples, reference)
	logErr := optimizer.ComputeLogError(reportedSamples, reference, 1e-20, 0)

	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"Finished: best=%0.6g stop=%s iterations=%d evals=%d rms=%0.6g log=%0.6g\n",
		result.BestCost, result.StopReason, result.Iterations, result.Evaluations, rms, logErr)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(),
		"Saved preset to %s and rendered fit to %s\n", options.outputPath, renderedPath)

	return nil
}

func projectToPCM16Domain(samples []float32) {
	for i, sample := range samples {
		samples[i] = float32(float64(float32ToInt16(sample)) / 32768.0)
	}
}

func loadMonoWAVFloat32(path string) ([]float32, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open wav %q: %w", path, err)
	}

	defer func() {
		_ = file.Close()
	}()

	decoder := wav.NewDecoder(file)
	if !decoder.IsValidFile() {
		return nil, 0, fmt.Errorf("invalid wav file: %s", path)
	}

	intBuffer, err := decoder.FullPCMBuffer()
	if err != nil {
		return nil, 0, fmt.Errorf("decode wav %q: %w", path, err)
	}

	if intBuffer == nil || intBuffer.Format == nil {
		return nil, 0, fmt.Errorf("invalid decoded buffer: %s", path)
	}

	bitDepth := intBuffer.SourceBitDepth
	if bitDepth <= 0 {
		bitDepth = 16
	}

	scale := math.Pow(2, float64(bitDepth-1))

	channels := intBuffer.Format.NumChannels
	if channels <= 0 {
		channels = 1
	}

	samples := make([]float32, len(intBuffer.Data)/channels)
	for i := range samples {
		samples[i] = float32(float64(intBuffer.Data[i*channels]) / scale)
	}

	return samples, intBuffer.Format.SampleRate, nil
}
