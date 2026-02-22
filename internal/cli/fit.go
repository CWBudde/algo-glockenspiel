package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

func newFitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fit",
		Short: "Fit model parameters to a reference recording",
		Long:  "Optimize model parameters against a target audio file (Phase 2 implementation pending).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("fit command is not implemented yet")
		},
	}
}
