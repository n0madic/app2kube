package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/n0madic/app2kube/pkg/app2kube"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// Availability requires a fully observed, completed rollout, not a historical
// Progressing=True condition (which survives subsequent readiness failures).
func deploymentAvailable(d *appsv1.Deployment) bool {
	desired := int32(1)
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}
	return d.DeletionTimestamp == nil && desired > 0 && d.Status.ObservedGeneration >= d.Generation &&
		d.Status.UpdatedReplicas == desired && d.Status.Replicas == desired &&
		d.Status.ReadyReplicas == desired && d.Status.AvailableReplicas == desired
}

func deploymentIsAvailable(ctx context.Context, kcs kubernetes.Interface, ns, name string) (bool, error) {
	d, err := kcs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	return deploymentAvailable(d), nil
}

// Wait for uninterrupted health. The post-switch check fails immediately when
// deployment readiness is lost; endpoint propagation can settle within timeout.
func waitBlueGreenHealthy(ctx context.Context, timeout, stable, interval time.Duration, check func(context.Context) (bool, error)) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var healthySince time.Time
	return wait.PollUntilContextCancel(ctx, interval, true, func(ctx context.Context) (bool, error) {
		healthy, err := check(ctx)
		if err != nil {
			return false, err
		}
		if !healthy {
			healthySince = time.Time{}
			return false, nil
		}
		if healthySince.IsZero() {
			healthySince = time.Now()
		}
		return time.Since(healthySince) >= stable, nil
	})
}

func blueGreenServices(ctx context.Context, kcs kubernetes.Interface, app *app2kube.App) ([]corev1.Service, error) {
	svcs, err := kcs.CoreV1().Services(app.Namespace).List(ctx, metav1.ListOptions{LabelSelector: getSelector(app.Labels)})
	if err != nil {
		return nil, err
	}
	var result []corev1.Service
	var color string
	for _, svc := range svcs.Items {
		c := svc.Spec.Selector[app2kube.LabelColor]
		if c == "" {
			continue
		}
		if c != "blue" && c != "green" {
			return nil, fmt.Errorf("service %s has invalid color %q", svc.Name, c)
		}
		if color != "" && color != c {
			return nil, fmt.Errorf("services select different colors; reconcile selectors before deploying")
		}
		color = c
		result = append(result, svc)
	}
	return result, nil
}

// Verify ready EndpointSlice backends really belong to this color, rather than
// trusting Service selectors while endpoint reconciliation is still in flight.
func blueGreenEndpointsReady(ctx context.Context, kcs kubernetes.Interface, app *app2kube.App) (bool, error) {
	d, err := kcs.AppsV1().Deployments(app.Namespace).Get(ctx, app.GetDeploymentName(), metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	selector, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	if err != nil {
		return false, err
	}
	pods, err := kcs.CoreV1().Pods(app.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return false, err
	}
	podUIDs := map[types.UID]bool{}
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp == nil {
			podUIDs[pod.UID] = true
		}
	}
	svcs, err := blueGreenServices(ctx, kcs, app)
	if err != nil {
		return false, err
	}
	if len(svcs) == 0 {
		return false, fmt.Errorf("no color-selecting services found after switch")
	}
	for _, svc := range svcs {
		if svc.Spec.Selector[app2kube.LabelColor] != app.Deployment.BlueGreenColor {
			return false, fmt.Errorf("service %s no longer selects candidate color", svc.Name)
		}
		slices, err := kcs.DiscoveryV1().EndpointSlices(app.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.Set{"kubernetes.io/service-name": svc.Name}.String()})
		if err != nil {
			return false, err
		}
		ready := false
		for _, slice := range slices.Items {
			for _, ep := range slice.Endpoints {
				if ep.Conditions.Ready == nil || !*ep.Conditions.Ready || (ep.Conditions.Terminating != nil && *ep.Conditions.Terminating) {
					continue
				}
				if ep.TargetRef == nil || ep.TargetRef.Kind != "Pod" || !podUIDs[ep.TargetRef.UID] {
					return false, nil
				}
				ready = true
			}
		}
		if !ready {
			return false, nil
		}
	}
	return true, nil
}

// Restore only selectors we observed before this deploy. JSON Patch tests
// protect concurrent service edits; readiness is rechecked before each patch.
func restoreBlueGreenServices(ctx context.Context, kcs kubernetes.Interface, ns, previousName, candidate string, previous []corev1.Service) error {
	if len(previous) == 0 {
		return fmt.Errorf("no previous color exists; no working rollback available")
	}
	for _, old := range previous {
		ready, err := deploymentIsAvailable(ctx, kcs, ns, previousName)
		if err != nil {
			return fmt.Errorf("cannot verify previous deployment; no working rollback confirmed: %w", err)
		}
		if !ready {
			return fmt.Errorf("previous deployment is not ready; no working rollback available")
		}
		svc, err := kcs.CoreV1().Services(ns).Get(ctx, old.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if svc.UID != old.UID {
			return fmt.Errorf("service %s was replaced; refusing rollback", old.Name)
		}
		if svc.Spec.Selector[app2kube.LabelColor] == old.Spec.Selector[app2kube.LabelColor] {
			continue
		}
		if svc.Spec.Selector[app2kube.LabelColor] != candidate {
			return fmt.Errorf("service %s was changed concurrently; refusing rollback", old.Name)
		}
		// Accept only the expected color change, preserving unrelated selector edits.
		expected := old.DeepCopy().Spec.Selector
		expected[app2kube.LabelColor] = candidate
		if !reflect.DeepEqual(expected, svc.Spec.Selector) {
			return fmt.Errorf("service %s selector changed concurrently; refusing rollback", old.Name)
		}
		patch, err := json.Marshal([]map[string]interface{}{
			{"op": "test", "path": "/metadata/resourceVersion", "value": svc.ResourceVersion},
			{"op": "test", "path": "/spec/selector", "value": svc.Spec.Selector},
			{"op": "replace", "path": "/spec/selector", "value": old.Spec.Selector},
		})
		if err != nil {
			return err
		}
		if _, err := kcs.CoreV1().Services(ns).Patch(ctx, old.Name, types.JSONPatchType, patch, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("rollback service %s (rollback may be partial): %w", old.Name, err)
		}
	}
	return nil
}

// Diagnostics intentionally omit logs, env, Secret values and event messages,
// since application errors can contain credentials (e.g. Redis session URLs).
func blueGreenDiagnostics(ctx context.Context, out io.Writer, kcs kubernetes.Interface, app *app2kube.App) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ds, err := kcs.AppsV1().Deployments(app.Namespace).List(ctx, metav1.ListOptions{LabelSelector: getSelector(app.Labels)})
	if err != nil {
		fmt.Fprintf(out, "Diagnostics: %v\n", err)
	} else {
		for _, d := range ds.Items {
			fmt.Fprintf(out, "Deployment %s: desired=%v ready=%d available=%d\n", d.Name, replicasForDiagnostics(d), d.Status.ReadyReplicas, d.Status.AvailableReplicas)
			for _, c := range d.Status.Conditions {
				fmt.Fprintf(out, "  %s=%s reason=%s\n", c.Type, c.Status, c.Reason)
			}
		}
	}
	pods, err := kcs.CoreV1().Pods(app.Namespace).List(ctx, metav1.ListOptions{LabelSelector: getSelector(app.Labels)})
	if err != nil {
		fmt.Fprintf(out, "Pod diagnostics: %v\n", err)
		return
	}
	for _, pod := range pods.Items {
		fmt.Fprintf(out, "Pod %s: phase=%s node=%s\n", pod.Name, pod.Status.Phase, pod.Spec.NodeName)
		for _, c := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
			fmt.Fprintf(out, "  %s: ready=%t restarts=%d", c.Name, c.Ready, c.RestartCount)
			if c.State.Waiting != nil {
				fmt.Fprintf(out, " waiting=%s", c.State.Waiting.Reason)
			}
			if c.State.Terminated != nil {
				fmt.Fprintf(out, " terminated=%s exit=%d", c.State.Terminated.Reason, c.State.Terminated.ExitCode)
			}
			fmt.Fprintln(out)
		}
		events, err := kcs.CoreV1().Events(app.Namespace).List(ctx, metav1.ListOptions{FieldSelector: "involvedObject.uid=" + string(pod.UID)})
		if err != nil {
			fmt.Fprintf(out, "  Event diagnostics: %v\n", err)
			continue
		}
		for _, e := range events.Items {
			if e.Type == corev1.EventTypeWarning {
				fmt.Fprintf(out, "  Warning %s count=%d last=%s\n", e.Reason, e.Count, e.LastTimestamp.String())
			}
		}
	}
}

func replicasForDiagnostics(d appsv1.Deployment) int32 {
	if d.Spec.Replicas == nil {
		return 1
	}
	return *d.Spec.Replicas
}

// Keep the observation interval in addition to the readiness/propagation budget.
func blueGreenWaitBudget(minutes int, observation time.Duration) time.Duration {
	if minutes == 0 {
		return 0
	}
	return time.Duration(minutes)*time.Minute + observation
}
