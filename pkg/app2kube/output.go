package app2kube

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
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
	// OutputCertificate only (cert-manager Certificate for letsencrypt ingresses)
	OutputCertificate
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
	{
		// cert-manager Certificate objects for letsencrypt ingresses. Rendered
		// from a local typed struct (certificate.go) so cert-manager is not a
		// build dependency; on clusters without the CRD the user simply does not
		// enable letsencrypt and none are emitted.
		selects: []OutputResource{OutputAll, OutputAllOther, OutputCertificate},
		render: func(app *App) ([]runtime.Object, error) {
			certs, err := app.GetCertificates()
			if err != nil {
				return nil, err
			}
			return toObjects(certs), nil
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

// certManagerEmittedKind is the cert-manager Certificate kind, kept out of the
// unconditional emittedKinds: it is only added to the prune/delete sets when the
// app actually uses letsencrypt (usesCertManager). Listing it unconditionally
// would make `apply --prune`/`delete all` reference certificates.cert-manager.io
// on clusters that have no cert-manager CRD installed, which fails the whole
// operation. The fully qualified resource name avoids colliding with unrelated
// `certificates` CRDs from other operators.
var certManagerEmittedKind = EmittedKind{"cert-manager.io/v1/Certificate", "certificates.cert-manager.io"}

// usesCertManager reports whether the app emits any cert-manager Certificate,
// i.e. letsencrypt is enabled globally or on at least one ingress entry.
func (app *App) usesCertManager() bool {
	if app.Common.Ingress.Letsencrypt {
		return true
	}
	for _, ing := range app.Ingress {
		if ing.Letsencrypt {
			return true
		}
	}
	return false
}

// pruneAndDeleteKinds returns the resource kinds app2kube can emit for this
// specific app, conditionally including the cert-manager Certificate so the
// prune/delete tooling only references its CRD when letsencrypt is actually in
// use.
func (app *App) pruneAndDeleteKinds() []EmittedKind {
	if !app.usesCertManager() {
		return emittedKinds
	}
	kinds := make([]EmittedKind, 0, len(emittedKinds)+1)
	kinds = append(kinds, emittedKinds...)
	kinds = append(kinds, certManagerEmittedKind)
	return kinds
}

// PruneWhitelist returns the "group/version/Kind" list for `kubectl apply
// --prune`: every kind app2kube can emit must be prunable, otherwise a resource
// that drops out of the manifest (e.g. a PDB when replicas scale back to 1) is
// orphaned. The cert-manager Certificate is included only when this app uses
// letsencrypt, so a cert-manager-less cluster is not asked to prune a missing
// CRD.
func (app *App) PruneWhitelist() []string {
	kinds := app.pruneAndDeleteKinds()
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, k.GVK)
	}
	return out
}

// DeleteResourceTypes returns the comma-separated kubectl resource list for
// `delete all`. kubectl's own "all" category omits the namespaced extras
// app2kube emits (configmaps, secrets, pvc, ingress, PDB), so every kind is
// named explicitly; pods/replicasets/jobs are cascade-deleted with their owners.
// The cert-manager Certificate is included only when this app uses letsencrypt.
func (app *App) DeleteResourceTypes() string {
	kinds := app.pruneAndDeleteKinds()
	names := make([]string, 0, len(kinds))
	for _, k := range kinds {
		names = append(names, k.Resource)
	}
	return strings.Join(names, ",")
}

// GetManifest returns a manifest with the specified resource types.
func (app *App) GetManifest(outputFormat string, typeOutput ...OutputResource) (string, error) {
	// Derive the implicit Service (Ingress with no explicit Service, single app
	// container) once, deterministically, before rendering. This makes a
	// Service-/Ingress-only render — and the blue/green phase that emits traffic
	// resources without re-rendering the Deployment — derive it the same way a
	// full render does, instead of relying on the Deployment generator's side
	// effect happening earlier in the same call.
	if err := app.ensureImplicitService(); err != nil {
		return "", err
	}

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
	"all":         OutputAll,
	"certificate": OutputCertificate,
	"configmap":   OutputConfigMap,
	"cronjob":     OutputCronJob,
	"deployment":  OutputDeployment,
	"ingress":     OutputIngress,
	"pdb":         OutputPodDisruptionBudget,
	"pvc":         OutputPersistentVolumeClaim,
	"secret":      OutputSecret,
	"service":     OutputService,
}

// ParseOutputType maps a user-facing --type name to an OutputResource. The
// second return value is false for unknown names.
func ParseOutputType(name string) (OutputResource, bool) {
	out, ok := outputTypeNames[strings.ToLower(name)]
	return out, ok
}

// ValidOutputTypes returns the accepted --type names in sorted order. It backs
// the "unknown --type" error message so the list of valid names cannot drift
// from outputTypeNames.
func ValidOutputTypes() []string {
	names := make([]string, 0, len(outputTypeNames))
	for name := range outputTypeNames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
// stored Kubernetes manifest), silently corrupting them.
//
// It is also block-scalar aware: a `creationTimestamp: null` line *inside* a
// data value rendered as a YAML block scalar (`key: |` / `key: >`, e.g. a
// Kubernetes manifest stored in a ConfigMap) trims to the exact match too, so a
// context-blind filter would corrupt it. Content lines of a block scalar (blank
// lines or lines indented deeper than the key) are passed through verbatim;
// only a real structural metadata key is stripped.
//
// It preserves the final line even when the input has no trailing newline —
// reading line-by-line with ReadBytes('\n') previously dropped that unterminated
// last line on io.EOF, silently losing data from any serialization not ending in
// a newline (#55).
func stripCreationTimestamp(in []byte) []byte {
	buf := bytes.NewBuffer(in)
	filtered := bytes.NewBuffer([]byte{})
	inBlockScalar := false
	blockIndent := 0
	for {
		line, err := buf.ReadBytes('\n')
		// ReadBytes returns the data read so far together with io.EOF when the
		// stream ends without a delimiter, so the final unterminated line must be
		// processed before breaking.
		if len(line) > 0 {
			body := bytes.TrimRight(line, "\r\n")
			trimmed := bytes.TrimLeft(body, " \t")
			indent := len(body) - len(trimmed)

			if inBlockScalar {
				// A blank line, or a line indented deeper than the block key, is
				// block-scalar content: pass it through untouched. A line at or
				// below the key indent ends the block and is reprocessed normally.
				if len(trimmed) == 0 || indent > blockIndent {
					filtered.Write(line)
					if err != nil {
						break
					}
					continue
				}
				inBlockScalar = false
			}

			if !bytes.Equal(trimmed, []byte("creationTimestamp: null")) {
				filtered.Write(line)
			}

			// Enter block-scalar mode when this mapping value opens a literal/
			// folded scalar so its content is protected on subsequent lines.
			if isBlockScalarHeader(trimmed) {
				inBlockScalar = true
				blockIndent = indent
			}
		}
		if err != nil {
			break
		}
	}
	return filtered.Bytes()
}

// isBlockScalarHeader reports whether a (left-trimmed) mapping line opens a YAML
// block scalar, i.e. its value after "key:" begins with a '|' or '>' indicator
// (optionally followed by chomping/indentation indicators or a comment). A
// quoted value that merely starts with those characters is not a block scalar.
func isBlockScalarHeader(trimmed []byte) bool {
	idx := bytes.LastIndex(trimmed, []byte(": "))
	if idx < 0 {
		return false
	}
	val := bytes.TrimLeft(trimmed[idx+2:], " ")
	return len(val) > 0 && (val[0] == '|' || val[0] == '>')
}
