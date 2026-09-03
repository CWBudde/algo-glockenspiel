package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/cwbudde/algo-glockenspiel/internal/campaign"
	"github.com/cwbudde/algo-glockenspiel/internal/fitrun"
	"github.com/spf13/cobra"
)

// seedHuntDesign is the one design whose arms are derived at plan time, so it
// is the only one --winner applies to.
const seedHuntDesign = "seed-hunt"

// engineShapeDesign is where a seed-hunt winner comes from. Naming an arm of
// any other design would compare a refinement against a shape nothing measured.
const engineShapeDesign = "engine-shape"

// newListCmd prints the registered designs.
//
// The list is the answer to "what can I run", and it prints the budget and the
// job count because those are what decide whether a design is a minute or an
// hour of machine time.
func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the registered designs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			for index, design := range campaign.Registered() {
				if index > 0 {
					_, _ = fmt.Fprintln(out)
				}

				names := make([]string, 0, len(design.Arms))
				for _, arm := range design.Arms {
					names = append(names, arm.Name)
				}

				_, _ = fmt.Fprintf(out, "%s: %s\n", design.Name, design.Description)
				_, _ = fmt.Fprintf(out, "  %d blocks x %d arms = %d jobs, %d evaluations each\n",
					design.Blocks, len(design.Arms), design.Blocks*len(design.Arms), design.Budget)
				_, _ = fmt.Fprintf(out, "  reference %s at note %d under %s\n",
					design.Reference, design.Note, design.Profile)
				_, _ = fmt.Fprintf(out, "  arms: %s\n", strings.Join(names, ", "))
			}

			return nil
		},
	}
}

// newPlanCmd writes a campaign's manifest and prints what it committed to.
func newPlanCmd() *cobra.Command {
	var (
		dir     string
		workers int
		winner  string
	)

	cmd := &cobra.Command{
		Use:   "plan <design>",
		Short: "Write a campaign directory's manifest",
		Long: "Plan resolves the reference, the binary and the seeds, then writes manifest.json once. " +
			"An existing manifest is refused rather than replaced, so a design cannot be edited " +
			"after any of its results are visible.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			design, err := designToPlan(args[0], winner)
			if err != nil {
				return err
			}

			target, err := planDirectory(dir, design.Name)
			if err != nil {
				return err
			}

			manifest, err := campaign.Plan(design, target, workers)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprint(cmd.OutOrStdout(), campaign.PlanTable(manifest))
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nmanifest %s\n",
				filepath.Join(target, campaign.FileManifest))

			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "campaign directory (default out/campaign/<design>)")
	cmd.Flags().IntVar(&workers, "workers", 0,
		"objective worker width to pin for every job (default the machine's CPU count)")
	cmd.Flags().StringVar(&winner, "winner", "",
		"the engine-shape cmaes arm seed-hunt refines (seed-hunt only)")

	return cmd
}

// designToPlan resolves the design name, and the --winner flag which only
// seed-hunt accepts.
//
// The flag exists at all because seed-hunt's arms are the winner of
// engine-shape at two population sizes, and which arm that is cannot be known
// until 8.6 has run engine-shape. Every other design is a value in the source
// and takes no flags, so a plan is reproducible from its name alone.
func designToPlan(name, winner string) (campaign.Design, error) {
	if winner == "" {
		return campaign.Lookup(name)
	}

	if name != seedHuntDesign {
		return campaign.Design{}, fmt.Errorf(
			"--winner shapes the %s design and design %q takes no flags: its arms are registered in "+
				"internal/campaign/designs.go", seedHuntDesign, name)
	}

	shape, err := campaign.Lookup(engineShapeDesign)
	if err != nil {
		return campaign.Design{}, err
	}

	arm, err := shape.ArmByName(winner)
	if err != nil {
		return campaign.Design{}, err
	}

	return campaign.SeedHunt(arm)
}

// planDirectory resolves the campaign directory and moves the process to the
// repository root, where a design's reference path is meaningful.
//
// The directory is made absolute first, against the directory the command was
// invoked in, so that a relative --dir still means what the caller typed.
func planDirectory(dir, design string) (string, error) {
	root, err := repositoryRoot()
	if err != nil {
		return "", err
	}

	target := filepath.Join(root, campaignRoot, design)

	if dir != "" {
		if target, err = filepath.Abs(dir); err != nil {
			return "", fmt.Errorf("resolve campaign directory %q: %w", dir, err)
		}
	}

	if err := os.Chdir(root); err != nil {
		return "", fmt.Errorf("enter the repository root %q: %w", root, err)
	}

	return target, nil
}

// newRunCmd runs the jobs of a planned campaign.
func newRunCmd() *cobra.Command {
	var (
		dir       string
		limit     int
		onlyBlock int
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the jobs of a planned campaign",
		Long: "Jobs run in this process, one at a time, at the worker width the manifest pinned. " +
			"A finished job is skipped, so an interrupted campaign resumes where it stopped. " +
			"SIGINT and SIGTERM stop it after the job in flight, which is then repeated.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			opts := campaign.RunOptions{Limit: limit, OnlyBlock: onlyBlock}

			err := campaign.Run(ctx, dir, cmd.OutOrStdout(), opts)
			if err == nil {
				return nil
			}

			if ctx.Err() != nil {
				// The run's own error is kept beside the cancellation notice.
				// A job can fail in the same moment a signal arrives, and
				// discarding that error would leave the campaign looking like
				// it was merely interrupted.
				return errors.Join(cancellationError(dir), err)
			}

			return err
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "campaign directory holding manifest.json")
	cmd.Flags().IntVar(&limit, "limit", 0, "stop after this many jobs have run (0 runs them all)")
	// Minus one rather than zero, because block zero is a real block and a
	// default of zero would quietly run one twelfth of the campaign.
	cmd.Flags().IntVar(&onlyBlock, "only-block", -1, "run only this block (-1 runs every block)")
	_ = cmd.MarkFlagRequired("dir")

	return cmd
}

// cancellationError names the jobs a signal cut short.
//
// campaign.Run returns the context's own error, which says nothing about where
// the campaign got to, and the useful thing to tell someone who just pressed
// Ctrl-C is which job will be repeated when they resume. A cut job is one whose
// summary reports a cancelled context; fitrun writes a run directory whatever
// happened, so the file is there to read.
//
// Every such job is named rather than only the first in the manifest. An
// earlier campaign in the same directory may have been cut too, and with
// --only-block or --limit the job just cut need not be the earliest one the
// manifest lists, so naming the first would name the wrong job.
func cancellationError(dir string) error {
	cut, err := canceledJobs(dir)
	if err != nil || len(cut) == 0 {
		return fmt.Errorf("the campaign was cancelled before it finished; run it again to resume")
	}

	return fmt.Errorf(
		"the campaign was cancelled; %s spent a fraction of the budget, so run "+
			"clears and repeats %s when the campaign is resumed",
		jobPhrase(cut), pluralThem(len(cut)))
}

// jobPhrase names the cut jobs the way a sentence wants them.
func jobPhrase(jobs []string) string {
	if len(jobs) == 1 {
		return "job " + jobs[0]
	}

	return "jobs " + strings.Join(jobs, ", ")
}

// pluralThem is the pronoun the cut jobs take.
func pluralThem(count int) string {
	if count == 1 {
		return "it"
	}

	return "them"
}

// canceledJobs returns the IDs of every job of the campaign whose summary
// reports a cancelled context, in manifest order.
func canceledJobs(dir string) ([]string, error) {
	manifest, err := campaign.ReadManifest(dir)
	if err != nil {
		return nil, err
	}

	var cut []string

	for _, job := range manifest.Jobs {
		raw, err := os.ReadFile(filepath.Join(dir, job.Dir, fitrun.FileResult))
		if err != nil {
			continue
		}

		var summary fitrun.Summary

		if err := json.Unmarshal(raw, &summary); err != nil {
			continue
		}

		if summary.StopReason == "context_canceled" {
			cut = append(cut, job.ID)
		}
	}

	return cut, nil
}

// newCollectCmd turns the run directories into one results file.
func newCollectCmd() *cobra.Command {
	var (
		dir     string
		partial bool
	)

	cmd := &cobra.Command{
		Use:   "collect",
		Short: "Write results.csv from a campaign's run directories",
		Long: "Every job is scored at the best cost its trace recorded at or below the budget, which is " +
			"what makes two backends comparable when a generation is atomic and a run may overrun " +
			"the cap by one of them.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := campaign.Collect(dir, partial)
			if err != nil {
				return err
			}

			rows, err := campaign.ReadResults(path)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s, %d rows\n", path, len(rows))

			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "campaign directory holding manifest.json")
	cmd.Flags().BoolVar(&partial, "partial", false,
		"collect a campaign that is not finished, leaving out the jobs that have not run")
	_ = cmd.MarkFlagRequired("dir")

	return cmd
}

// newAnalyzeCmd rebuilds the report from a results file.
func newAnalyzeCmd() *cobra.Command {
	var (
		dir string
		csv string
		out string
	)

	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Rebuild a campaign's report from results.csv",
		Long: "The report is rebuilt from the results file alone, so an archived campaign is one file " +
			"rather than a tree of run directories.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var path string

			switch {
			case csv != "" && dir != "":
				return fmt.Errorf("--dir and --csv name the same input, so give one of them")
			case csv != "":
				path = csv
			case dir != "":
				path = filepath.Join(dir, campaign.FileResults)
			default:
				return fmt.Errorf("give --dir, or --csv for a results file that was archived on its own")
			}

			report, err := campaign.Analyze(path)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprint(cmd.OutOrStdout(), campaign.RenderMarkdown(report))

			if out == "" {
				return nil
			}

			return campaign.WriteReport(report, out)
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "campaign directory, whose results.csv is analysed")
	cmd.Flags().StringVar(&csv, "csv", "", "results file to analyse")
	cmd.Flags().StringVar(&out, "out", "", "also write the report to this file")

	return cmd
}

// newVersionCmd prints what a manifest would record about this binary.
//
// It is the way to check, before resuming an hours-old campaign, whether the
// binary on disk is still the one that planned it: run refuses a different
// hash and the hash printed here is the one it compares.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build identity and this binary's SHA-256",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			identity := fitrun.ReadIdentity()
			out := cmd.OutOrStdout()

			_, _ = fmt.Fprintf(out, "go %s\n", identity.Go)
			_, _ = fmt.Fprintf(out, "revision %s\n", identity.Revision)
			_, _ = fmt.Fprintf(out, "modified %t\n", identity.Modified)
			_, _ = fmt.Fprintf(out, "time %s\n", identity.Time)
			_, _ = fmt.Fprintf(out, "%s %s\n", fitrun.MayflyLibrary, identity.Libraries[fitrun.MayflyLibrary])
			_, _ = fmt.Fprintf(out, "%s %s\n", fitrun.CMAESLibrary, identity.Libraries[fitrun.CMAESLibrary])

			path, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locate the running binary: %w", err)
			}

			sum, err := fitrun.FileSHA256(path)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(out, "binary sha256 %s\n", sum)

			return nil
		},
	}
}
