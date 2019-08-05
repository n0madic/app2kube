package app2kube

import (
	"bytes"
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes/scheme"
)

func getYAML(obj runtime.Object) string {
	printFlags := genericclioptions.NewPrintFlags("").WithTypeSetter(scheme.Scheme).WithDefaultOutput("yaml")

	printer, err := printFlags.ToPrinter()
	if err != nil {
		panic(err)
	}

	out := bytes.NewBuffer([]byte{})
	if err := printer.PrintObj(obj, out); err != nil {
		panic(err)
	}

	name := ""
	if acc, err := meta.Accessor(obj); err == nil {
		if n := acc.GetName(); len(n) > 0 {
			name = n
		}
	}

	return fmt.Sprintf("---\n# %s: %s\n%s\n",
		reflect.Indirect(reflect.ValueOf(obj)).Type().Name(),
		name,
		out,
	)
}
