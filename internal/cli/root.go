package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/cwbudde/glockenspiel/assets"
	"github.com/cwbudde/glockenspiel/internal/preset"
	"github.com/spf13/cobra"
)

// errorPrefix identifies the program in diagnostics, the way a Unix tool does.
const errorPrefix = "glockenspiel: "

// version is set via ldflags in release builds.
var version = "dev"

// NewRootCmd builds the glockenspiel root command.
func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "glockenspiel",
		Short: "Physical model glockenspiel synthesizer",
		// Usage on a runtime failure is noise, and Run below owns the single
		// error message so cobra must not print a second one.
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.AddCommand(
		newSynthCmd(),
		newFitCmd(),
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

// Execute runs the root command on the process streams and returns its exit code.
func Execute() int {
	return Run(os.Args[1:], os.Stdout, os.Stderr)
}

// loadPresetOrDefault loads a preset from path, falling back to the embedded
// default when no path was given. A relative default such as
// assets/presets/default.json only resolves when the process happens to run
// from the repository root, which an installed binary never does.
func loadPresetOrDefault(path string) (*preset.Preset, error) {
	if path == "" {
		return assets.DefaultPreset()
	}

	return preset.Load(path)
}
