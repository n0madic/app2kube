package cmd

import (
	"fmt"
	"strings"

	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"
)

var typeOutput []string

// NewCmdManifest return manifest command
func NewCmdManifest() *cobra.Command {
	manifestCmd := &cobra.Command{
		Use:   "manifest",
		Short: "Generate kubernetes manifests for an application",
		RunE:  manifest,
	}

	manifestCmd.Flags().StringVarP(&output, "output", "o", "yaml", "Output format")
	manifestCmd.Flags().StringArrayVar(&typeOutput, "type", []string{"all"}, "Types of output resources (several can be specified)")
	addAppFlags(manifestCmd)
	addBlueGreenFlag(manifestCmd)

	return manifestCmd
}

func manifest(cmd *cobra.Command, args []string) error {
	app, err := initApp()
	if err != nil {
		return err
	}

	out, err := buildManifest(app, typeOutput, output, flagIncludeNamespace)
	if err != nil {
		return err
	}

	fmt.Println(out)

	return nil
}

// parseOutputTypes maps the user-facing --type strings to OutputResource values.
// Unknown values are ignored, matching the original switch behavior.
func parseOutputTypes(types []string) []app2kube.OutputResource {
	var outputTypes []app2kube.OutputResource
	for _, outType := range types {
		switch strings.ToLower(outType) {
		case "all":
			outputTypes = append(outputTypes, app2kube.OutputAll)
		case "configmap":
			outputTypes = append(outputTypes, app2kube.OutputConfigMap)
		case "cronjob":
			outputTypes = append(outputTypes, app2kube.OutputCronJob)
		case "deployment":
			outputTypes = append(outputTypes, app2kube.OutputDeployment)
		case "ingress":
			outputTypes = append(outputTypes, app2kube.OutputIngress)
		case "pvc":
			outputTypes = append(outputTypes, app2kube.OutputPersistentVolumeClaim)
		case "secret":
			outputTypes = append(outputTypes, app2kube.OutputSecret)
		case "service":
			outputTypes = append(outputTypes, app2kube.OutputService)
		}
	}
	return outputTypes
}

// buildManifest renders the manifest string for the given app and selected
// resource types. It is split out from manifest() so the rendering logic can be
// tested without capturing stdout.
func buildManifest(app *app2kube.App, types []string, outputFormat string, includeNamespace bool) (string, error) {
	if app.Namespace == app2kube.NamespaceDefault {
		app.Namespace = ""
	}

	out, err := app.GetManifest(outputFormat, parseOutputTypes(types)...)
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
