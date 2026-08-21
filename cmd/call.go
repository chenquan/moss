package cmd

import (
	"io"

	"github.com/spf13/cobra"
	"moss/internal/app"
)

func newCallCommand(stdout, stderr io.Writer) *cobra.Command {
	var requestPath, responsePath string

	call := &cobra.Command{
		Use:           "call",
		Short:         "Machine protocol call",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			code := app.RunCall(requestPath, responsePath, stdout, stderr)
			if code != app.ExitOK {
				return &ExitError{Code: code}
			}
			return nil
		},
	}
	call.Flags().StringVar(&requestPath, "request", "", "request JSON file")
	call.Flags().StringVar(&responsePath, "response", "", "response JSON file")
	_ = call.MarkFlagRequired("request")
	_ = call.MarkFlagRequired("response")
	return call
}
