package cmd

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"
)

const defaultFile = ".app2kube.yml"

var (
	err                  error
	fileValues           []string
	flagVerbose          bool
	flagIncludeNamespace bool
	output               string
	rawVals              []byte
	snapshot             string
	stringValues         []string
	valueFiles           app2kube.ValueFiles
	values               []string
)

func initApp() (*app2kube.App, error) {
	if _, err := os.Stat(defaultFile); !os.IsNotExist(err) {
		valueFiles.Set(defaultFile)
	}

	if len(valueFiles)+len(values)+len(stringValues)+len(fileValues) == 0 {
		return nil, errors.New("Values are required")
	}

	app := app2kube.NewApp()

	rawVals, err = app.LoadValues(valueFiles, values, stringValues, fileValues)
	if err != nil {
		return nil, err
	}

	if flagVerbose {
		fmt.Fprintf(os.Stderr, "---\n# merged values\n%s\n", rawVals)
	}

	if *kubeConfigFlags.Namespace == "" && app.Namespace != "" {
		*kubeConfigFlags.Namespace = app.Namespace
	} else if *kubeConfigFlags.Namespace != "" {
		app.Namespace = *kubeConfigFlags.Namespace
	}

	if app.Namespace == "" {
		app.Namespace = app2kube.NamespaceDefault
	}

	app.Labels["app.kubernetes.io/managed-by"] = "app2kube"

	if blueGreenDeploy {
		app.Deployment.BlueGreenColor, err = getTargetBlueGreenColor(app.Namespace, app.Labels)
		if err != nil {
			return nil, err
		}
	}

	if snapshot != "" {
		header := fmt.Sprintf("# Snapshot of values saved by app2kube %s in %s\n---\n",
			rootCmd.Version,
			time.Now().Format("2006-01-02 15:04:05 MST"))
		err := ioutil.WriteFile(snapshot, []byte(header+string(rawVals)), 0660)
		if err != nil {
			return nil, err
		}
		fmt.Fprintln(os.Stderr, "Snapshot of values saved in", snapshot)
	}

	return app, nil
}

func addAppFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVarP(&flagIncludeNamespace, "include-namespace", "", false, "Include namespace manifest")
	cmd.Flags().StringArrayVar(&values, "set", []string{}, "Set values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	cmd.Flags().StringArrayVar(&fileValues, "set-file", []string{}, "Set values from respective files specified via the command line (can specify multiple or separate values with commas: key1=path1,key2=path2)")
	cmd.Flags().StringArrayVar(&stringValues, "set-string", []string{}, "Set STRING values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	cmd.Flags().StringVarP(&snapshot, "snapshot", "", "", "Save the parsed YAML values in the specified file for reuse")
	cmd.Flags().VarP(&valueFiles, "values", "f", "Specify values in a YAML file or a URL (can specify multiple). Add the suffix '?' to the file name so that it can be skipped if it is not found")
	cmd.Flags().BoolVarP(&flagVerbose, "verbose", "v", false, "Show the parsed YAML values as well")
}
