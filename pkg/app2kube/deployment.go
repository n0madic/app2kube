package app2kube

import (
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilpointer "k8s.io/utils/pointer"
)

// GetDeployment resource
func (app *App) GetDeployment() (deployment *appsv1.Deployment, err error) {
	if len(app.Deployment.Containers) > 0 {
		replicas := app.Deployment.ReplicaCount
		if replicas < 1 || app.Staging != "" {
			replicas = 1
		}

		var containers []apiv1.Container
		for name, container := range app.Deployment.Containers {
			container.Name = strings.ToLower(name)
			err = app.processContainer(&container)
			if err != nil {
				return
			}
			containers = append(containers, container)
		}

		var initContainers []apiv1.Container
		for name, icontainer := range app.Deployment.InitContainers {
			icontainer.Name = strings.ToLower(name)
			err = app.processContainer(&icontainer)
			if err != nil {
				return
			}
			initContainers = append(initContainers, icontainer)
		}

		deployment = &appsv1.Deployment{
			ObjectMeta: app.GetObjectMeta(app.GetDeploymentName()),
			Spec: appsv1.DeploymentSpec{
				Replicas:             utilpointer.Int32Ptr(replicas),
				RevisionHistoryLimit: utilpointer.Int32Ptr(app.Deployment.RevisionHistoryLimit),
				Selector: &metav1.LabelSelector{
					MatchLabels: app.GetColorLabels(),
				},
				Strategy: app.Deployment.Strategy,
				Template: apiv1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: app.GetColorLabels(),
					},
					Spec: apiv1.PodSpec{
						AutomountServiceAccountToken: utilpointer.BoolPtr(app.Common.MountServiceAccountToken),
						Containers:                   containers,
						InitContainers:               initContainers,
						DNSPolicy:                    app.Common.DNSPolicy,
						EnableServiceLinks:           utilpointer.BoolPtr(app.Common.EnableServiceLinks),
						NodeSelector:                 app.Common.NodeSelector,
						Tolerations:                  app.Common.Tolerations,
					},
				},
			},
		}

		if app.Deployment.BlueGreenColor != "" {
			deployment.ObjectMeta.Labels = app.GetColorLabels()
		}

		if app.Common.Image.PullSecrets != "" {
			deployment.Spec.Template.Spec.ImagePullSecrets = []apiv1.LocalObjectReference{{
				Name: app.Common.Image.PullSecrets,
			}}
		}

		if app.Common.GracePeriod > 0 {
			deployment.Spec.Template.Spec.TerminationGracePeriodSeconds = &app.Common.GracePeriod
		}

		if app.Common.SharedData != "" && len(deployment.Spec.Template.Spec.Containers) > 1 {
			deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, apiv1.Volume{
				Name:         "shared-data",
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
