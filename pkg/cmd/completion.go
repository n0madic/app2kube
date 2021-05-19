package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// NewCmdCompletion return completion command
func NewCmdCompletion() *cobra.Command {
	return &cobra.Command{
		Use:                   "completion [bash|zsh|fish|powershell]",
		DisableFlagsInUseLine: true,
		Short:                 "Generate completion script",
		Long: `To load completions:

	Bash:

	  $ source <(app2kube completion bash)

	  # To load completions for each session, execute once:
	  # Linux:
	  $ app2kube completion bash > /etc/bash_completion.d/app2kube
	  # macOS:
	  $ app2kube completion bash > /usr/local/etc/bash_completion.d/app2kube

	Zsh:

	  # If shell completion is not already enabled in your environment,
	  # you will need to enable it.  You can execute the following once:

	  $ echo "autoload -U compinit; compinit" >> ~/.zshrc

	  # To load completions for each session, execute once:
	  $ app2kube completion zsh > "${fpath[1]}/_app2kube"

	  # You will need to start a new shell for this setup to take effect.

	fish:

	  $ app2kube completion fish | source

	  # To load completions for each session, execute once:
	  $ app2kube completion fish > ~/.config/fish/completions/app2kube.fish

	PowerShell:

	  PS> app2kube completion powershell | Out-String | Invoke-Expression

	  # To load completions for every new session, run:
	  PS> app2kube completion powershell > app2kube.ps1
	  # and source this file from your PowerShell profile.
	`,
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		Args:      cobra.ExactValidArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			switch args[0] {
			case "bash":
				rootCmd.GenBashCompletion(os.Stdout)
			case "zsh":
				rootCmd.GenZshCompletion(os.Stdout)
			case "fish":
				rootCmd.GenFishCompletion(os.Stdout, true)
			case "powershell":
				rootCmd.GenPowerShellCompletionWithDesc(os.Stdout)
			}
		},
	}
}
