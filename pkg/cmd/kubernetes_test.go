package cmd

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// #65: the blue/green pre-delete must ignore a NotFound — on the first (zero)
// deploy of a color there is nothing to delete — but abort on a real error
// (RBAC, connectivity) instead of printing it and letting the doomed apply
// proceed.
func TestPreDeleteDeployment(t *testing.T) {
	ctx := context.Background()

	// Existing deployment → deleted cleanly, no error.
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web-blue", Namespace: "prod"}}
	kcs := fake.NewSimpleClientset(dep)
	if err := preDeleteDeployment(ctx, kcs, "web-blue", "prod"); err != nil {
		t.Errorf("deleting an existing deployment must succeed: %v", err)
	}

	// First (zero) deploy: nothing to delete → NotFound is ignored.
	kcs = fake.NewSimpleClientset()
	if err := preDeleteDeployment(ctx, kcs, "web-blue", "prod"); err != nil {
		t.Errorf("a missing deployment (first deploy) must be ignored, got %v", err)
	}

	// A real error (forbidden / connectivity) must abort, not be swallowed.
	kcs = fake.NewSimpleClientset()
	kcs.PrependReactor("delete", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	if err := preDeleteDeployment(ctx, kcs, "web-blue", "prod"); err == nil {
		t.Errorf("a real delete error must abort the deploy, not be swallowed")
	}
}

// blue-green prune must remove the color's PodDisruptionBudget too, not leave it
// orphaned. A missing PDB (single-replica deploy) is the expected NotFound case
// and must be ignored; a real error must abort.
func TestPrunePodDisruptionBudget(t *testing.T) {
	ctx := context.Background()

	// Existing PDB → deleted cleanly.
	pdb := &policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{Name: "web-blue", Namespace: "prod"}}
	kcs := fake.NewSimpleClientset(pdb)
	if err := prunePodDisruptionBudget(ctx, kcs, "web-blue", "prod"); err != nil {
		t.Errorf("deleting an existing PDB must succeed: %v", err)
	}
	if _, err := kcs.PolicyV1().PodDisruptionBudgets("prod").Get(ctx, "web-blue", metav1.GetOptions{}); err == nil {
		t.Errorf("PDB must be gone after prune")
	}

	// Single-replica deploy: no PDB exists → NotFound is ignored.
	kcs = fake.NewSimpleClientset()
	if err := prunePodDisruptionBudget(ctx, kcs, "web-blue", "prod"); err != nil {
		t.Errorf("a missing PDB must be ignored, got %v", err)
	}

	// A real error must abort, not be swallowed.
	kcs = fake.NewSimpleClientset()
	kcs.PrependReactor("delete", "poddisruptionbudgets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	if err := prunePodDisruptionBudget(ctx, kcs, "web-blue", "prod"); err == nil {
		t.Errorf("a real delete error must abort, not be swallowed")
	}
}
