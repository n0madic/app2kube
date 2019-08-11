package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// NewCmdCompletion return completion command
func NewCmdCompletion() *cobra.Command {
	return &cobra.Command{
		Use:                   "completion",
		DisableFlagsInUseLine: true,
		Short:                 "Generates bash completion scripts",
		Long: `To load completion run

	. <(app2kube completion)

	To configure your bash shell to load completions for each session add to your bashrc

	# ~/.bashrc or ~/.profile
	. <(app2kube completion)
	`,
		Run: func(cmd *cobra.Command, args []string) {
			rootCmd.GenBashCompletion(os.Stdout)
		},
	}
}
