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
	defaultIngress string
	setVals        []string
	setStringVals  []string
	fileValues     []string
	valsFiles      app2kube.ValueFiles
	flagVerbose    bool
	snapshot       string
	namespace      string
)

var version = "DEV"

func main() {
	cmd := &cobra.Command{
		Use:   "app2kube [flags]",
		Short: fmt.Sprintf("Generate kubernetes manifests for an application (app2kube %s)", version),
		RunE:  run,
	}

	f := cmd.Flags()
	f.StringArrayVar(&setVals, "set", []string{}, "Set values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	f.StringArrayVar(&setStringVals, "set-string", []string{}, "Set STRING values on the command line (can specify multiple or separate values with commas: key1=val1,key2=val2)")
	f.StringArrayVar(&fileValues, "set-file", []string{}, "Set values from respective files specified via the command line (can specify multiple or separate values with commas: key1=path1,key2=path2)")
	f.VarP(&valsFiles, "values", "f", "Specify values in a YAML file or a URL (can specify multiple)")
	f.BoolVarP(&flagVerbose, "verbose", "v", false, "Show the parsed YAML values as well")
	f.StringVarP(&snapshot, "snapshot", "s", "", "Save the parsed YAML values in the specified file for reuse")
	f.StringVarP(&defaultIngress, "ingress", "i", "nginx", "Ingress class")
	f.StringVarP(&namespace, "namespace", "n", "", "Namespace used for manifests")

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	if len(valsFiles)+len(setVals)+len(setStringVals)+len(fileValues) == 0 {
		return errors.New("Values are required")
	}

	app := app2kube.NewApp()

	rawVals, err := app.LoadValues(valsFiles, setVals, setStringVals, fileValues)
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

	fmt.Print(app.GetPersistentVolumeClaims())
	fmt.Print(app.GetSecret())
	fmt.Print(app.GetCronJobs())
	fmt.Print(app.GetDeployment())
	fmt.Print(app.GetServices())
	fmt.Print(app.GetIngress(defaultIngress))

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
