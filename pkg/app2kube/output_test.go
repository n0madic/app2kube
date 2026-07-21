package app2kube

import (
	"slices"
	"strings"
	"testing"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

// The prune whitelist and the delete resource list derive from the same single
// source (emittedKinds), so they must stay in lockstep: equal length, both
// covering the PodDisruptionBudget, and free of kinds app2kube never emits (the
// old hand-written whitelist had stale ServiceAccount/DaemonSet entries).
func TestPruneAndDeleteListsDeriveFromRegistry(t *testing.T) {
	app := NewApp() // no letsencrypt → plain emittedKinds, no cert-manager
	prune := app.PruneWhitelist()
	del := strings.Split(app.DeleteResourceTypes(), ",")
	if len(prune) != len(del) {
		t.Fatalf("prune whitelist (%d) and delete list (%d) must have equal length", len(prune), len(del))
	}
	if !strings.Contains(app.DeleteResourceTypes(), "poddisruptionbudgets") {
		t.Errorf("delete list must include poddisruptionbudgets: %q", app.DeleteResourceTypes())
	}
	for _, gvk := range prune {
		if strings.HasSuffix(gvk, "/ServiceAccount") || strings.HasSuffix(gvk, "/DaemonSet") {
			t.Errorf("prune whitelist must not list a kind app2kube never emits: %q", gvk)
		}
	}
}

// The cert-manager Certificate CRD is only referenced by the prune/delete sets
// when the app actually uses letsencrypt; otherwise a cluster without
// cert-manager installed would be asked to prune/delete a missing CRD and fail.
func TestPruneAndDeleteCertManagerConditional(t *testing.T) {
	const certGVK = "cert-manager.io/v1/Certificate"
	const certRes = "certificates.cert-manager.io"

	plain := NewApp()
	if slices.Contains(plain.PruneWhitelist(), certGVK) {
		t.Errorf("prune whitelist must not list %q without letsencrypt: %v", certGVK, plain.PruneWhitelist())
	}
	if strings.Contains(plain.DeleteResourceTypes(), certRes) {
		t.Errorf("delete list must not list %q without letsencrypt: %q", certRes, plain.DeleteResourceTypes())
	}

	// Per-entry letsencrypt on a workload-bearing app: a Certificate is emitted,
	// so the CRD must be referenced. ingressTestApp carries containers + service.
	perEntry := ingressTestApp()
	perEntry.Ingress = []Ingress{{Host: "le.example.com", IngressCommon: IngressCommon{Letsencrypt: true}}}
	if !slices.Contains(perEntry.PruneWhitelist(), certGVK) {
		t.Errorf("prune whitelist must list %q with letsencrypt: %v", certGVK, perEntry.PruneWhitelist())
	}
	if !strings.Contains(perEntry.DeleteResourceTypes(), certRes) {
		t.Errorf("delete list must list %q with letsencrypt: %q", certRes, perEntry.DeleteResourceTypes())
	}

	// common.ingress.letsencrypt also enables it (workload + an ingress entry).
	common := ingressTestApp()
	common.Common.Ingress.Letsencrypt = true
	common.Ingress = []Ingress{{Host: "le.example.com"}}
	if !slices.Contains(common.PruneWhitelist(), certGVK) {
		t.Errorf("prune whitelist must list %q with common letsencrypt: %v", certGVK, common.PruneWhitelist())
	}

	// letsencrypt enabled but no workload (no containers/service) emits no
	// Certificate, so the CRD must NOT be referenced — the prune/delete guard
	// must mirror the GetCertificates emit guard, or a workload-less render on a
	// cert-manager-less cluster fails `apply --prune`/`delete all` wholesale.
	noWorkload := NewApp()
	noWorkload.Common.Ingress.Letsencrypt = true
	noWorkload.Ingress = []Ingress{{Host: "le.example.com", IngressCommon: IngressCommon{Letsencrypt: true}}}
	if certs, err := noWorkload.GetCertificates(); err != nil || len(certs) != 0 {
		t.Fatalf("precondition: no-workload app must emit 0 certificates, got %d (err=%v)", len(certs), err)
	}
	if slices.Contains(noWorkload.PruneWhitelist(), certGVK) {
		t.Errorf("prune whitelist must not list %q when no Certificate is emitted (no workload): %v", certGVK, noWorkload.PruneWhitelist())
	}
	if strings.Contains(noWorkload.DeleteResourceTypes(), certRes) {
		t.Errorf("delete list must not list %q when no Certificate is emitted (no workload): %q", certRes, noWorkload.DeleteResourceTypes())
	}

	// Regression: a global common.ingress.letsencrypt with NO ingress entry emits
	// no Certificate — GetCertificates only emits while iterating app.Ingress. The
	// guard used to return true on the global flag alone, so an internal service
	// inheriting letsencrypt from a shared base values file made `apply --prune`
	// and `delete all` reference the cert-manager CRD on a cluster that may not
	// have it installed, failing the whole operation.
	noIngress := ingressTestApp()
	noIngress.Common.Ingress.Letsencrypt = true
	if certs, err := noIngress.GetCertificates(); err != nil || len(certs) != 0 {
		t.Fatalf("precondition: app with no ingress must emit 0 certificates, got %d (err=%v)", len(certs), err)
	}
	if slices.Contains(noIngress.PruneWhitelist(), certGVK) {
		t.Errorf("prune whitelist must not list %q when no ingress entry exists: %v", certGVK, noIngress.PruneWhitelist())
	}
	if strings.Contains(noIngress.DeleteResourceTypes(), certRes) {
		t.Errorf("delete list must not list %q when no ingress entry exists: %q", certRes, noIngress.DeleteResourceTypes())
	}

	// Regression (the other direction): an app whose Service is implicit — derived
	// by ensureImplicitService from a single container's named port — DOES emit a
	// Certificate. The guard used to test len(app.Service) directly, but the
	// prune/delete callers run before GetManifest performs that derivation, so the
	// Certificate was applied yet excluded from the prune whitelist and from
	// `delete all`, orphaning it and its cert-manager-populated TLS Secret.
	implicitSvc := NewApp()
	implicitSvc.Name = "example"
	implicitSvc.Deployment.Containers = map[string]apiv1.Container{"app": {
		Image: "example/app:v1",
		Ports: []apiv1.ContainerPort{{Name: "http", ContainerPort: 8080}},
	}}
	implicitSvc.Ingress = []Ingress{{Host: "le.example.com", IngressCommon: IngressCommon{Letsencrypt: true}}}
	if !slices.Contains(implicitSvc.PruneWhitelist(), certGVK) {
		t.Errorf("prune whitelist must list %q for an implicit-Service app: %v", certGVK, implicitSvc.PruneWhitelist())
	}
	if !strings.Contains(implicitSvc.DeleteResourceTypes(), certRes) {
		t.Errorf("delete list must list %q for an implicit-Service app: %q", certRes, implicitSvc.DeleteResourceTypes())
	}
	// The guard must agree with what a real render emits.
	if err := implicitSvc.ensureImplicitService(); err != nil {
		t.Fatalf("ensureImplicitService: %v", err)
	}
	if certs, err := implicitSvc.GetCertificates(); err != nil || len(certs) != 1 {
		t.Fatalf("implicit-Service app must emit 1 certificate, got %d (err=%v)", len(certs), err)
	}
}

// The cert-manager Certificate is a locally-defined runtime.Object that is not
// registered in scheme.Scheme, so GetManifest can only render it through the
// printer's pre-set-TypeMeta path (the type-setter delegates on the
// not-registered error, and the YAML/JSON printer honors the Certificate's own
// TypeMeta). Every other Certificate test calls GetCertificates() directly, so a
// regression in that render path (an empty GVK, a DeepCopyObject/TypeMeta change)
// would ship silently and break every `manifest --type certificate` and every
// letsencrypt `apply`. Assert the Certificate actually renders with its kind and
// apiVersion via both OutputCertificate and OutputAll.
func TestGetManifestRendersCertificate(t *testing.T) {
	for _, out := range []OutputResource{OutputCertificate, OutputAll} {
		app := ingressTestApp()
		app.Common.Image.Repository = "example/app"
		app.Common.Image.Tag = "v1"
		app.Ingress = []Ingress{{Host: "le.example.com", IngressCommon: IngressCommon{Letsencrypt: true}}}

		manifest, err := app.GetManifest("yaml", out)
		if err != nil {
			t.Fatalf("GetManifest(%d): %v", out, err)
		}
		if !strings.Contains(manifest, "kind: Certificate") {
			t.Errorf("GetManifest(%d) missing `kind: Certificate`:\n%s", out, manifest)
		}
		if !strings.Contains(manifest, "apiVersion: cert-manager.io/v1") {
			t.Errorf("GetManifest(%d) missing `apiVersion: cert-manager.io/v1`:\n%s", out, manifest)
		}
	}
}

// fullTestApp builds an app that exercises every generator: a multi-replica
// Deployment (so the PDB is emitted), a Service, a ConfigMap, a Secret, a PVC, a
// CronJob and a letsencrypt Ingress (so both the TLS Secret and the cert-manager
// Certificate are emitted).
func fullTestApp() *App {
	app := NewApp()
	app.Name = "full"
	app.Common.Image.Repository = "example/app"
	app.Common.Image.Tag = "v1"
	app.Deployment.ReplicaCount = ptr.To(int32(2)) // >1 triggers the PDB
	app.Deployment.Containers = map[string]apiv1.Container{
		"app": {Ports: []apiv1.ContainerPort{{ContainerPort: 8080}}},
	}
	app.Service = map[string]Service{"web": {Port: 8080}}
	app.ConfigMap = map[string]string{"K": "V"}
	app.Secrets = map[string]string{"S": "secret"}
	app.Volumes = map[string]VolumeSpec{
		"data": {MountPath: "/data", Spec: apiv1.PersistentVolumeClaimSpec{
			AccessModes: []apiv1.PersistentVolumeAccessMode{apiv1.ReadWriteMany},
		}},
	}
	app.Cronjob = map[string]CronjobSpec{
		"tick": {Schedule: "* * * * *", Container: apiv1.Container{Command: []string{"true"}}},
	}
	app.Ingress = []Ingress{{Host: "full.example.com", IngressCommon: IngressCommon{Letsencrypt: true}}}
	return app
}

// manifestKinds returns the kinds of a rendered YAML manifest, in order. Only
// unindented `kind:` lines count: a nested one is a field of some spec, not an
// emitted object (a Certificate's `issuerRef.kind: ClusterIssuer`).
func manifestKinds(manifest string) []string {
	var kinds []string
	for _, line := range strings.Split(manifest, "\n") {
		if after, ok := strings.CutPrefix(line, "kind:"); ok {
			kinds = append(kinds, strings.TrimSpace(after))
		}
	}
	return kinds
}

// Regression: `delete all` used to name the resource types in creation order, so
// kubectl tore the app down bottom-up — the Secret a still-running Deployment
// mounts went first, and the Service backing a live Ingress went before the
// Ingress. The list must be in teardown order instead: cert-manager Certificate
// and Ingress first (traffic stops at the edge), then Service, then the
// workloads, then the data and config they depend on.
func TestDeleteResourceTypesIsInTeardownOrder(t *testing.T) {
	want := []string{
		"certificates.cert-manager.io",
		"ingresses",
		"services",
		"poddisruptionbudgets",
		"deployments",
		"cronjobs",
		"persistentvolumeclaims",
		"configmaps",
		"secrets",
	}
	got := strings.Split(fullTestApp().DeleteResourceTypes(), ",")
	if !slices.Equal(got, want) {
		t.Errorf("delete order:\n got %v\nwant %v", got, want)
	}
}

// Regression (the delete-by-manifest path, `delete` with no arguments): kubectl
// deletes the objects of a manifest in the order it reads them, so the teardown
// manifest must be the creation order reversed — the same traffic-first,
// data-last sequence `delete all` gets from emittedKinds. Relative constraints
// are asserted rather than the exact sequence so adding a generator does not
// break this test for the wrong reason.
func TestGetTeardownManifestIsReverseOfCreationOrder(t *testing.T) {
	app := fullTestApp()

	created, err := app.GetManifest("yaml", OutputAll)
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	torn, err := fullTestApp().GetTeardownManifest("yaml")
	if err != nil {
		t.Fatalf("GetTeardownManifest: %v", err)
	}

	createdKinds := manifestKinds(created)
	tornKinds := manifestKinds(torn)
	if len(createdKinds) != len(tornKinds) {
		t.Fatalf("teardown manifest must contain the same resources as the full manifest:\n created %v\nteardown %v", createdKinds, tornKinds)
	}

	// first/last occurrence: Secret is emitted twice (app Secret, ingress TLS).
	pos := func(kinds []string, kind string, last bool) int {
		idx := -1
		for i, k := range kinds {
			if k == kind && (idx < 0 || last) {
				idx = i
			}
		}
		if idx < 0 {
			t.Fatalf("kind %q missing from manifest %v", kind, kinds)
		}
		return idx
	}
	before := func(earlier, later string) {
		t.Helper()
		if e, l := pos(tornKinds, earlier, true), pos(tornKinds, later, false); e >= l {
			t.Errorf("%s (index %d) must be deleted before %s (index %d): %v", earlier, e, later, l, tornKinds)
		}
	}
	before("Certificate", "Ingress")
	before("Ingress", "Service")
	before("Service", "Deployment")
	before("PodDisruptionBudget", "Deployment")
	before("Deployment", "PersistentVolumeClaim")
	before("CronJob", "ConfigMap")

	// The workload's own Secret/ConfigMap must outlive it: they are the last
	// objects in the manifest, after every workload.
	if d, s := pos(tornKinds, "Deployment", true), pos(tornKinds, "Secret", true); d >= s {
		t.Errorf("Deployment (index %d) must be deleted before its Secret (index %d): %v", d, s, tornKinds)
	}

	// The Namespace is never part of a teardown manifest (it is deleted only on
	// explicit --include-namespace).
	if slices.Contains(tornKinds, "Namespace") {
		t.Errorf("teardown manifest must not contain the Namespace: %v", tornKinds)
	}
}

// Every kind app2kube actually renders must be registered in emittedKinds,
// otherwise `apply --prune`/`delete all` would orphan it. Rendering a fully
// populated app and checking each emitted kind against the registry catches a
// new generator that forgot to register its kind.
func TestEmittedKindsCoverAllGeneratedResources(t *testing.T) {
	app := fullTestApp()

	manifest, err := app.GetManifest("yaml", OutputAll)
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}

	registered := map[string]bool{}
	for _, k := range emittedKinds {
		parts := strings.Split(k.GVK, "/")
		registered[parts[len(parts)-1]] = true
	}

	seen := map[string]bool{}
	for _, kind := range manifestKinds(manifest) {
		seen[kind] = true
		if !registered[kind] {
			t.Errorf("manifest emits kind %q not registered in emittedKinds (prune/delete would orphan it)", kind)
		}
	}
	// Sanity: the full app must have actually exercised the PDB and the
	// letsencrypt Certificate paths.
	for _, kind := range []string{"PodDisruptionBudget", "Certificate"} {
		if !seen[kind] {
			t.Errorf("expected a %s in the full manifest, kinds seen: %v", kind, seen)
		}
	}
}

// #55: the creationTimestamp-stripping filter must preserve the final line even
// when the input does not end in a newline — otherwise the last line of a
// serialization without a trailing newline is silently dropped (data loss in
// the manifest fed to kubectl).
func TestStripCreationTimestamp(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"no trailing newline": {
			in:   "a: 1\ncreationTimestamp: null\nb: 2",
			want: "a: 1\nb: 2",
		},
		"trailing newline preserved": {
			in:   "a: 1\ncreationTimestamp: null\nb: 2\n",
			want: "a: 1\nb: 2\n",
		},
		"timestamp on last unterminated line is dropped": {
			in:   "a: 1\ncreationTimestamp: null",
			want: "a: 1\n",
		},
		"empty input": {
			in:   "",
			want: "",
		},
		// #8: only the whole `creationTimestamp: null` metadata line is dropped,
		// at any indentation — not arbitrary data values that merely contain the
		// word, which a substring match would corrupt (e.g. a stored manifest).
		"indented metadata line stripped": {
			in:   "metadata:\n  creationTimestamp: null\n  name: cfg\n",
			want: "metadata:\n  name: cfg\n",
		},
		"data value containing the word kept": {
			in:   "data:\n  note: creationTimestamp is a field\n  ts: \"2020 creationTimestamp\"\n",
			want: "data:\n  note: creationTimestamp is a field\n  ts: \"2020 creationTimestamp\"\n",
		},
		// #8: a `creationTimestamp: null` line inside a block-scalar data value
		// (e.g. a Kubernetes manifest stored in a ConfigMap) must be preserved,
		// while the object's own metadata line is still stripped.
		"block scalar data value timestamp kept; metadata stripped": {
			in:   "data:\n  pod.yaml: |\n    metadata:\n      creationTimestamp: null\n    spec: {}\nmetadata:\n  creationTimestamp: null\n  name: cfg\n",
			want: "data:\n  pod.yaml: |\n    metadata:\n      creationTimestamp: null\n    spec: {}\nmetadata:\n  name: cfg\n",
		},
		// Structural stripping still applies at any depth (e.g. a Deployment's
		// pod template metadata), since that is a real key, not block content.
		"deeply nested metadata line stripped": {
			in:   "spec:\n  template:\n    metadata:\n      creationTimestamp: null\n      labels:\n        app: x\n",
			want: "spec:\n  template:\n    metadata:\n      labels:\n        app: x\n",
		},
		// A block scalar that is itself a sequence element ("- |-", e.g. a
		// multiline container command/arg) must have its content protected too:
		// a `creationTimestamp: null` line inside it is data, not metadata; only
		// the object's own metadata line is stripped.
		"sequence block scalar timestamp kept; metadata stripped": {
			in:   "command:\n- |-\n  do stuff\n  creationTimestamp: null\nmetadata:\n  creationTimestamp: null\n  name: cfg\n",
			want: "command:\n- |-\n  do stuff\n  creationTimestamp: null\nmetadata:\n  name: cfg\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := string(stripCreationTimestamp([]byte(tc.in))); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
