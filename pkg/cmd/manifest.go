package cmd

import (
	"fmt"
	"strings"

	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"
)

// NewCmdManifest return manifest command
func NewCmdManifest() *cobra.Command {
	var (
		output     string
		typeOutput []string
	)

	manifestCmd := &cobra.Command{
		Use:   "manifest",
		Short: "Generate kubernetes manifests for an application",
		Args:  cobra.NoArgs,
	}

	manifestCmd.Flags().StringVarP(&output, "output", "o", "yaml", "Output format")
	manifestCmd.Flags().StringArrayVar(&typeOutput, "type", []string{"all"}, "Types of output resources (several can be specified)")
	opts := addAppFlags(manifestCmd)
	addBlueGreenFlag(manifestCmd, opts)

	manifestCmd.RunE = func(cmd *cobra.Command, args []string) error {
		// Don't print full usage on runtime errors (only on arg-parse errors),
		// matching the other subcommands; manifest output is piped to kubectl.
		cmd.SilenceUsage = true

		app, err := opts.initApp(cmd.Context())
		if err != nil {
			return err
		}

		out, err := buildManifest(app, typeOutput, output, opts.includeNamespace)
		if err != nil {
			return err
		}

		fmt.Println(out)

		return nil
	}

	return manifestCmd
}

// parseOutputTypes maps the user-facing --type strings to OutputResource
// values. An unknown value is an error (rather than silently ignored) so a typo
// fails loudly instead of producing a partial or empty manifest. The
// name-to-type mapping lives in the app2kube package (ParseOutputType) so it
// stays in sync with the generators.
func parseOutputTypes(types []string) ([]app2kube.OutputResource, error) {
	var outputTypes []app2kube.OutputResource
	for _, outType := range types {
		out, ok := app2kube.ParseOutputType(outType)
		if !ok {
			return nil, fmt.Errorf("unknown --type %q (valid types: %s)", outType, strings.Join(app2kube.ValidOutputTypes(), ", "))
		}
		outputTypes = append(outputTypes, out)
	}
	return outputTypes, nil
}

// buildManifest renders the manifest string for the given app and selected
// resource types. It is split out from manifest() so the rendering logic can be
// tested without capturing stdout.
func buildManifest(app *app2kube.App, types []string, outputFormat string, includeNamespace bool) (string, error) {
	if app.Namespace == app2kube.NamespaceDefault {
		app.Namespace = ""
	}

	outputTypes, err := parseOutputTypes(types)
	if err != nil {
		return "", err
	}

	out, err := app.GetManifest(outputFormat, outputTypes...)
	if err != nil {
		return "", err
	}

	if app.Namespace != "" && includeNamespace {
		namespace, err := app.GetManifest(outputFormat, app2kube.OutputNamespace)
		if err != nil {
			return "", err
		}
		out = namespace + out
	}

	return out, nil
}
