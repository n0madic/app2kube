package app2kube

import (
	"fmt"
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

			if container.Image == "" {
				if app.Common.Image.Repository != "" {
					container.Image = app.Common.Image.Repository + ":" + app.Common.Image.Tag
				} else {
					panic(fmt.Sprintf("Image required for container: %s\n", name))
				}
			}
			if app.Common.Image.Repository != "" {
				imageParts := strings.Split(container.Image, ":")
				if len(imageParts) == 2 && imageParts[0] == app.Common.Image.Repository {
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
						Name: app.GetReleaseName(),
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

			if app.Staging != "" {
				container.Resources = apiv1.ResourceRequirements{}
			}

			containers = append(containers, container)
		}

		replicas := app.Deployment.ReplicaCount
		if replicas < 1 || app.Staging != "" {
			replicas = 1
		}

		deployment := &appsv1.Deployment{
			ObjectMeta: app.GetObjectMeta(app.GetReleaseName()),
			Spec: appsv1.DeploymentSpec{
				Replicas:             &replicas,
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
						NodeSelector:                 app.Common.NodeSelector,
						Tolerations:                  app.Common.Tolerations,
					},
				},
			},
		}

		if app.Common.Image.PullSecrets != "" {
			deployment.Spec.Template.Spec.ImagePullSecrets = []apiv1.LocalObjectReference{
				apiv1.LocalObjectReference{
					Name: app.Common.Image.PullSecrets,
				},
			}
		}

		if app.Common.GracePeriod > 0 {
			deployment.Spec.Template.Spec.TerminationGracePeriodSeconds = &app.Common.GracePeriod
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
						ClaimName: app.GetReleaseName() + "-" + volName,
					},
				},
			})
		}

		yaml = getYAML("Deployment: "+app.GetReleaseName(), deployment)
	}
	return
}
