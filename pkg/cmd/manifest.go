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

	var outputTypes []app2kube.OutputResource
	for _, outType := range typeOutput {
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

	manifest, err := app.GetManifest(output, outputTypes...)
	if err != nil {
		return err
	}

	if flagIncludeNamespace {
		namespace, err := app.GetManifest(output, app2kube.OutputNamespace)
		if err != nil {
			return err
		}
		manifest = namespace + manifest
	}

	fmt.Println(manifest)

	return nil
}
