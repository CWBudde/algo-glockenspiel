package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

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

	cmd.AddCommand(newPackPlanCmd(), newPackRunCmd(), newPackCollectCmd())

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
