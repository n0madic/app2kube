package app2kube

import (
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetDeployment YAML
func (app *App) GetDeployment() (yaml string) {
	if len(app.Deployment.Containers) > 0 {
		containers := []apiv1.Container{}
		for name, container := range app.Deployment.Containers {
			container.Name = name
			commonImage := app.Common.Image.Repository + ":" + app.Common.Image.Tag
			if container.Image == "" {
				container.Image = commonImage
			} else {
				imageParts := strings.Split(container.Image, ":")
				if len(imageParts) == 2 && imageParts[0] == commonImage {
					container.Image = imageParts[0] + ":" + app.Common.Image.Tag
				}
			}
			for key, value := range app.Configmap {
				container.Env = append(container.Env, apiv1.EnvVar{Name: key, Value: value})
			}
			if app.CommitHash != "" {
				container.Env = append(container.Env, apiv1.EnvVar{Name: "COMMIT_HASH", Value: app.CommitHash})
			}
			if len(app.Secrets) > 0 {
				container.EnvFrom = append(
					container.EnvFrom,
					apiv1.EnvFromSource{SecretRef: &apiv1.SecretEnvSource{LocalObjectReference: apiv1.LocalObjectReference{
						Name: app.Name,
					}}},
				)
			}
			if app.Deployment.SharedData != "" {
				container.VolumeMounts = append(container.VolumeMounts, apiv1.VolumeMount{
					Name:      "shared-data",
					MountPath: app.Deployment.SharedData,
				})
			}
			for volName, volume := range app.Volumes {
				container.VolumeMounts = append(container.VolumeMounts, apiv1.VolumeMount{
					Name:      volName,
					MountPath: volume.MountPath,
				})
			}
			containers = append(containers, container)
		}

		replica := app.Deployment.ReplicaCount
		if replica < 1 {
			replica = 1
		}

		deployment := &appsv1.Deployment{
			ObjectMeta: app.GetObjectMeta(app.Name),
			Spec: appsv1.DeploymentSpec{
				Replicas:             &replica,
				RevisionHistoryLimit: &app.Deployment.RevisionHistoryLimit,
				Selector: &metav1.LabelSelector{
					MatchLabels: app.Labels,
				},
				Strategy: app.Deployment.Strategy,
				Template: apiv1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: app.Labels,
					},
					Spec: apiv1.PodSpec{
						AutomountServiceAccountToken: &app.Common.MountServiceAccountToken,
						Containers:                   containers,
						DNSPolicy:                    app.Common.DNSPolicy,
						EnableServiceLinks:           &app.Common.EnableServiceLinks,
						ImagePullSecrets: []apiv1.LocalObjectReference{
							apiv1.LocalObjectReference{
								Name: app.Common.Image.PullSecrets,
							},
						},
						NodeSelector:                  app.Common.NodeSelector,
						TerminationGracePeriodSeconds: &app.Common.GracePeriod,
						Tolerations:                   app.Common.Tolerations,
					},
				},
			},
		}

		if app.Deployment.SharedData != "" {
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
						ClaimName: app.Name + "-" + volName,
					},
				},
			})
		}

		yaml = getYAML("Deployment: "+app.Name, deployment)
	}
	return
}
