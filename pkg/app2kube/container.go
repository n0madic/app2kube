package app2kube

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
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
		// Let an explicit container-level env var win over the global app.Env of
		// the same name: skip colliding keys so the manifest carries no duplicate
		// (Kubernetes would otherwise resolve to the last, silently letting the
		// global value override the specific one).
		declared := make(map[string]bool, len(container.Env))
		for _, e := range container.Env {
			declared[e.Name] = true
		}
		for _, key := range sortedKeys(app.Env) {
			if declared[key] {
				continue
			}
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
				Name:      sharedDataVolumeName,
				MountPath: app.Common.SharedData,
			})
		}

		for volName, volume := range app.Volumes {
			container.VolumeMounts = append(container.VolumeMounts, apiv1.VolumeMount{
				Name:      volName,
				MountPath: volume.MountPath,
			})
		}

		// Apply the opt-in common.resources baseline to app-image containers
		// that declare none. Skipped in staging, where resources are stripped
		// below regardless.
		if app.Staging == "" && app.Common.Resources != nil &&
			len(container.Resources.Requests) == 0 && len(container.Resources.Limits) == 0 {
			// Deep-copy so each container gets its own Requests/Limits maps instead
			// of aliasing the shared app.Common.Resources (and each other).
			container.Resources = *app.Common.Resources.DeepCopy()
		}

		// Emit a conservative, non-breaking container securityContext default for
		// app-image containers when the user set none: only disable privilege
		// escalation. Capabilities are deliberately NOT dropped — dropping ALL
		// breaks common workloads that legitimately rely on default Linux
		// capabilities (a root nginx binding :80 needs NET_BIND_SERVICE, and its
		// master needs SETUID/SETGID to spawn workers), and no single minimal
		// add-set covers every app image. runAsNonRoot / readOnlyRootFilesystem
		// are likewise left to explicit config.
		if container.SecurityContext == nil {
			container.SecurityContext = &apiv1.SecurityContext{
				AllowPrivilegeEscalation: ptr.To(false),
			}
		}
	}

	if container.ImagePullPolicy == "" {
		container.ImagePullPolicy = app.Common.Image.PullPolicy
	}
	if container.ImagePullPolicy == "" {
		// Set the pull policy explicitly so the deploy is reproducible instead of
		// depending on Kubernetes' version-specific implicit rule (#45).
		container.ImagePullPolicy = defaultPullPolicy(container.Image)
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

			// Add a ReadinessProbe mirroring the liveness target when none is
			// configured, so traffic is routed only once the port accepts
			// connections. Scoped to app-image containers (like the
			// securityContext/resources defaults): an auto readiness probe on a
			// third-party sidecar could fail and make the whole pod NotReady,
			// blocking traffic to the main app. A user-provided readiness probe
			// still gets a missing HTTPGet port filled, for any container.
			if reflect.ValueOf(container.ReadinessProbe).IsNil() {
				if !thirdpartyImage {
					container.ReadinessProbe = &apiv1.Probe{
						ProbeHandler: apiv1.ProbeHandler{
							TCPSocket: &apiv1.TCPSocketAction{
								Port: containerPort,
							},
						},
					}
				}
			} else if !reflect.ValueOf(container.ReadinessProbe.HTTPGet).IsNil() && portIsUnset(container.ReadinessProbe.HTTPGet.Port) {
				container.ReadinessProbe.HTTPGet.Port = containerPort
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

// defaultPullPolicy mirrors Kubernetes' implicit pull-policy rule but sets it
// explicitly so a deploy is reproducible: an image tagged :latest, with no tag,
// or otherwise mutable defaults to Always; a fixed tag or a digest-pinned image
// (@sha256:...) defaults to IfNotPresent (#45).
func defaultPullPolicy(image string) apiv1.PullPolicy {
	if strings.Contains(image, "@") {
		return apiv1.PullIfNotPresent // digest-pinned: immutable
	}
	name := image
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:] // strip registry host[:port]/ so a port isn't read as a tag
	}
	tag := ""
	if i := strings.LastIndex(name, ":"); i >= 0 {
		tag = name[i+1:]
	}
	if tag == "" || tag == "latest" {
		return apiv1.PullAlways
	}
	return apiv1.PullIfNotPresent
}
