package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

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
}

func (o *appOptions) initApp(ctx context.Context) (*app2kube.App, error) {
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

	if o.verbose {
		fmt.Fprintf(os.Stderr, "---\n# merged values\n%s\n", rawVals)
	}

	// Namespace precedence: flag > file > default. An explicitly-set --namespace
	// wins even when empty (forcing the default), so it is distinguishable from an
	// absent flag (#59). Sync the resolved value back so downstream kubectl ops
	// use it.
	app.Namespace = resolveNamespace(rootCmd.PersistentFlags().Changed("namespace"), *kubeConfigFlags.Namespace, app.Namespace)
	*kubeConfigFlags.Namespace = app.Namespace

	// managed-by is seeded by the library (NewApp/ensureLabels); the CLI no
	// longer needs to set it explicitly.

	if blueGreenDeploy {
		app.Deployment.BlueGreenColor, err = getTargetBlueGreenColor(ctx, app.Namespace, app.Labels)
		if err != nil {
			return nil, err
		}
	}

	return app, nil
}

// resolveNamespace applies the namespace precedence flag > file > default. An
// explicitly-set --namespace wins even when empty (forcing the default), which
// is why the caller passes flagChanged separately from the value (#59).
func resolveNamespace(flagChanged bool, flagNs, fileNs string) string {
	ns := fileNs
	if flagChanged {
		ns = flagNs
	}
	if ns == "" {
		return app2kube.NamespaceDefault
	}
	return ns
}

func addAppFlags(cmd *cobra.Command) *appOptions {
	o := &appOptions{}
	cmd.Flags().BoolVarP(&o.includeNamespace, "include-namespace", "", false, "Include namespace manifest")
	cmd.Flags().StringArrayVar(&o.values, "set", []string{}, "Set values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	cmd.Flags().StringArrayVar(&o.fileValues, "set-file", []string{}, "Set values from respective files specified via the command line (can specify multiple or separate values with commas: key1=path1,key2=path2)")
	cmd.Flags().StringArrayVar(&o.stringValues, "set-string", []string{}, "Set STRING values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	cmd.Flags().VarP(&o.valueFiles, "values", "f", "Specify values in a YAML file (can specify multiple). Add the suffix '?' to the file name so that it can be skipped if it is not found")
	cmd.Flags().BoolVarP(&o.verbose, "verbose", "v", false, "Show the parsed YAML values as well")
	return o
}
