package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/n0madic/app2kube"
	"github.com/spf13/cobra"
)

var (
	app            *app2kube.App
	defaultIngress string
	err            error
	fileValues     []string
	flagVerbose    bool
	namespace      string
	output         string
	rawVals        []byte
	snapshot       string
	stringValues   []string
	valueFiles     app2kube.ValueFiles
	values         []string
)

var version = "DEV"

var rootCmd = &cobra.Command{
	Use:                "app2kube",
	Short:              fmt.Sprintf("Kubernetes application deployment (app2kube %s)", version),
	PersistentPreRunE:  preRun,
	PersistentPostRunE: postRun,
	Version:            version,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&defaultIngress, "ingress", "i", "nginx", "Ingress class")
	rootCmd.PersistentFlags().StringVarP(&namespace, "namespace", "n", "", "Namespace used for manifests")
	rootCmd.PersistentFlags().StringArrayVar(&values, "set", []string{}, "Set values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	rootCmd.PersistentFlags().StringArrayVar(&fileValues, "set-file", []string{}, "Set values from respective files specified via the command line (can specify multiple or separate values with commas: key1=path1,key2=path2)")
	rootCmd.PersistentFlags().StringArrayVar(&stringValues, "set-string", []string{}, "Set STRING values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	rootCmd.PersistentFlags().StringVarP(&snapshot, "snapshot", "s", "", "Save the parsed YAML values in the specified file for reuse")
	rootCmd.PersistentFlags().VarP(&valueFiles, "values", "f", "Specify values in a YAML file or a URL (can specify multiple)")
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "Show the parsed YAML values as well")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func preRun(cmd *cobra.Command, args []string) error {
	if cmd.Use != "help [command]" && cmd.Use != "encrypt" {
		if len(valueFiles)+len(values)+len(stringValues)+len(fileValues) == 0 {
			return errors.New("Values are required")
		}

		app = app2kube.NewApp()

		rawVals, err = app.LoadValues(valueFiles, values, stringValues, fileValues)
		if err != nil {
			return err
		}

		if flagVerbose {
			fmt.Fprintf(os.Stderr, "---\n# merged values\n%s\n", rawVals)
		}

		if namespace != "" {
			app.Namespace = namespace
		}

		app.Labels["app.kubernetes.io/managed-by"] = "app2kube"
	}
	return nil
}

func postRun(cmd *cobra.Command, args []string) error {
	if snapshot != "" {
		header := fmt.Sprintf("# Snapshot of values saved by app2kube %s in %s\n---\n",
			version,
			time.Now().Format("2006-01-02 15:04:05 MST"))
		err := ioutil.WriteFile(snapshot, []byte(header+string(rawVals)), 0660)
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Snapshot of values saved in", snapshot)
	}

	return nil
}
