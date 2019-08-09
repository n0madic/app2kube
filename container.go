package app2kube

import (
	"fmt"
	"reflect"
	"strings"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func (app *App) processContainer(container *apiv1.Container) error {
	if container.Image == "" {
		if app.Common.Image.Repository != "" {
			container.Image = app.Common.Image.Repository + ":" + app.Common.Image.Tag
		} else {
			return fmt.Errorf("image required for container %s", container.Name)
		}
	}

	thirdpartyImage := false
	if app.Common.Image.Repository != "" {
		imageParts := strings.Split(container.Image, ":")
		if len(imageParts) == 2 && imageParts[0] == app.Common.Image.Repository {
			container.Image = imageParts[0] + ":" + app.Common.Image.Tag
		} else {
			thirdpartyImage = true
		}
	}

	if !thirdpartyImage {
		for key, value := range app.Env {
			container.Env = append(container.Env, apiv1.EnvVar{Name: key, Value: value})
		}

		if len(app.ConfigMap) > 0 {
			container.EnvFrom = append(
				container.EnvFrom,
				apiv1.EnvFromSource{ConfigMapRef: &apiv1.ConfigMapEnvSource{LocalObjectReference: apiv1.LocalObjectReference{
					Name: app.GetReleaseName(),
				}}},
			)
		}

		if len(app.Secrets) > 0 {
			container.EnvFrom = append(
				container.EnvFrom,
				apiv1.EnvFromSource{SecretRef: &apiv1.SecretEnvSource{LocalObjectReference: apiv1.LocalObjectReference{
					Name: app.GetReleaseName(),
				}}},
			)
		}

		if app.Common.SharedData != "" {
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

	if len(container.Ports) > 0 {
		// Automatic creation of a service for ingress if one is not specified
		if len(app.Service) == 0 && len(app.Ingress) > 0 {
			app.Service = map[string]Service{}
			for _, port := range container.Ports {
				if port.Name != "" {
					app.Service[port.Name] = Service{
						Port:     port.ContainerPort,
						Protocol: port.Protocol,
					}
				}
			}
			if len(app.Service) == 0 {
				return fmt.Errorf("Named container port required to create service for container: %s", container.Name)
			}
		}
		// Add LivenessProbe to container port if probe not specified
		if reflect.ValueOf(container.LivenessProbe).IsNil() && len(container.Ports) == 1 {
			container.LivenessProbe = &apiv1.Probe{
				Handler: apiv1.Handler{
					TCPSocket: &apiv1.TCPSocketAction{
						Port: intstr.IntOrString{Type: intstr.Int, IntVal: container.Ports[0].ContainerPort},
					},
				},
				InitialDelaySeconds: 5,
			}
		}

	}

	return nil
}
