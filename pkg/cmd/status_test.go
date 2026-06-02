package cmd

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// The status table functions take a kubernetes.Interface so they can be
// exercised against a fake client set, without a live cluster.

func statusLabels() map[string]string {
	return map[string]string{"app.kubernetes.io/instance": "production"}
}

func TestGetDeploymentStatus(t *testing.T) {
	labels := statusLabels()
	kcs := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "ns", Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
		},
		Status: appsv1.DeploymentStatus{Replicas: 2, ReadyReplicas: 1},
	})

	out, err := getDeploymentStatus(context.Background(), kcs, "ns", labels)
	if err != nil {
		t.Fatalf("getDeploymentStatus: %v", err)
	}
	if !strings.Contains(out, "demo") {
		t.Errorf("expected deployment name in output, got %q", out)
	}
	if !strings.Contains(out, "1/2") {
		t.Errorf("expected ready ratio 1/2 in output, got %q", out)
	}
}

func TestGetServicesStatus(t *testing.T) {
	labels := statusLabels()
	kcs := fake.NewSimpleClientset(&apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-svc", Namespace: "ns", Labels: labels},
		Spec: apiv1.ServiceSpec{
			Type:      apiv1.ServiceTypeClusterIP,
			ClusterIP: "10.0.0.1",
			Ports:     []apiv1.ServicePort{{Port: 8080}},
		},
	})

	out, err := getServicesStatus(context.Background(), kcs, "ns", labels)
	if err != nil {
		t.Fatalf("getServicesStatus: %v", err)
	}
	if !strings.Contains(out, "demo-svc") {
		t.Errorf("expected service name in output, got %q", out)
	}
	if !strings.Contains(out, "8080") {
		t.Errorf("expected port 8080 in output, got %q", out)
	}
}

func TestGetServicesStatusEmpty(t *testing.T) {
	// No matching resources yields an empty table (skipped in output).
	kcs := fake.NewSimpleClientset()
	out, err := getServicesStatus(context.Background(), kcs, "ns", statusLabels())
	if err != nil {
		t.Fatalf("getServicesStatus: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty output for no services, got %q", out)
	}
}
