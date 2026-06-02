package app2kube

import (
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// podSecurityContext returns the Pod-level security context to emit: the user's
// common.securityContext when set (verbatim — an explicit empty one acts as an
// opt-out), otherwise a conservative default that only sets the non-breaking
// seccompProfile: RuntimeDefault. runAsNonRoot / fsGroup and other potentially
// breaking fields are left to explicit configuration.
func (app *App) podSecurityContext() *apiv1.PodSecurityContext {
	if app.Common.SecurityContext != nil {
		return app.Common.SecurityContext
	}
	return &apiv1.PodSecurityContext{
		SeccompProfile: &apiv1.SeccompProfile{Type: apiv1.SeccompProfileTypeRuntimeDefault},
	}
}

// GetDeployment resource
func (app *App) GetDeployment() (deployment *appsv1.Deployment, err error) {
	if len(app.Deployment.Containers) > 0 {
		replicas := app.Deployment.ReplicaCount
		if replicas < 1 {
			replicas = 1
		}

		var containers []apiv1.Container
		for name, container := range app.Deployment.Containers {
			container.Name = strings.ToLower(name)
			err = app.processContainer(&container, false)
			if err != nil {
				return
			}
			containers = append(containers, container)
		}

		var initContainers []apiv1.Container
		for name, icontainer := range app.Deployment.InitContainers {
			icontainer.Name = strings.ToLower(name)
			err = app.processContainer(&icontainer, true)
			if err != nil {
				return
			}
			initContainers = append(initContainers, icontainer)
		}

		affinity, err := app.getAffinity()
		if err != nil {
			return nil, err
		}

		deployment = &appsv1.Deployment{
			ObjectMeta: app.GetObjectMeta(app.GetDeploymentName()),
			Spec: appsv1.DeploymentSpec{
				Replicas:             ptr.To(replicas),
				RevisionHistoryLimit: ptr.To(app.Deployment.RevisionHistoryLimit),
				Selector: &metav1.LabelSelector{
					MatchLabels: app.GetColorLabels(),
				},
				Strategy: app.Deployment.Strategy,
				Template: apiv1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: app.GetColorLabels(),
					},
					Spec: apiv1.PodSpec{
						Affinity:                     affinity,
						AutomountServiceAccountToken: ptr.To(app.Common.MountServiceAccountToken),
						Containers:                   containers,
						InitContainers:               initContainers,
						DNSPolicy:                    app.Common.DNSPolicy,
						EnableServiceLinks:           ptr.To(app.Common.EnableServiceLinks),
						NodeSelector:                 app.Common.NodeSelector,
						SecurityContext:              app.podSecurityContext(),
						Tolerations:                  app.Common.Tolerations,
					},
				},
			},
		}

		if app.Deployment.BlueGreenColor != "" {
			deployment.Labels = app.GetColorLabels()
		}

		if app.Common.Image.PullSecrets != "" {
			deployment.Spec.Template.Spec.ImagePullSecrets = []apiv1.LocalObjectReference{{
				Name: app.Common.Image.PullSecrets,
			}}
		}

		if app.Common.GracePeriod > 0 {
			deployment.Spec.Template.Spec.TerminationGracePeriodSeconds = &app.Common.GracePeriod
		}

		// Count init containers too: an app-image init container also receives the
		// shared-data VolumeMount in processContainer, so the EmptyDir volume must
		// exist whenever main+init together exceed one container — otherwise the
		// init mount references a missing volume and the pod spec is invalid (#18).
		if app.Common.SharedData != "" &&
			len(deployment.Spec.Template.Spec.Containers)+len(deployment.Spec.Template.Spec.InitContainers) > 1 {
			deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, apiv1.Volume{
				Name:         sharedDataVolumeName,
				VolumeSource: apiv1.VolumeSource{EmptyDir: &apiv1.EmptyDirVolumeSource{}},
			})
		}

		for volName := range app.Volumes {
			deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, apiv1.Volume{
				Name: volName,
				VolumeSource: apiv1.VolumeSource{
					PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{
						ClaimName: app.GetReleaseName() + "-" + volName,
					},
				},
			})
		}
	}
	return
}
