package cli

import "github.com/spf13/cobra"

// version is set via ldflags in release builds.
var version = "dev"

// NewRootCmd builds the glockenspiel root command.
func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "glockenspiel",
		Short:         "Physical model glockenspiel synthesizer",
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

// Execute runs the root command.
func Execute() error {
	return NewRootCmd().Execute()
}
