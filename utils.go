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

// PrintObj return manifest from object
func PrintObj(obj runtime.Object, output string) (string, error) {
	if reflect.ValueOf(obj).IsNil() {
		return "", nil
	}

	printFlags := genericclioptions.NewPrintFlags("").WithTypeSetter(scheme.Scheme).WithDefaultOutput(output)

	printer, err := printFlags.ToPrinter()
	if err != nil {
		return "", err
	}

	out := bytes.NewBuffer([]byte{})
	if err := printer.PrintObj(obj, out); err != nil {
		return "", err
	}

	name := ""
	if acc, err := meta.Accessor(obj); err == nil {
		if n := acc.GetName(); len(n) > 0 {
			name = n
		}
	} else {
		return "", err
	}

	return fmt.Sprintf("---\n# %s: %s\n%s\n",
		reflect.Indirect(reflect.ValueOf(obj)).Type().Name(),
		name,
		out,
	), nil
}
