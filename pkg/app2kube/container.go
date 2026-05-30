package app2kube

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// processContainer fills in defaults and injected configuration for a container.
// When isInit is true the container is an init container: the auto-service
// derivation and Liveness/Readiness probe injection are skipped, because the
// Kubernetes API rejects probes on non-sidecar init containers and an init
// container port must never drive the app's Service. Env/EnvFrom and volume
// mounts are still applied (init containers legitimately use them).
func (app *App) processContainer(container *apiv1.Container, isInit bool) error {
	if container.Image == "" {
		if app.Common.Image.Repository != "" {
			container.Image = app.Common.Image.Repository + ":" + app.Common.Image.Tag
		} else {
			return fmt.Errorf("image required for container %s", container.Name)
		}
	}

	thirdpartyImage := false
	if app.Common.Image.Repository != "" {
		repo := app.Common.Image.Repository
		// The image belongs to the app when it is exactly the repository or the
		// repository followed by a tag (":") or a digest ("@"). Splitting on ":"
		// would misclassify repositories that contain a registry port (e.g.
		// registry.io:5000/app) or digests (repo@sha256:...) as third-party.
		if container.Image == repo ||
			strings.HasPrefix(container.Image, repo+":") ||
			strings.HasPrefix(container.Image, repo+"@") {
			container.Image = repo + ":" + app.Common.Image.Tag
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

	if !isInit && len(container.Ports) > 0 {
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
				return fmt.Errorf("named container port required to create service for container: %s", container.Name)
			}
		}

		if len(container.Ports) == 1 {
			containerPort := intstr.IntOrString{Type: intstr.Int, IntVal: container.Ports[0].ContainerPort}

			// Add LivenessProbe to container port if probe not specified
			if reflect.ValueOf(container.LivenessProbe).IsNil() {
				container.LivenessProbe = &apiv1.Probe{
					ProbeHandler: apiv1.ProbeHandler{
						TCPSocket: &apiv1.TCPSocketAction{
							Port: containerPort,
						},
					},
					InitialDelaySeconds: 5,
				}
			} else {
				// Add missing port to LivenessProbe
				if !reflect.ValueOf(container.LivenessProbe.TCPSocket).IsNil() && portIsUnset(container.LivenessProbe.TCPSocket.Port) {
					container.LivenessProbe.TCPSocket.Port = containerPort
				}
				if !reflect.ValueOf(container.LivenessProbe.HTTPGet).IsNil() && portIsUnset(container.LivenessProbe.HTTPGet.Port) {
					container.LivenessProbe.HTTPGet.Port = containerPort
				}
			}

			// Add missing port to ReadinessProbe
			if !reflect.ValueOf(container.ReadinessProbe).IsNil() && !reflect.ValueOf(container.ReadinessProbe.HTTPGet).IsNil() {
				if portIsUnset(container.ReadinessProbe.HTTPGet.Port) {
					container.ReadinessProbe.HTTPGet.Port = containerPort
				}
			}

		}
	}

	return nil
}

// portIsUnset reports whether an IntOrString probe port carries no value, i.e.
// it is neither a numeric port nor a named (string) port. A named port has a
// non-empty StrVal and must not be overwritten with the numeric container port.
func portIsUnset(p intstr.IntOrString) bool {
	return p.IntVal == 0 && p.StrVal == ""
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
