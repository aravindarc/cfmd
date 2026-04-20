// Package cli wires every subcommand onto a cobra root.
package cli

import (
	"github.com/spf13/cobra"
)

// NewRootCommand returns the root `cfmd` command. main.go invokes
// `ExecuteContext` on it.
func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "cfmd",
		Short:         "Sync Confluence pages with local markdown files",
		Long:          "cfmd pulls and pushes Confluence pages as local markdown files, with a diff-and-confirm workflow that prevents accidental overwrites.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.PersistentFlags().BoolP("verbose", "v", false, "verbose output to stderr")

	cmd.AddCommand(newInitCommand())
	cmd.AddCommand(newPushCommand())
	cmd.AddCommand(newPullCommand())
	cmd.AddCommand(newDiffCommand())
	cmd.AddCommand(newStatusCommand())
	cmd.AddCommand(newConvertCommand())

	return cmd
}
