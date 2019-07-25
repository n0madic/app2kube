package app2kube

import (
	"bytes"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes/scheme"
)

func getYAML(name string, obj runtime.Object) string {
	printFlags := genericclioptions.NewPrintFlags("").WithTypeSetter(scheme.Scheme).WithDefaultOutput("yaml")

	printer, err := printFlags.ToPrinter()
	if err != nil {
		panic(err)
	}

	out := bytes.NewBuffer([]byte{})
	if err := printer.PrintObj(obj, out); err != nil {
		panic(err)
	}

	return fmt.Sprintf("---\n# %s\n%s\n", name, out)
}
