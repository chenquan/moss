package cmd

import (
	"github.com/spf13/cobra"
	"moss/internal/app"
)

func newCallCommand() *cobra.Command {
	call := &cobra.Command{
		Use:           "call",
		Short:         "Machine protocol call",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			code := app.RunCallStdio(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			if code != app.ExitOK {
				return &ExitError{Code: code}
			}
			return nil
		},
	}
	return call
}
