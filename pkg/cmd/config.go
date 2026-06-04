package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"
)

// renderDotenv builds the .env representation of an app's configmap, env and
// decrypted secrets, with keys sorted for deterministic output.
func renderDotenv(app *app2kube.App, export, quotes bool) (string, error) {
	cfg := make(map[string]string)
	for k, v := range app.ConfigMap {
		cfg[k] = v
	}
	for k, v := range app.Env {
		cfg[k] = v
	}
	secrets, err := app.GetDecryptedSecrets()
	if err != nil {
		return "", err
	}
	for k, v := range secrets {
		cfg[k] = v
	}

	// Build the key list from the merged map so a key present in more than one
	// source (ConfigMap/Env/Secrets) is emitted once, not once per source.
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	prefix := ""
	if export {
		prefix = "export "
	}
	quote := ""
	if quotes {
		quote = "\""
	}

	var b strings.Builder
	for _, key := range keys {
		b.WriteString(prefix + key + "=" + quote + cfg[key] + quote + "\n")
	}
	return b.String(), nil
}

// collectDomains returns the sorted, de-duplicated list of ingress hosts and
// aliases for an app. Aliases go through app.IngressAliases (the single source
// of the staging-suppression rule) so the listed domains match what GetIngress
// actually serves — under staging, aliases are dropped from both.
func collectDomains(app *app2kube.App) []string {
	var domains []string
	for _, ingress := range app.Ingress {
		domains = append(domains, ingress.Host)
		domains = append(domains, app.IngressAliases(ingress)...)
	}
	if len(domains) == 0 {
		return nil
	}
	sort.Strings(domains)
	j := 0
	for i := 1; i < len(domains); i++ {
		if domains[j] == domains[i] {
			continue
		}
		j++
		domains[j] = domains[i]
	}
	return domains[:j+1]
}

// renderSecrets builds the YAML-ish listing of decrypted secrets, sorted by key.
func renderSecrets(app *app2kube.App) (string, error) {
	secrets, err := app.GetDecryptedSecrets()
	if err != nil {
		return "", err
	}
	keys := make([]string, 0, len(secrets))
	for k := range secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("secrets:\n")
	for _, key := range keys {
		b.WriteString("  " + key + ": " + secrets[key] + "\n")
	}
	return b.String(), nil
}

// NewCmdConfig return config command
func NewCmdConfig() *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage application config",
	}

	// addConfigSub wires a config subcommand with its own appOptions. These
	// commands inject a default application name so they work without one.
	addConfigSub := func(use, short string, run func(cmd *cobra.Command, app *app2kube.App) error) *cobra.Command {
		c := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs}
		opts := addAppFlags(c)
		_ = c.Flags().MarkHidden("include-namespace")
		_ = c.Flags().MarkHidden("snapshot")
		c.RunE = func(cmd *cobra.Command, args []string) error {
			opts.stringValues = append(opts.stringValues, "name=app")
			app, err := opts.initApp(cmd.Context())
			if err != nil {
				return err
			}
			cmd.SilenceUsage = true
			return run(cmd, app)
		}
		configCmd.AddCommand(c)
		return c
	}

	dotenv := addConfigSub("dotenv", "Print the config as .env", func(cmd *cobra.Command, app *app2kube.App) error {
		exportFlag, _ := cmd.Flags().GetBool("export")
		quoteFlag, _ := cmd.Flags().GetBool("quotes")
		out, err := renderDotenv(app, exportFlag, quoteFlag)
		if err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	})
	dotenv.Flags().BoolP("export", "e", false, "Print export statements")
	dotenv.Flags().BoolP("quotes", "q", false, "Print quotes around values")

	addConfigSub("domain", "Print the list of domains from ingress", func(cmd *cobra.Command, app *app2kube.App) error {
		for _, domain := range collectDomains(app) {
			fmt.Println(domain)
		}
		return nil
	})

	addConfigSub("secrets", "Print decrypted secrets", func(cmd *cobra.Command, app *app2kube.App) error {
		out, err := renderSecrets(app)
		if err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	})

	var (
		encryptString string
		encryptFiles  app2kube.ValueFiles
	)
	encryptCmd := &cobra.Command{
		Use:   "encrypt",
		Short: "Encrypt secret values",
		Long:  "Encrypts values in secrets section for specified YAML files. The result is written to the same file.\nSet the APP2KUBE_PASSWORD environment variable to encrypt with AES.\nSet the APP2KUBE_ENCRYPT_KEY environment variable to encrypt with RSA.\nUse the `config generate-keys` command to generate RSA keys.\nRSA has priority over AES if both keys are specified.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			str, err := resolveEncryptString(encryptString, os.Stdin)
			if err != nil {
				return err
			}
			return runEncrypt(str, encryptFiles)
		},
	}
	encryptCmd.Flags().StringVarP(&encryptString, "string", "", "", "Encrypt the specified string ('-' reads it from stdin to keep it out of shell history)")
	encryptCmd.Flags().VarP(&encryptFiles, "values", "f", "Encrypt secrets in a file (can specify multiple)")
	configCmd.AddCommand(encryptCmd)

	configCmd.AddCommand(&cobra.Command{
		Use:   "generate-keys",
		Short: "Generate RSA keys",
		Long:  "Generates RSA-2048 encrypt and decrypt keys",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			publicKey, privateKey, err := app2kube.GenerateRSAKeys(2048)
			if err != nil {
				return err
			}
			fmt.Println("export " + app2kube.EnvEncryptKey + "=" + publicKey)
			fmt.Println()
			fmt.Println("export " + app2kube.EnvDecryptKey + "=" + privateKey)
			fmt.Println()
			return nil
		},
	})

	return configCmd
}
