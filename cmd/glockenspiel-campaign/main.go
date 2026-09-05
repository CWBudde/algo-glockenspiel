// Command glockenspiel-campaign plans, runs, collects and analyses a designed
// comparison of optimizer arms.
//
// It is a separate binary rather than a subcommand of glockenspiel because a
// campaign is identified by the executable that planned it: the manifest
// records the file's SHA-256 and run refuses a different one. A binary that
// also carried the synthesiser, the server and the WASM entry points would
// change its hash for reasons that have nothing to do with the search, and
// every half-finished campaign on the machine would stop resuming.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// errorPrefix identifies the program in diagnostics, the way a Unix tool does.
const errorPrefix = "glockenspiel-campaign: "

// campaignRoot is where a campaign directory lands when nobody names one. It
// sits under out/, which is gitignored, because a campaign writes a run
// directory per job and a full design is gigabytes.
const campaignRoot = "out/campaign"

// NewRootCmd builds the campaign root command.
//
// The command tree is built by a constructor rather than assembled in an
// initialiser so a test can execute it against its own arguments and its own
// output streams, which is the only way the flag wiring is covered at all.
func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "glockenspiel-campaign",
		Short: "Plan, run, collect and analyse optimizer campaigns",
		Long: "A campaign runs every arm of a registered design on every one of a set of paired seed " +
			"blocks, at a matched evaluation budget, and records enough of each run to be argued " +
			"about later. See docs/campaign.md.",
		// Usage on a runtime failure is noise, and Run below owns the single
		// error message so cobra must not print a second one.
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.AddCommand(
		newListCmd(),
		newPlanCmd(),
		newRunCmd(),
		newStatusCmd(),
		newCollectCmd(),
		newAnalyzeCmd(),
		newPackCmd(),
		newVersionCmd(),
	)

	return rootCmd
}

// Run executes the root command against explicit streams and returns the
// process exit code. Failures are reported on stderr exactly once.
func Run(args []string, stdout, stderr io.Writer) int {
	rootCmd := NewRootCmd()
	rootCmd.SetArgs(args)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)

	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(stderr, errorPrefix+err.Error())

		return 1
	}

	return 0
}

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}

// repositoryRoot walks up from the working directory to the directory holding
// go.mod.
//
// A design names its reference as a repository-relative path, because that is
// the name a reviewer reading designs.go can check, and the design's hash has
// to be the same wherever it was planned from. So plan resolves that name
// against the repository rather than against whatever directory the recipe was
// invoked in.
func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("locate the working directory: %w", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf(
				"no go.mod above the working directory: plan resolves a design's reference against the " +
					"repository, so it has to be run from inside the checkout")
		}

		dir = parent
	}
}
