package app2kube

import (
	"fmt"
	"reflect"
	"sort"
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
		for _, key := range sortedKeys(app.Env) {
			container.Env = append(container.Env, apiv1.EnvVar{Name: key, Value: app.Env[key]})
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
		if len(app.Service) == 0 && len(app.Ingress) > 0 && len(app.Deployment.Containers) == 1 {
			app.Service = map[string]Service{}
			for _, port := range container.Ports {
				if port.Name != "" {
					if _, ok := app.Service[port.Name]; ok {
						return fmt.Errorf("container port names must be different: %s", container.Name)
					}
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

		if len(container.Ports) == 1 {
			containerPort := intstr.IntOrString{Type: intstr.Int, IntVal: container.Ports[0].ContainerPort}

			// Add LivenessProbe to container port if probe not specified
			if reflect.ValueOf(container.LivenessProbe).IsNil() {
				container.LivenessProbe = &apiv1.Probe{
					Handler: apiv1.Handler{
						TCPSocket: &apiv1.TCPSocketAction{
							Port: containerPort,
						},
					},
					InitialDelaySeconds: 5,
				}
			} else {
				// Add missing port to LivenessProbe
				if !reflect.ValueOf(container.LivenessProbe.HTTPGet).IsNil() && container.LivenessProbe.HTTPGet.Port.IntVal == 0 {
					container.LivenessProbe.HTTPGet.Port = containerPort
				}
			}

			// Add missing port to ReadinessProbe
			if !reflect.ValueOf(container.ReadinessProbe).IsNil() && !reflect.ValueOf(container.ReadinessProbe.HTTPGet).IsNil() {
				if container.ReadinessProbe.HTTPGet.Port.IntVal == 0 {
					container.ReadinessProbe.HTTPGet.Port = containerPort
				}
			}

		}
	}

	return nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, len(m))
	i := 0
	for k := range m {
		keys[i] = k
		i++
	}
	sort.Strings(keys)
	return keys
}
