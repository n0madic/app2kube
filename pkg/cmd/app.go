package cmd

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"
)

const defaultFile = ".app2kube.yml"

// appOptions holds the per-command inputs used to load an application's values.
// Each command owns its own instance (created by addAppFlags), so commands no
// longer share mutable package-level state.
type appOptions struct {
	valueFiles       app2kube.ValueFiles
	values           []string
	stringValues     []string
	fileValues       []string
	verbose          bool
	includeNamespace bool
	snapshot         string
	rawVals          []byte // merged values, populated by initApp after a successful load
}

func (o *appOptions) initApp() (*app2kube.App, error) {
	if flagAllApplications {
		return &app2kube.App{
			Name:      "all",
			Namespace: *kubeConfigFlags.Namespace,
		}, nil
	}

	// The default file is always used as a base when present in the current
	// directory; values from -f/--set extend and override it, so it must come
	// first. A non-NotExist Stat error (e.g. permission denied) must not be
	// treated as "present". A local copy avoids mutating the bound flag value.
	valueFiles := o.valueFiles
	if _, err := os.Stat(defaultFile); err == nil {
		valueFiles = append(app2kube.ValueFiles{defaultFile}, valueFiles...)
	}

	if len(valueFiles)+len(o.values)+len(o.stringValues)+len(o.fileValues) == 0 {
		return nil, errors.New("values are required")
	}

	app := app2kube.NewApp()

	rawVals, err := app.LoadValues(valueFiles, o.values, o.stringValues, o.fileValues)
	if err != nil {
		return nil, err
	}
	o.rawVals = rawVals

	if o.verbose {
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

	app.Labels[app2kube.LabelManagedBy] = app2kube.ManagedByValue

	if blueGreenDeploy {
		app.Deployment.BlueGreenColor, err = getTargetBlueGreenColor(app.Namespace, app.Labels)
		if err != nil {
			return nil, err
		}
	}

	if o.snapshot != "" {
		header := fmt.Sprintf("# Snapshot of values saved by app2kube %s in %s\n---\n",
			rootCmd.Version,
			time.Now().Format("2006-01-02 15:04:05 MST"))
		// Snapshots embed merged plaintext values (env/configmap/--set data), so
		// write owner-only to avoid exposing them on shared CI runners.
		if err := os.WriteFile(o.snapshot, []byte(header+string(rawVals)), 0600); err != nil {
			return nil, err
		}
		fmt.Fprintln(os.Stderr, "Snapshot of values saved in", o.snapshot)
	}

	return app, nil
}

func addAppFlags(cmd *cobra.Command) *appOptions {
	o := &appOptions{}
	cmd.Flags().BoolVarP(&o.includeNamespace, "include-namespace", "", false, "Include namespace manifest")
	cmd.Flags().StringArrayVar(&o.values, "set", []string{}, "Set values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	cmd.Flags().StringArrayVar(&o.fileValues, "set-file", []string{}, "Set values from respective files specified via the command line (can specify multiple or separate values with commas: key1=path1,key2=path2)")
	cmd.Flags().StringArrayVar(&o.stringValues, "set-string", []string{}, "Set STRING values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	cmd.Flags().StringVarP(&o.snapshot, "snapshot", "", "", "Save the parsed YAML values in the specified file for reuse")
	cmd.Flags().VarP(&o.valueFiles, "values", "f", "Specify values in a YAML file (can specify multiple). Add the suffix '?' to the file name so that it can be skipped if it is not found")
	cmd.Flags().BoolVarP(&o.verbose, "verbose", "v", false, "Show the parsed YAML values as well")
	return o
}
