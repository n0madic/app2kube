package app2kube

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/kubectl/pkg/scheme"
)

// OutputResource type
type OutputResource int

const (
	// OutputAll for all resources (except namespace)
	OutputAll OutputResource = iota
	// OutputAllForDeployment is all the resources needed to run Deployment
	OutputAllForDeployment
	// OutputAllOther is all other resources not needed to run Deployment
	OutputAllOther
	// OutputConfigMap only
	OutputConfigMap
	// OutputCronJob only
	OutputCronJob
	// OutputDeployment only
	OutputDeployment
	// OutputIngress only
	OutputIngress
	// OutputNamespace only
	OutputNamespace
	// OutputPersistentVolumeClaim only
	OutputPersistentVolumeClaim
	// OutputSecret only
	OutputSecret
	// OutputService only
	OutputService
)

// generator describes how to render one kind of resource and which requested
// OutputResource values select it. Adding a new resource type means appending
// a single entry here (and a name in outputTypeNames) — no edits to GetManifest.
type generator struct {
	name    string
	selects []OutputResource
	render  func(app *App) ([]runtime.Object, error)
}

func (g generator) matches(out OutputResource) bool {
	for _, s := range g.selects {
		if s == out {
			return true
		}
	}
	return false
}

// toObjects converts a slice of concrete resource pointers to runtime.Object.
func toObjects[T runtime.Object](items []T) []runtime.Object {
	objs := make([]runtime.Object, 0, len(items))
	for _, it := range items {
		objs = append(objs, it)
	}
	return objs
}

// manifestGenerators is the ordered registry of resource generators. The order
// defines the order of resources in the rendered manifest and matches the
// previous hand-written sequence in GetManifest.
var manifestGenerators = []generator{
	{
		name:    "namespace",
		selects: []OutputResource{OutputNamespace},
		render: func(app *App) ([]runtime.Object, error) {
			return []runtime.Object{app.GetNamespace()}, nil
		},
	},
	{
		name:    "secret",
		selects: []OutputResource{OutputAll, OutputAllForDeployment, OutputSecret},
		render: func(app *App) ([]runtime.Object, error) {
			secret, err := app.GetSecret()
			if err != nil {
				return nil, err
			}
			return []runtime.Object{secret}, nil
		},
	},
	{
		name:    "configmap",
		selects: []OutputResource{OutputAll, OutputAllForDeployment, OutputConfigMap},
		render: func(app *App) ([]runtime.Object, error) {
			configmap, err := app.GetConfigMap()
			if err != nil {
				return nil, err
			}
			return []runtime.Object{configmap}, nil
		},
	},
	{
		name:    "pvc",
		selects: []OutputResource{OutputAll, OutputAllForDeployment, OutputPersistentVolumeClaim},
		render: func(app *App) ([]runtime.Object, error) {
			claims, err := app.GetPersistentVolumeClaims()
			if err != nil {
				return nil, err
			}
			return toObjects(claims), nil
		},
	},
	{
		name:    "cronjob",
		selects: []OutputResource{OutputAll, OutputAllOther, OutputCronJob},
		render: func(app *App) ([]runtime.Object, error) {
			jobs, err := app.GetCronJobs()
			if err != nil {
				return nil, err
			}
			return toObjects(jobs), nil
		},
	},
	{
		name:    "deployment",
		selects: []OutputResource{OutputAll, OutputAllForDeployment, OutputDeployment},
		render: func(app *App) ([]runtime.Object, error) {
			deployment, err := app.GetDeployment()
			if err != nil {
				return nil, err
			}
			return []runtime.Object{deployment}, nil
		},
	},
	{
		name:    "service",
		selects: []OutputResource{OutputAll, OutputAllOther, OutputService},
		render: func(app *App) ([]runtime.Object, error) {
			services, err := app.GetServices()
			if err != nil {
				return nil, err
			}
			return toObjects(services), nil
		},
	},
	{
		// TLS secrets for ingress are emitted together with other resources and
		// with --type secret, matching the original behavior.
		name:    "ingressSecrets",
		selects: []OutputResource{OutputAll, OutputAllOther, OutputSecret},
		render: func(app *App) ([]runtime.Object, error) {
			return toObjects(app.GetIngressSecrets()), nil
		},
	},
	{
		name:    "ingress",
		selects: []OutputResource{OutputAll, OutputAllOther, OutputIngress},
		render: func(app *App) ([]runtime.Object, error) {
			ingress, err := app.GetIngress()
			if err != nil {
				return nil, err
			}
			return toObjects(ingress), nil
		},
	},
}

// GetManifest returns a manifest with the specified resource types.
func (app *App) GetManifest(outputFormat string, typeOutput ...OutputResource) (string, error) {
	var manifest string
	for _, out := range typeOutput {
		for _, g := range manifestGenerators {
			if !g.matches(out) {
				continue
			}
			objs, err := g.render(app)
			if err != nil {
				return "", err
			}
			for _, obj := range objs {
				yml, err := PrintObj(obj, outputFormat)
				if err != nil {
					return "", err
				}
				manifest += yml
			}
		}
	}
	return manifest, nil
}

// outputTypeNames maps the user-facing --type strings to OutputResource values.
// This is the single source of truth for resource type names.
var outputTypeNames = map[string]OutputResource{
	"all":        OutputAll,
	"configmap":  OutputConfigMap,
	"cronjob":    OutputCronJob,
	"deployment": OutputDeployment,
	"ingress":    OutputIngress,
	"pvc":        OutputPersistentVolumeClaim,
	"secret":     OutputSecret,
	"service":    OutputService,
}

// ParseOutputType maps a user-facing --type name to an OutputResource. The
// second return value is false for unknown names.
func ParseOutputType(name string) (OutputResource, bool) {
	out, ok := outputTypeNames[strings.ToLower(name)]
	return out, ok
}

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

	// remove 'creationTimestamp: null' from manifest
	filtered := bytes.NewBuffer([]byte{})
	for {
		line, err := out.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return "", err
			}
		}
		if !bytes.Contains(line, []byte("creationTimestamp")) {
			filtered.Write(line)
		}
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
		filtered,
	), nil
}
