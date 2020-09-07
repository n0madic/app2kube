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

	configCmd.AddCommand(&cobra.Command{
		Use:   "domain",
		Short: "Print the list of domains from ingress",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := initApp()
			if err != nil {
				return err
			}
			cmd.SilenceUsage = true

			var domains []string
			for _, ingress := range app.Ingress {
				domains = append(domains, ingress.Host)
				domains = append(domains, ingress.Aliases...)
			}
			sort.Strings(domains)

			// Deduplicate domains
			j := 0
			for i := 1; i < len(domains); i++ {
				if domains[j] == domains[i] {
					continue
				}
				j++
				domains[j] = domains[i]
			}
			domains = domains[:j+1]

			for _, domain := range domains {
				fmt.Println(domain)
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
