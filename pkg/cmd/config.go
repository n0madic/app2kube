package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
)

// NewCmdConfig return config command
func NewCmdConfig() *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage application config",
	}

	configCmd.AddCommand(&cobra.Command{
		Use:   "dotenv",
		Short: "Print the config as .env",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := initApp()
			if err != nil {
				return err
			}
			cmd.SilenceUsage = true

			cfg := make(map[string]string)
			keys := make([]string, 0, len(app.ConfigMap)+len(app.Env)+len(app.Secrets))
			for k, v := range app.ConfigMap {
				keys = append(keys, k)
				cfg[k] = v
			}
			for k, v := range app.Env {
				keys = append(keys, k)
				cfg[k] = v
			}
			secrets, err := app.GetDecryptedSecrets()
			if err != nil {
				return err
			}
			for k, v := range secrets {
				keys = append(keys, k)
				cfg[k] = v
			}
			sort.Strings(keys)

			for _, key := range keys {
				fmt.Println(key + "=" + cfg[key])
			}
			return nil
		},
	})

	for _, cmd := range configCmd.Commands() {
		addAppFlags(cmd)
		cmd.Flags().MarkHidden("include-namespace")
	}

	return configCmd
}
