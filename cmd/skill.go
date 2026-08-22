package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	mossskill "github.com/chenquan/moss/internal/skill"
)

func newSkillCommand() *cobra.Command {
	skillCommand := &cobra.Command{
		Use:           "skill",
		Short:         "Install the Moss Skill resources",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
	}

	var targets []string
	var scope string
	var force bool
	installCommand := &cobra.Command{
		Use:           "install",
		Short:         "Install the bundled Moss Skill",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			results, err := mossskill.Install(mossskill.InstallOptions{Targets: targets, Scope: scope, Force: force})
			if err != nil {
				return err
			}
			for _, result := range results {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "installed Moss Skill for %s (%s) at %s: %d written, %d unchanged\n", result.Target, result.Scope, result.Destination, result.Installed, result.Skipped)
			}
			return nil
		},
	}
	installCommand.Flags().StringArrayVar(&targets, "target", []string{mossskill.TargetClaude}, "target editor; repeat for multiple targets: claude or codex")
	installCommand.Flags().StringVar(&scope, "scope", mossskill.ScopeGlobal, "installation scope: global or project")
	installCommand.Flags().BoolVar(&force, "force", false, "delete and recreate the entire Skill directory, including unrelated files")
	installCommand.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), cmd.UsageString())
	})
	skillCommand.AddCommand(installCommand)
	return skillCommand
}
