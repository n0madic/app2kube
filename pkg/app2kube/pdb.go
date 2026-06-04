package app2kube

import (
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// GetPodDisruptionBudget returns a PodDisruptionBudget that keeps at least one
// pod running through a voluntary disruption (node drain/upgrade), but only when
// the Deployment runs more than one replica. A single-replica PDB with
// minAvailable:1 would block every drain (the node could never be emptied), and
// nothing is emitted for the no-container case. The selector matches the
// Deployment's stable selector so it covers exactly its pods (#47).
func (app *App) GetPodDisruptionBudget() (*policyv1.PodDisruptionBudget, error) {
	if len(app.Deployment.Containers) == 0 {
		return nil, nil
	}

	replicas := int32(1)
	if app.Deployment.ReplicaCount != nil {
		replicas = *app.Deployment.ReplicaCount
	}
	if replicas <= 1 {
		return nil, nil
	}

	minAvailable := intstr.IntOrString{Type: intstr.Int, IntVal: 1}
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: app.GetObjectMeta(app.GetDeploymentName()),
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: &minAvailable,
			Selector:     &metav1.LabelSelector{MatchLabels: app.GetSelectorLabels()},
		},
	}, nil
}
