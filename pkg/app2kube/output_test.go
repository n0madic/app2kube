package app2kube

import (
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
	prune := PruneWhitelist()
	del := strings.Split(DeleteResourceTypes(), ",")
	if len(prune) != len(del) {
		t.Fatalf("prune whitelist (%d) and delete list (%d) must have equal length", len(prune), len(del))
	}
	if !strings.Contains(DeleteResourceTypes(), "poddisruptionbudgets") {
		t.Errorf("delete list must include poddisruptionbudgets: %q", DeleteResourceTypes())
	}
	for _, gvk := range prune {
		if strings.HasSuffix(gvk, "/ServiceAccount") || strings.HasSuffix(gvk, "/DaemonSet") {
			t.Errorf("prune whitelist must not list a kind app2kube never emits: %q", gvk)
		}
	}
}

// Every kind app2kube actually renders must be registered in emittedKinds,
// otherwise `apply --prune`/`delete all` would orphan it. Rendering a fully
// populated app and checking each emitted kind against the registry catches a
// new generator that forgot to register its kind.
func TestEmittedKindsCoverAllGeneratedResources(t *testing.T) {
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
	app.Ingress = []Ingress{{Host: "full.example.com", TLSCrt: "C", TLSKey: "K"}}

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
	for _, line := range strings.Split(manifest, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "kind:") {
			continue
		}
		kind := strings.TrimSpace(strings.TrimPrefix(line, "kind:"))
		seen[kind] = true
		if !registered[kind] {
			t.Errorf("manifest emits kind %q not registered in emittedKinds (prune/delete would orphan it)", kind)
		}
	}
	// Sanity: the full app must have actually exercised the PDB path.
	if !seen["PodDisruptionBudget"] {
		t.Errorf("expected a PodDisruptionBudget in the full manifest, kinds seen: %v", seen)
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
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := string(stripCreationTimestamp([]byte(tc.in))); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
