package cmd

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// defaultConfigAppName is the placeholder name the read-only `config`
// subcommands fall back to when the values carry none. Their output (env vars,
// domains, secrets) does not depend on it.
const defaultConfigAppName = "app"

// dotenvSafe matches values that can be written to a .env file unquoted: no
// whitespace, newlines, quotes, backslashes or shell metacharacters that would
// change how the line parses.
var dotenvSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// dotenvValue renders a value for a .env line. An unsafe value is always
// double-quoted and escaped, even without --quotes: a secret containing a newline
// would otherwise start a new line that parses as another variable assignment
// (`foo\nINJECTED=bar` injects INJECTED), and a value containing a double quote
// would break out of the quoting --quotes adds. strconv.Quote's escapes (\n, \",
// \\) are the ones .env parsers understand inside double quotes.
func dotenvValue(v string, quotes bool) string {
	if quotes || !dotenvSafe.MatchString(v) {
		return strconv.Quote(v)
	}
	return v
}

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

	var b strings.Builder
	for _, key := range keys {
		b.WriteString(prefix + key + "=" + dotenvValue(cfg[key], quotes) + "\n")
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

// renderSecrets builds the YAML listing of decrypted secrets, sorted by key.
//
// The values are emitted through the YAML encoder rather than concatenated, so a
// secret containing a newline, a colon, a leading '#' or a quote produces valid,
// round-trippable YAML. Hand-built "  key: value" lines silently emitted a broken
// document for any such secret. Keys are ordered explicitly (a yaml.Node mapping)
// so the output stays deterministic.
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

	inner := &yaml.Node{Kind: yaml.MappingNode}
	for _, key := range keys {
		inner.Content = append(inner.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: secrets[key]},
		)
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "secrets"},
		inner,
	}}

	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
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
	// commands default the application name so they work without one.
	addConfigSub := func(use, short string, run func(cmd *cobra.Command, app *app2kube.App) error) *cobra.Command {
		c := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs}
		opts := addAppFlags(c)
		// The output of these subcommands never depends on the application name, so
		// they neither require a value source nor a `name` in the values they are
		// given (a values file with just an `env:` section is a valid input). A name
		// the user does set still wins — the default is seeded before the values are
		// parsed, not forced on top of them.
		opts.nameOptional = true
		_ = c.Flags().MarkHidden("include-namespace")
		c.RunE = func(cmd *cobra.Command, args []string) error {
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
			cmd.SilenceUsage = true
			return runEncrypt(str, cmd.Flags().Changed("string"), encryptFiles)
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
