package app2kube

import (
	"fmt"
	"os"
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
		// An unset replicaCount defaults to 1; an explicit value (including 0, for
		// scale-to-zero) is honored. The field is *int32 so unset is
		// distinguishable from an explicit 0 (#42).
		replicas := int32(1)
		if app.Deployment.ReplicaCount != nil {
			replicas = *app.Deployment.ReplicaCount
			if replicas < 0 {
				replicas = 0
			}
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

		// Roll the Deployment when the config it consumes changes: a checksum of
		// the referenced ConfigMap/Secret in the pod template makes an envFrom
		// change part of the template (#22). Computed from the rendered
		// containers, so only the config actually wired in is hashed.
		checksums := app.configChecksumAnnotations(append(append([]apiv1.Container{}, containers...), initContainers...))

		deployment = &appsv1.Deployment{
			ObjectMeta: app.GetObjectMeta(app.GetDeploymentName()),
			Spec: appsv1.DeploymentSpec{
				Replicas:             ptr.To(replicas),
				RevisionHistoryLimit: ptr.To(app.Deployment.RevisionHistoryLimit),
				Selector: &metav1.LabelSelector{
					MatchLabels: app.GetSelectorLabels(),
				},
				Strategy: app.Deployment.Strategy,
				Template: apiv1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels:      app.GetColorLabels(),
						Annotations: checksums,
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

		// processContainer mounts shared-data on every app-image container (main
		// and init) whenever SharedData is set, so the EmptyDir volume must exist
		// whenever SharedData is set — even with a single container — otherwise a
		// mount references a missing volume and the pod spec is invalid (#18).
		if app.Common.SharedData != "" {
			deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, apiv1.Volume{
				Name:         sharedDataVolumeName,
				VolumeSource: apiv1.VolumeSource{EmptyDir: &apiv1.EmptyDirVolumeSource{}},
			})
		}

		for volName, vol := range app.Volumes {
			// A ReadWriteOnce(-only) PVC can be bound on a single node; mounting it
			// into a multi-replica Deployment makes pods on other nodes unschedulable
			// (a StatefulSet would be correct but is out of scope). Warn instead of
			// silently emitting a spec that deadlocks (#48).
			if replicas > 1 && len(vol.Spec.AccessModes) > 0 && !pvcAllowsMultiAttach(vol.Spec.AccessModes) {
				fmt.Fprintf(os.Stderr, "WARNING: PVC %q (%v) is mounted into a %d-replica Deployment; pods on different nodes cannot share a ReadWriteOnce volume and scheduling will block (use a single replica or a ReadWriteMany volume; StatefulSet is out of scope)\n", volName, vol.Spec.AccessModes, replicas)
			}
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
