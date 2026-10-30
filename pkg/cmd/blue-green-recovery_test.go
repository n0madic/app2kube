package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/n0madic/app2kube/pkg/app2kube"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func healthyDeployment(name string) *appsv1.Deployment {
	replicas := int32(2)
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", Generation: 2}, Spec: appsv1.DeploymentSpec{Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "candidate"}}}, Status: appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 2, UpdatedReplicas: 2, ReadyReplicas: 2, AvailableReplicas: 2}}
}

func TestDeploymentAvailabilityRejectsHistoricalAndIncompleteReadiness(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*appsv1.Deployment)
	}{
		{"zero replicas", func(d *appsv1.Deployment) { *d.Spec.Replicas = 0 }},
		{"stale generation", func(d *appsv1.Deployment) { d.Status.ObservedGeneration = 1 }},
		{"unready", func(d *appsv1.Deployment) { d.Status.ReadyReplicas = 0 }},
		{"unavailable", func(d *appsv1.Deployment) { d.Status.AvailableReplicas = 0 }},
		{"old pods", func(d *appsv1.Deployment) { d.Status.UpdatedReplicas = 1 }},
		{"incomplete rollout", func(d *appsv1.Deployment) { d.Status.Replicas = 3 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := healthyDeployment("blue")
			if !deploymentAvailable(d) {
				t.Fatal("fixture not ready")
			}
			tc.change(d)
			if deploymentAvailable(d) {
				t.Fatal("accepted unsafe deployment")
			}
		})
	}
}

func TestStableReadinessResetsAndHonorsCancellation(t *testing.T) {
	calls := 0
	var recoveredAt time.Time
	err := waitBlueGreenHealthy(context.Background(), time.Second, 10*time.Millisecond, time.Millisecond, func(context.Context) (bool, error) {
		calls++
		if calls == 2 {
			time.Sleep(15 * time.Millisecond)
			return false, nil
		}
		if calls == 3 {
			recoveredAt = time.Now()
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if recoveredAt.IsZero() || time.Since(recoveredAt) < 10*time.Millisecond {
		t.Fatal("did not restart stability window after readiness loss")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitBlueGreenHealthy(ctx, 0, time.Second, time.Millisecond, func(context.Context) (bool, error) { return false, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if err := waitBlueGreenHealthy(context.Background(), 5*time.Millisecond, time.Second, time.Millisecond, func(context.Context) (bool, error) { return false, nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestRollbackOnlyToCurrentlyReadyDeployment(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(map[bool]string{false: "both down", true: "healthy previous"}[ready], func(t *testing.T) {
			d := healthyDeployment("cab-blue")
			if !ready {
				d.Status.ReadyReplicas = 0
			}
			old := corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "cab", Namespace: "ns", UID: "service-uid", ResourceVersion: "1"}, Spec: corev1.ServiceSpec{Selector: map[string]string{app2kube.LabelColor: "blue", "app": "cab"}}}
			current := old.DeepCopy()
			current.Spec.Selector[app2kube.LabelColor] = "green"
			client := fake.NewSimpleClientset(d, current)
			err := restoreBlueGreenServices(context.Background(), client, "ns", "cab-blue", "green", []corev1.Service{old})
			if ready && err != nil {
				t.Fatal(err)
			}
			if !ready && (err == nil || !strings.Contains(err.Error(), "no working rollback")) {
				t.Fatalf("got %v", err)
			}
			got, _ := client.CoreV1().Services("ns").Get(context.Background(), "cab", metav1.GetOptions{})
			want := "green"
			if ready {
				want = "blue"
			}
			if got.Spec.Selector[app2kube.LabelColor] != want {
				t.Fatalf("selector = %v", got.Spec.Selector)
			}
			for _, a := range client.Actions() {
				if a.GetVerb() == "delete" {
					t.Fatal("deleted resource")
				}
				if !ready && a.GetVerb() == "patch" {
					t.Fatal("patched despite unhealthy previous")
				}
			}
		})
	}
}

func TestRollbackRechecksReadinessBeforeEveryService(t *testing.T) {
	d := healthyDeployment("cab-blue")
	services := []corev1.Service{}
	client := fake.NewSimpleClientset(d)
	for _, name := range []string{"one", "two"} {
		old := corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID(name), ResourceVersion: "1"}, Spec: corev1.ServiceSpec{Selector: map[string]string{app2kube.LabelColor: "blue"}}}
		services = append(services, old)
		current := old.DeepCopy()
		current.Spec.Selector[app2kube.LabelColor] = "green"
		_, _ = client.CoreV1().Services("ns").Create(context.Background(), current, metav1.CreateOptions{})
	}
	checks := 0
	client.PrependReactor("get", "deployments", func(a ktesting.Action) (bool, runtime.Object, error) {
		checks++
		if checks == 2 {
			failed := d.DeepCopy()
			failed.Status.ReadyReplicas = 0
			return true, failed, nil
		}
		return false, nil, nil
	})
	if err := restoreBlueGreenServices(context.Background(), client, "ns", "cab-blue", "green", services); err == nil {
		t.Fatal("accepted previous deployment becoming unready")
	}
	first, _ := client.CoreV1().Services("ns").Get(context.Background(), "one", metav1.GetOptions{})
	second, _ := client.CoreV1().Services("ns").Get(context.Background(), "two", metav1.GetOptions{})
	if first.Spec.Selector[app2kube.LabelColor] != "blue" || second.Spec.Selector[app2kube.LabelColor] != "green" {
		t.Fatal("readiness was not rechecked between patches")
	}
}

func TestRollbackPreservesConcurrentSelectorChanges(t *testing.T) {
	old := corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "cab", Namespace: "ns", UID: "uid", ResourceVersion: "1"}, Spec: corev1.ServiceSpec{Selector: map[string]string{app2kube.LabelColor: "blue", "app": "cab"}}}
	current := old.DeepCopy()
	current.Spec.Selector[app2kube.LabelColor] = "green"
	current.Spec.Selector["app"] = "another"
	client := fake.NewSimpleClientset(healthyDeployment("cab-blue"), current)
	if err := restoreBlueGreenServices(context.Background(), client, "ns", "cab-blue", "green", []corev1.Service{old}); err == nil {
		t.Fatal("accepted concurrent selector change")
	}
	for _, a := range client.Actions() {
		if a.GetVerb() == "patch" {
			t.Fatal("overwrote concurrent change")
		}
	}
}

func TestEndpointsMustBelongToCandidate(t *testing.T) {
	for _, uid := range []types.UID{"old-pod", "candidate-pod"} {
		t.Run(string(uid), func(t *testing.T) {
			app := app2kube.NewApp()
			app.Name = "cab"
			app.Namespace = "ns"
			app.Labels = map[string]string{"app": "cab"}
			app.Deployment.BlueGreenColor = "green"
			d := healthyDeployment(app.GetDeploymentName())
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "ns", UID: "candidate-pod", Labels: map[string]string{"app": "candidate"}}}
			svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "cab", Namespace: "ns", Labels: app.Labels}, Spec: corev1.ServiceSpec{Selector: map[string]string{app2kube.LabelColor: "green"}}}
			ready := true
			slice := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Name: "slice", Namespace: "ns", Labels: map[string]string{"kubernetes.io/service-name": "cab"}}, Endpoints: []discoveryv1.Endpoint{{Addresses: []string{"10.0.0.1"}, Conditions: discoveryv1.EndpointConditions{Ready: &ready}, TargetRef: &corev1.ObjectReference{Kind: "Pod", UID: uid}}}}
			client := fake.NewSimpleClientset(d, pod, svc, slice)
			healthy, err := blueGreenEndpointsReady(context.Background(), client, app)
			if err != nil {
				t.Fatal(err)
			}
			if healthy != (uid == "candidate-pod") {
				t.Fatalf("healthy=%v for %s", healthy, uid)
			}
		})
	}
}
