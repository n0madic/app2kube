package app2kube

import (
	"fmt"
	"strings"

	apiv1 "k8s.io/api/core/v1"
)

func (app *App) processContainers(source map[string]apiv1.Container) (containers []apiv1.Container) {
	for name, container := range source {
		container.Name = strings.ToLower(name)

		thirdpartyImage := false
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
			} else {
				thirdpartyImage = true
			}
		}

		if !thirdpartyImage {
			for key, value := range app.Configmap {
				container.Env = append(container.Env, apiv1.EnvVar{Name: key, Value: value})
			}

			if len(app.Secrets) > 0 {
				container.EnvFrom = append(
					container.EnvFrom,
					apiv1.EnvFromSource{SecretRef: &apiv1.SecretEnvSource{LocalObjectReference: apiv1.LocalObjectReference{
						Name: app.GetReleaseName(),
					}}},
				)
			}

			if app.Common.SharedData != "" && len(source) > 1 {
				container.VolumeMounts = append(container.VolumeMounts, apiv1.VolumeMount{
					Name:      "shared-data",
					MountPath: app.Common.SharedData,
				})
			}

			for volName, volume := range app.Volumes {
				container.VolumeMounts = append(container.VolumeMounts, apiv1.VolumeMount{
					Name:      volName,
					MountPath: volume.MountPath,
				})
			}
		}

		if container.ImagePullPolicy == "" {
			container.ImagePullPolicy = app.Common.Image.PullPolicy
		}

		if app.Staging != "" {
			container.Resources = apiv1.ResourceRequirements{}
		}

		containers = append(containers, container)
	}
	return
}
