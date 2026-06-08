package cmd

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/n0madic/app2kube/pkg/app2kube"

	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/kubectl/pkg/validation"
)

// Apply must feed the rendered manifest to kubectl through an in-memory reader
// (resource.Builder.Stream) instead of hijacking the process's global os.Stdin.
// This parses a multi-document manifest into the same resource.Infos kubectl
// apply would build, and asserts os.Stdin is left untouched.
func TestStreamApplyObjectsParsesManifestWithoutStdin(t *testing.T) {
	orig := os.Stdin
	manifest := strings.Join([]string{
		`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"cm-a","namespace":"app"},"data":{"k":"v"}}`,
		`{"apiVersion":"v1","kind":"Service","metadata":{"name":"svc-b","namespace":"app"},"spec":{"ports":[{"port":80}]}}`,
	}, "\n")

	infos, err := streamApplyObjects(resource.NewLocalBuilder(), validation.NullSchema{}, "app", false, "", manifest)
	if err != nil {
		t.Fatalf("streamApplyObjects: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("got %d infos, want 2", len(infos))
	}

	names := []string{infos[0].Name, infos[1].Name}
	for _, want := range []string{"cm-a", "svc-b"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing object %q in %v", want, names)
		}
	}
	for _, info := range infos {
		if info.Namespace != "app" {
			t.Errorf("object %q namespace = %q, want app", info.Name, info.Namespace)
		}
	}
	if os.Stdin != orig {
		t.Errorf("os.Stdin was modified; apply must not touch global stdin")
	}
}

// Regression: a PodDisruptionBudget is emitted with the Deployment but is
// conditional on replicas>1, so it can drop out of the manifest. `apply --prune`
// must be allowed to delete it, otherwise scaling back to a single replica
// orphans a minAvailable PDB that blocks every node drain.
func TestApplyPruneWhitelistIncludesPDB(t *testing.T) {
	whitelist := app2kube.NewApp().PruneWhitelist()
	if !slices.Contains(whitelist, "policy/v1/PodDisruptionBudget") {
		t.Errorf("apply --prune whitelist must include policy/v1/PodDisruptionBudget: %v", whitelist)
	}
}

// Regression: kubectl's "all" category excludes PodDisruptionBudgets, so
// `delete all` must name poddisruptionbudgets explicitly or the PDB survives
// teardown and keeps blocking voluntary disruptions.
func TestDeleteAllResourceTypesIncludesPDB(t *testing.T) {
	resources := app2kube.NewApp().DeleteResourceTypes()
	if !strings.Contains(resources, "poddisruptionbudget") {
		t.Errorf("`delete all` resource list must include poddisruptionbudgets: %q", resources)
	}
}

// #60: on a blue/green phase-1 or trackReady failure the operator must be told
// the new color was deployed but traffic was NOT switched, and that re-running
// is safe.
func TestBlueGreenNotSwitchedMsg(t *testing.T) {
	msg := strings.ToLower(blueGreenNotSwitchedMsg("green"))
	for _, want := range []string{"green", "not switched", "re-run"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q must mention %q", msg, want)
		}
	}
}

// Regression (#26): the --track value must be validated at parse time (before
// the cluster is mutated), accepting only "", "ready", "follow" (case-insensitive)
// and rejecting typos like "redy".
func TestValidateTrackValue(t *testing.T) {
	for _, v := range []string{"", "ready", "follow", "READY", "Follow"} {
		if err := validateTrackValue(v); err != nil {
			t.Errorf("valid track %q rejected: %v", v, err)
		}
	}
	for _, v := range []string{"redy", "yes", "true", "foll", "status"} {
		if err := validateTrackValue(v); err == nil {
			t.Errorf("invalid track %q accepted", v)
		}
	}
}
