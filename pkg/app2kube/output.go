package app2kube

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/printers"
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
	// OutputPodDisruptionBudget only
	OutputPodDisruptionBudget
	// OutputSecret only
	OutputSecret
	// OutputService only
	OutputService
)

// generator describes how to render one kind of resource and which requested
// OutputResource values select it. Adding a new resource type means appending
// a single entry here (and a user-facing name in outputTypeNames) — no edits to
// GetManifest.
type generator struct {
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
		selects: []OutputResource{OutputNamespace},
		render: func(app *App) ([]runtime.Object, error) {
			return []runtime.Object{app.GetNamespace()}, nil
		},
	},
	{
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
		// The PDB deploys with the Deployment (phase 1 of blue/green) so it
		// protects the pods as soon as they exist; render returns nothing for a
		// single-replica deploy (#47).
		selects: []OutputResource{OutputAll, OutputAllForDeployment, OutputPodDisruptionBudget},
		render: func(app *App) ([]runtime.Object, error) {
			pdb, err := app.GetPodDisruptionBudget()
			if err != nil {
				return nil, err
			}
			if pdb == nil {
				return nil, nil
			}
			return []runtime.Object{pdb}, nil
		},
	},
	{
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
		selects: []OutputResource{OutputAll, OutputAllOther, OutputSecret},
		render: func(app *App) ([]runtime.Object, error) {
			secrets, err := app.GetIngressSecrets()
			if err != nil {
				return nil, err
			}
			return toObjects(secrets), nil
		},
	},
	{
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

// EmittedKind pairs the two identifiers the destructive CLI operations need for
// one resource kind app2kube renders: the GVK string `apply --prune` expects and
// the plural kubectl resource name `delete` uses.
type EmittedKind struct {
	GVK      string
	Resource string
}

// emittedKinds is the single source of truth for the resource kinds app2kube
// renders (see manifestGenerators above), used by `apply --prune` and `delete
// all` so neither can drift from the generators. The Namespace is intentionally
// excluded — it is never pruned and is deleted only on explicit request. When a
// new generator is added to manifestGenerators, add its kind here too.
var emittedKinds = []EmittedKind{
	{"/v1/ConfigMap", "configmaps"},
	{"/v1/PersistentVolumeClaim", "persistentvolumeclaims"},
	{"/v1/Secret", "secrets"},
	{"/v1/Service", "services"},
	{"apps/v1/Deployment", "deployments"},
	{"batch/v1/CronJob", "cronjobs"},
	{"networking.k8s.io/v1/Ingress", "ingresses"},
	{"policy/v1/PodDisruptionBudget", "poddisruptionbudgets"},
}

// PruneWhitelist returns the "group/version/Kind" list for `kubectl apply
// --prune`: every kind app2kube can emit must be prunable, otherwise a resource
// that drops out of the manifest (e.g. a PDB when replicas scale back to 1) is
// orphaned.
func PruneWhitelist() []string {
	out := make([]string, 0, len(emittedKinds))
	for _, k := range emittedKinds {
		out = append(out, k.GVK)
	}
	return out
}

// DeleteResourceTypes returns the comma-separated kubectl resource list for
// `delete all`. kubectl's own "all" category omits the namespaced extras
// app2kube emits (configmaps, secrets, pvc, ingress, PDB), so every kind is
// named explicitly; pods/replicasets/jobs are cascade-deleted with their owners.
func DeleteResourceTypes() string {
	names := make([]string, 0, len(emittedKinds))
	for _, k := range emittedKinds {
		names = append(names, k.Resource)
	}
	return strings.Join(names, ",")
}

// GetManifest returns a manifest with the specified resource types.
func (app *App) GetManifest(outputFormat string, typeOutput ...OutputResource) (string, error) {
	// Build the printer once and reuse it for every object: it depends only on
	// the output format, so reconstructing it per object (as PrintObj does for
	// single-object callers) is wasted work on a multi-resource render.
	printer, err := objPrinter(outputFormat)
	if err != nil {
		return "", err
	}

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
				yml, err := printObj(obj, printer)
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
	"pdb":        OutputPodDisruptionBudget,
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

// objPrinter builds the resource printer for the given output format. It depends
// only on the format, so a multi-object render builds it once and reuses it.
func objPrinter(output string) (printers.ResourcePrinter, error) {
	return genericclioptions.NewPrintFlags("").WithTypeSetter(scheme.Scheme).WithDefaultOutput(output).ToPrinter()
}

// PrintObj returns the manifest for a single object. It builds a printer per
// call; batch callers should use GetManifest, which reuses one printer.
func PrintObj(obj runtime.Object, output string) (string, error) {
	if reflect.ValueOf(obj).IsNil() {
		return "", nil
	}

	printer, err := objPrinter(output)
	if err != nil {
		return "", err
	}

	return printObj(obj, printer)
}

// printObj renders a single object with a pre-built printer.
func printObj(obj runtime.Object, printer printers.ResourcePrinter) (string, error) {
	if reflect.ValueOf(obj).IsNil() {
		return "", nil
	}

	out := bytes.NewBuffer([]byte{})
	if err := printer.PrintObj(obj, out); err != nil {
		return "", err
	}

	filtered := stripCreationTimestamp(out.Bytes())

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

// stripCreationTimestamp removes the metadata `creationTimestamp: null` line
// that the serializer always emits, at any indentation. It matches the whole
// trimmed line rather than the bare substring: a substring match would also
// delete ConfigMap/Secret data values that merely contain the word (e.g. a
// stored Kubernetes manifest), silently corrupting them. It preserves the final
// line even when the input has no trailing newline — reading line-by-line with
// ReadBytes('\n') previously dropped that unterminated last line on io.EOF,
// silently losing data from any serialization not ending in a newline (#55).
func stripCreationTimestamp(in []byte) []byte {
	buf := bytes.NewBuffer(in)
	filtered := bytes.NewBuffer([]byte{})
	for {
		line, err := buf.ReadBytes('\n')
		// ReadBytes returns the data read so far together with io.EOF when the
		// stream ends without a delimiter, so the final unterminated line must be
		// processed before breaking.
		if len(line) > 0 {
			trimmed := bytes.TrimLeft(bytes.TrimRight(line, "\r\n"), " \t")
			if !bytes.Equal(trimmed, []byte("creationTimestamp: null")) {
				filtered.Write(line)
			}
		}
		if err != nil {
			break
		}
	}
	return filtered.Bytes()
}
