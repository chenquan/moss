package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// ExitError carries an application exit category without asking Cobra to print
// a second diagnostic. The process entrypoint maps it to os.Exit.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string { return "" }

// NewRootCommand constructs a fresh machine-only command tree.
func NewRootCommand(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:                "cairn",
		Short:              "Cairn machine protocol entrypoint",
		SilenceUsage:       true,
		SilenceErrors:      true,
		DisableSuggestions: true,
		Args:               cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "cairn accepts the machine operation call or the setup command skill install")
			return &ExitError{Code: 2}
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "cairn exposes the machine call protocol and the explicit skill install setup command")
	})
	root.AddCommand(newCallCommand(stdout, stderr))
	root.AddCommand(newSkillCommand())
	return root
}

// Execute runs the machine command and returns a stable process exit code.
func Execute(stdout, stderr io.Writer) int {
	root := NewRootCommand(stdout, stderr)
	if err := root.Execute(); err != nil {
		if exitErr, ok := err.(*ExitError); ok {
			return exitErr.Code
		}
		_, _ = fmt.Fprintln(stderr, err.Error())
		return 2
	}
	return 0
}
