package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cwbudde/algo-glockenspiel/internal/optimizer"
	"github.com/cwbudde/algo-glockenspiel/internal/pack"
	"github.com/spf13/cobra"
)

// packSeedBase is the first seed a pack run uses.
//
// It is 130,000 because the registered campaign designs occupy 120,000 through
// 125,000 and the bases must not overlap at all. Phase 8.6 found that deriving
// a round's random stream from a seed by arithmetic coupled runs that were
// meant to be independent, and a coupled set understates its own spread; the
// cheap defence is that no two things sharing a machine ever share a base.
const packSeedBase = 130_000

// newPackCmd is the command group that fits a directory of per-note recordings.
//
// It lives beside the campaign commands rather than in the main CLI because it
// shares their discipline -- a manifest written once, a binary and every
// recording pinned by hash, a resumable run -- and none of the main CLI's. It
// is deliberately not a campaign design: a design compares arms against one
// recording, and twenty notes are not twenty arms.
func newPackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pack",
		Short: "Fit a directory of per-note recordings, one fit per note",
		Long: "A pack is a directory of single-strike recordings, one note per file, named by the note " +
			"each file sounds. plan resolves every file to its note by measuring it, run fits them one " +
			"at a time, and collect writes the tables a note-versus-partial regression reads.",
	}

	cmd.AddCommand(newPackPlanCmd(), newPackRunCmd(), newPackCollectCmd(), newPackFitJointCmd(),
		newPackScoreCmd(), newPackRegressCmd(), newPackStatusCmd())

	return cmd
}

func newPackPlanCmd() *cobra.Command {
	var (
		dir      string
		modes    int
		budget   int
		seedBase int64
		maxCents float64
		workers  int
		profile  string
	)

	cmd := &cobra.Command{
		Use:   "plan <pack-directory>",
		Short: "Measure a pack and write its run manifest",
		Long: "Every recording is measured and resolved to the note it actually sounds, which is the " +
			"authority rather than the file name: Freesound strips '#' from an upload's title, so ten " +
			"of the hollandm pack's twenty files arrived sharing a name with their own sharp. A file " +
			"whose name and pitch disagree is refused rather than fitted a semitone away from itself.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			metric, err := optimizer.ParseMetric(profile)
			if err != nil {
				return err
			}

			manifest, err := pack.Plan(args[0], dir, pack.Options{
				Modes:    modes,
				Budget:   budget,
				SeedBase: seedBase,
				MaxCents: maxCents,
				Workers:  workers,
				Profile:  metric,
			})
			if err != nil {
				return err
			}

			_, _ = fmt.Fprint(cmd.OutOrStdout(), pack.Table(manifest))

			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "out", "", "directory to write the run into")
	cmd.Flags().IntVar(&modes, "modes", 0,
		"partials to seed per note (0 takes every partial the analysis found)")
	cmd.Flags().IntVar(&budget, "budget", 24_000, "evaluation cap per note")
	cmd.Flags().Int64Var(&seedBase, "seed-base", packSeedBase, "first note's seed")
	cmd.Flags().Float64Var(&maxCents, "max-cents", pack.DefaultMaxCents,
		"how far from equal temperament a recording may sit before it is refused")
	cmd.Flags().IntVar(&workers, "workers", 0, "parallel evaluation width (0 follows the machine)")
	cmd.Flags().StringVar(&profile, "profile", string(optimizer.MetricBalanced), "objective profile")
	_ = cmd.MarkFlagRequired("out")

	return cmd
}

func newPackRunCmd() *cobra.Command {
	var (
		dir      string
		limit    int
		onlyNote int
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Fit the notes of a planned pack",
		Long: "Notes are fitted in this process, one at a time, at the worker width the manifest pinned. " +
			"A finished note is skipped, so an interrupted run resumes where it stopped. SIGINT and " +
			"SIGTERM stop it after the note in flight, which is then repeated.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			return pack.Run(ctx, dir, cmd.OutOrStdout(), pack.RunOptions{Limit: limit, OnlyNote: onlyNote})
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "pack run directory holding manifest.json")
	cmd.Flags().IntVar(&limit, "limit", 0, "stop after this many notes have run (0 runs them all)")
	cmd.Flags().IntVar(&onlyNote, "only-note", 0, "fit only this MIDI note (0 runs every note)")
	_ = cmd.MarkFlagRequired("dir")

	return cmd
}

func newPackFitJointCmd() *cobra.Command {
	var (
		dir          string
		out          string
		budget       int
		authoredNote int
		modes        int
		seed         int64
		workers      int
		notes        []int
		keytrack     bool
		pooledSeed   bool
		coverage     float64
	)

	cmd := &cobra.Command{
		Use:   "fit-joint",
		Short: "Fit one preset against every note of a planned pack at once",
		Long: "The candidate is authored at one note and transposed to each recording's own note, and " +
			"its score is the mean of the per-note composite scores. That is what fitting an " +
			"instrument means rather than one of its bars: the search looks for the bar whose " +
			"transposition covers the whole range, not for the bar that best fits any one recording. " +
			"The budget is for the whole fit, and one evaluation renders every note.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			outcome, fitted, err := pack.FitJoint(ctx, dir, out, cmd.OutOrStdout(), pack.JointOptions{
				Budget:       budget,
				AuthoredNote: authoredNote,
				Notes:        notes,
				Modes:        modes,
				Seed:         seed,
				Workers:      workers,

				SearchDecayKeytrack: keytrack,
				SeedFromModes:       pooledSeed,
				SeedCoverage:        coverage,
			})
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"joint score %.6f over %d notes, authored at %d, %d evaluations, %d/%d pinned\n",
				outcome.Summary.Score, len(fitted),
				outcome.Preset.Note, outcome.Summary.Evaluations,
				outcome.Summary.Pinned, outcome.Summary.Dimension)

			if beta := outcome.Preset.Parameters.DecayKeytrack; beta != nil {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "decay keytrack %.4f\n", *beta)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "planned pack run directory holding manifest.json")
	cmd.Flags().StringVar(&out, "out", "", "directory to write the joint fit into")
	cmd.Flags().IntVar(&budget, "budget", 24_000, "evaluation cap for the whole fit")
	cmd.Flags().IntVar(&authoredNote, "authored-note", 0,
		"note the preset is authored at (0 takes the median of the pack's notes)")
	cmd.Flags().IntVar(&modes, "modes", 0, "partials to seed (0 takes every partial at the authored note)")
	cmd.Flags().Int64Var(&seed, "seed", 1, "random stream")
	cmd.Flags().IntVar(&workers, "workers", 0, "parallel evaluation width (0 follows the machine)")
	cmd.Flags().IntSliceVar(&notes, "notes", nil,
		"fit only these MIDI notes (empty fits the whole pack)")
	cmd.Flags().BoolVar(&keytrack, "keytrack", false,
		"search the decay key-tracking exponent instead of holding it at 1 (needs notes an octave apart)")
	cmd.Flags().BoolVar(&pooledSeed, "pooled-seed", false,
		"seed the modes from every note's fit at once, by clustering pack-modes.csv, "+
			"instead of from the single recording at the authored note (needs `pack collect`)")
	cmd.Flags().Float64Var(&coverage, "seed-coverage", pack.DefaultSeedCoverage,
		"share of the pack's notes a partial must appear at to be seeded")
	_ = cmd.MarkFlagRequired("dir")
	_ = cmd.MarkFlagRequired("out")

	return cmd
}

func newPackRegressCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "regress",
		Short: "Regress the fitted modes against pitch",
		Long: "Reads pack-modes.csv and asks the two questions a generalised preset depends on: does the " +
			"decay follow the model's key-tracking law, and is the modal structure a fixed ratio-scale " +
			"of one bar. The scatter each regression leaves is the more useful half of the answer -- it " +
			"is what no law can remove, because it is the difference between one bar and the next.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := pack.WriteReport(dir, filepath.Base(dir))
			if err != nil {
				return err
			}

			_, _ = fmt.Fprint(cmd.OutOrStdout(), body)

			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "pack run directory holding pack-modes.csv")
	_ = cmd.MarkFlagRequired("dir")

	return cmd
}

func newPackScoreCmd() *cobra.Command {
	var (
		dir     string
		presets []string
		out     string
	)

	cmd := &cobra.Command{
		Use:   "score",
		Short: "Score presets against every note of a pack",
		Long: "Writes the transposition matrix: one row per preset, one column per note, plus the row " +
			"mean. That is the comparison this phase turns on -- a preset fitted to one bar and " +
			"transposed across the range against one fitted to the range at once -- and the per-note " +
			"columns are what stop the mean being the only number, since a preset can carry a good " +
			"mean while being useless at one end of the keyboard.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rows, notes, err := pack.ScorePresets(dir, presets, 0, 0)
			if err != nil {
				return err
			}

			table := pack.MatrixCSV(rows, notes)

			if out != "" {
				if err := pack.WriteCSVFile(out, table); err != nil {
					return err
				}
			}

			for _, row := range rows {
				kind := ""
				if row.Joint {
					kind = "  (joint)"
				}

				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-28s authored %3d  mean %.6f%s\n",
					row.Name, row.Note, row.Mean, kind)
			}

			comparison := pack.Compare(rows)

			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"\ndiagonal mean %.6f over %d notes -- every note fitted to itself\n",
				comparison.DiagonalMean, comparison.DiagonalN)

			if comparison.BestSingleName != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(),
					"best single-note preset %s, mean %.6f\n",
					comparison.BestSingleName, comparison.BestSingle)
			}

			if !math.IsNaN(comparison.JointMean) {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(),
					"joint mean %.6f\nprice of one preset covering %d notes: %+.6f\n",
					comparison.JointMean, comparison.DiagonalN, comparison.Price)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "planned pack run directory holding manifest.json")
	cmd.Flags().StringSliceVar(&presets, "preset", nil, "preset files to score (repeatable)")
	cmd.Flags().StringVar(&out, "out", "", "write the matrix as CSV to this path")
	_ = cmd.MarkFlagRequired("dir")
	_ = cmd.MarkFlagRequired("preset")

	return cmd
}

func newPackCollectCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "collect",
		Short: "Write the per-note and per-mode tables of a finished pack run",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			notes, err := pack.Collect(dir)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "collected %d notes into %s and %s\n",
				notes, pack.FileResults, pack.FileModeResults)

			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "pack run directory holding manifest.json")
	_ = cmd.MarkFlagRequired("dir")

	return cmd
}

// newPackStatusCmd is how you find out where a fit has got to without being at
// the machine running it.
//
// It reads the run directory rather than talking to the process, so it works
// from a second shell, after the shell that started the run has gone, and on a
// run started by someone else. --serve is the same reading over HTTP, for the
// case the pack commands were missing entirely: a joint fit is the better part
// of an hour and until now the only way to follow one was to tail a log on the
// machine it was running on.
func newPackStatusCmd() *cobra.Command {
	var (
		dir   string
		watch time.Duration
		serve string
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report how far a pack run or a joint fit has got",
		Long: "The directory decides what is read: a pack run directory holding a manifest reports " +
			"every note, and a single fit's output directory reports that one fit. Everything comes " +
			"from files the run has already written and flushed, so this never disturbs a run in " +
			"flight and never writes to its directory.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			if serve != "" {
				return pack.Serve(ctx, dir, serve, cmd.OutOrStdout())
			}

			if watch <= 0 {
				status, err := pack.ReadStatus(dir)
				if err != nil {
					return err
				}

				_, _ = fmt.Fprint(cmd.OutOrStdout(), pack.RenderStatus(status))

				return nil
			}

			return watchStatus(ctx, dir, watch, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "pack run directory, or a single fit's output directory")
	cmd.Flags().DurationVar(&watch, "watch", 0,
		"reprint at this interval instead of once (0 prints once)")
	cmd.Flags().StringVar(&serve, "serve", "",
		"serve the progress over HTTP at this address instead of printing it, e.g. :8099")
	_ = cmd.MarkFlagRequired("dir")

	return cmd
}

// watchStatus reprints the status until the context is cancelled.
//
// It redraws by clearing the screen rather than by scrolling. A progress table
// appended once a second is a log of a progress table, and the thing being
// watched is the latest one.
func watchStatus(ctx context.Context, dir string, every time.Duration, out io.Writer) error {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		status, err := pack.ReadStatus(dir)
		if err != nil {
			return err
		}

		_, _ = fmt.Fprintf(out, "\033[H\033[2J%s\n%s", pack.RenderStatus(status), time.Now().Format(time.Kitchen))

		select {
		case <-ctx.Done():
			_, _ = fmt.Fprintln(out)

			return nil
		case <-ticker.C:
		}
	}
}
