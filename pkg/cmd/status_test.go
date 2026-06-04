package cmd

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
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

// A Deployment matched by the app labels but carrying a nil spec.selector
// (foreign / hand-edited object) must not panic getDeploymentStatus.
func TestGetDeploymentStatusNilSelector(t *testing.T) {
	labels := statusLabels()
	kcs := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "foreign", Namespace: "ns", Labels: labels},
		Spec:       appsv1.DeploymentSpec{Selector: nil},
		Status:     appsv1.DeploymentStatus{Replicas: 1, ReadyReplicas: 1},
	})

	out, err := getDeploymentStatus(context.Background(), kcs, "ns", labels)
	if err != nil {
		t.Fatalf("getDeploymentStatus with nil selector: %v", err)
	}
	if !strings.Contains(out, "foreign") {
		t.Errorf("expected deployment name in output, got %q", out)
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

// #38: coverage for the previously untested status renderers, exercised against
// a fake client set. Each asserts the resource name and a key column render.

func TestGetConfigmapStatus(t *testing.T) {
	labels := statusLabels()
	kcs := fake.NewSimpleClientset(&apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-cm", Namespace: "ns", Labels: labels},
		Data:       map[string]string{"a": "1", "b": "2"},
	})
	out, err := getConfigmapStatus(context.Background(), kcs, "ns", labels)
	if err != nil {
		t.Fatalf("getConfigmapStatus: %v", err)
	}
	if !strings.Contains(out, "demo-cm") || !strings.Contains(out, "2") {
		t.Errorf("expected name and DATA count 2 in output, got %q", out)
	}
}

func TestGetSecretsStatus(t *testing.T) {
	labels := statusLabels()
	kcs := fake.NewSimpleClientset(&apiv1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-secret", Namespace: "ns", Labels: labels},
		Data:       map[string][]byte{"pwd": []byte("x")},
	})
	out, err := getSecretsStatus(context.Background(), kcs, "ns", labels)
	if err != nil {
		t.Fatalf("getSecretsStatus: %v", err)
	}
	if !strings.Contains(out, "demo-secret") {
		t.Errorf("expected secret name in output, got %q", out)
	}
}

func TestGetCronJobsStatus(t *testing.T) {
	labels := statusLabels()
	kcs := fake.NewSimpleClientset(&batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-cron", Namespace: "ns", Labels: labels},
		Spec:       batchv1.CronJobSpec{Schedule: "*/5 * * * *"},
	})
	out, err := getCronJobsStatus(context.Background(), kcs, "ns", labels)
	if err != nil {
		t.Fatalf("getCronJobsStatus: %v", err)
	}
	if !strings.Contains(out, "demo-cron") || !strings.Contains(out, "*/5 * * * *") {
		t.Errorf("expected cron name and schedule in output, got %q", out)
	}
}

func TestGetPVCStatus(t *testing.T) {
	labels := statusLabels()
	kcs := fake.NewSimpleClientset(&apiv1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-pvc", Namespace: "ns", Labels: labels},
		Status:     apiv1.PersistentVolumeClaimStatus{Phase: apiv1.ClaimBound},
	})
	out, err := getPVCStatus(context.Background(), kcs, "ns", labels)
	if err != nil {
		t.Fatalf("getPVCStatus: %v", err)
	}
	if !strings.Contains(out, "demo-pvc") || !strings.Contains(out, "Bound") {
		t.Errorf("expected pvc name and Bound phase in output, got %q", out)
	}
}

func TestGetPodsStatus(t *testing.T) {
	labels := statusLabels()
	kcs := fake.NewSimpleClientset(&apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-pod", Namespace: "ns", Labels: labels},
		Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "app"}}},
		Status:     apiv1.PodStatus{Phase: apiv1.PodRunning},
	})
	out, err := getPodsStatus(context.Background(), kcs, "ns", labels)
	if err != nil {
		t.Fatalf("getPodsStatus: %v", err)
	}
	if !strings.Contains(out, "demo-pod") || !strings.Contains(out, "Running") {
		t.Errorf("expected pod name and Running phase in output, got %q", out)
	}
}

func TestGetIngressStatus(t *testing.T) {
	labels := statusLabels()
	kcs := fake.NewSimpleClientset(&netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-ing", Namespace: "ns", Labels: labels},
		Spec:       netv1.IngressSpec{Rules: []netv1.IngressRule{{Host: "demo.example.com"}}},
	})
	out, err := getIngressStatus(context.Background(), kcs, "ns", labels)
	if err != nil {
		t.Fatalf("getIngressStatus: %v", err)
	}
	if !strings.Contains(out, "demo-ing") || !strings.Contains(out, "demo.example.com") {
		t.Errorf("expected ingress name and host in output, got %q", out)
	}
}
