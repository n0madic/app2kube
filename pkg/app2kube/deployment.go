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
		// Deep-copy so the Deployment and every CronJob pod spec get independent
		// PodSecurityContext values instead of aliasing the one the user supplied.
		return app.Common.SecurityContext.DeepCopy()
	}
	return &apiv1.PodSecurityContext{
		SeccompProfile: &apiv1.SeccompProfile{Type: apiv1.SeccompProfileTypeRuntimeDefault},
	}
}

// commonPodSpec builds the PodSpec fields shared by the Deployment and every
// CronJob pod template: the pod-level scheduling/security settings, image pull
// secrets and termination grace period. The caller fills in
// Containers/InitContainers, RestartPolicy and Volumes.
func (app *App) commonPodSpec(affinity *apiv1.Affinity) apiv1.PodSpec {
	spec := apiv1.PodSpec{
		Affinity:                     affinity,
		AutomountServiceAccountToken: ptr.To(app.Common.MountServiceAccountToken),
		DNSPolicy:                    app.Common.DNSPolicy,
		EnableServiceLinks:           ptr.To(app.Common.EnableServiceLinks),
		NodeSelector:                 app.Common.NodeSelector,
		SecurityContext:              app.podSecurityContext(),
		ServiceAccountName:           app.Common.ServiceAccountName,
		Tolerations:                  app.Common.Tolerations,
	}
	if app.Common.Image.PullSecrets != "" {
		spec.ImagePullSecrets = []apiv1.LocalObjectReference{{Name: app.Common.Image.PullSecrets}}
	}
	if app.Common.GracePeriod > 0 {
		spec.TerminationGracePeriodSeconds = ptr.To(app.Common.GracePeriod)
	}
	return spec
}

// podVolumes builds the pod volume list shared by the Deployment and CronJob pod
// templates: the shared-data EmptyDir (emitted whenever SharedData is set, to
// match the mount processContainer adds to every app-image container) plus a PVC
// volume for each app.Volume actually mounted by one of the given containers — a
// workload built entirely from third-party images mounts none and must not carry
// a dangling volume (#18). Volumes are emitted in sorted order so the rendered
// list is stable across runs. When replicas>1 it warns about ReadWriteOnce
// volumes that cannot be shared across nodes (#48); CronJob pods pass replicas=1
// to skip that Deployment-only check.
func (app *App) podVolumes(replicas int32, containerGroups ...[]apiv1.Container) []apiv1.Volume {
	var volumes []apiv1.Volume
	if app.Common.SharedData != "" {
		volumes = append(volumes, apiv1.Volume{
			Name:         sharedDataVolumeName,
			VolumeSource: apiv1.VolumeSource{EmptyDir: &apiv1.EmptyDirVolumeSource{}},
		})
	}

	mounted := make(map[string]bool)
	for _, group := range containerGroups {
		for _, c := range group {
			for _, vm := range c.VolumeMounts {
				mounted[vm.Name] = true
			}
		}
	}
	for _, volName := range sortedKeys(app.Volumes) {
		if !mounted[volName] {
			continue
		}
		vol := app.Volumes[volName]
		// A ReadWriteOnce(-only) PVC can be bound on a single node; mounting it
		// into a multi-replica Deployment makes pods on other nodes unschedulable
		// (a StatefulSet would be correct but is out of scope). Warn instead of
		// silently emitting a spec that deadlocks (#48).
		if replicas > 1 && len(vol.Spec.AccessModes) > 0 && !pvcAllowsMultiAttach(vol.Spec.AccessModes) {
			fmt.Fprintf(os.Stderr, "WARNING: PVC %q (%v) is mounted into a %d-replica Deployment; pods on different nodes cannot share a ReadWriteOnce volume and scheduling will block (use a single replica or a ReadWriteMany volume; StatefulSet is out of scope)\n", volName, vol.Spec.AccessModes, replicas)
		}
		volumes = append(volumes, apiv1.Volume{
			Name: volName,
			VolumeSource: apiv1.VolumeSource{
				PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{
					ClaimName: app.GetVolumeClaimName(volName),
				},
			},
		})
	}
	return volumes
}

// GetDeployment resource
func (app *App) GetDeployment() (deployment *appsv1.Deployment, err error) {
	if len(app.Deployment.Containers) > 0 {
		// A mutable :latest common image tag in a non-staging deploy is not
		// reproducible (and relies on the pull policy to refresh cached nodes);
		// warn so the operator can pin a specific tag or digest (#45).
		if !app.Staging.Active && app.Common.Image.Repository != "" && app.Common.Image.Tag == "latest" {
			fmt.Fprintf(os.Stderr, "WARNING: image %s:latest is a mutable tag; the deploy is not reproducible — pin a specific tag or digest\n", app.Common.Image.Repository)
		}

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

		// Iterate in sorted key order so the rendered container list is stable
		// across runs; an unsorted (map-random) order would change the pod
		// template on every render and roll the Deployment on each apply.
		var containers []apiv1.Container
		for _, name := range sortedKeys(app.Deployment.Containers) {
			container := app.Deployment.Containers[name]
			container.Name = strings.ToLower(name)
			err = app.processContainer(&container, false)
			if err != nil {
				return
			}
			containers = append(containers, container)
		}

		var initContainers []apiv1.Container
		for _, name := range sortedKeys(app.Deployment.InitContainers) {
			icontainer := app.Deployment.InitContainers[name]
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

		// Bound a wedged rollout so `kubectl rollout status`/kubedog reports
		// failure instead of hanging (#46). Defaults to 15 minutes (900s) to match
		// app2kube's default track timeout (cmd.defaultTrackTimeout), so the
		// Deployment's own progress deadline and `apply --track`/blue-green agree
		// on when a rollout has failed instead of one firing before the other.
		progressDeadline := app.Deployment.ProgressDeadlineSeconds
		if progressDeadline == nil {
			progressDeadline = ptr.To(int32(15 * 60))
		}

		// Shared pod-level settings, image pull secrets and grace period; the
		// deployment-specific container/init/volume fields are filled in below.
		// processContainer mounts shared-data and app.Volumes only on app-image
		// containers (main and init), so podVolumes builds the matching volume set.
		podSpec := app.commonPodSpec(affinity)
		podSpec.Containers = containers
		podSpec.InitContainers = initContainers
		podSpec.Volumes = app.podVolumes(replicas, containers, initContainers)

		deployment = &appsv1.Deployment{
			ObjectMeta: app.GetObjectMeta(app.GetDeploymentName()),
			Spec: appsv1.DeploymentSpec{
				Replicas:                ptr.To(replicas),
				RevisionHistoryLimit:    ptr.To(app.Deployment.RevisionHistoryLimit),
				ProgressDeadlineSeconds: progressDeadline,
				// The selector carries the full label set (GetColorLabels), matching
				// the pod template and the pre-v0.7 selector. spec.selector is
				// immutable, so keeping it byte-identical to what earlier releases
				// emitted lets `kubectl apply` upgrade Deployments created by older
				// app2kube versions in place instead of being rejected with
				// "field is immutable".
				Selector: &metav1.LabelSelector{
					MatchLabels: app.GetColorLabels(),
				},
				// Use the user's strategy verbatim; when unset, leave it empty so
				// the apiserver applies its built-in RollingUpdate default
				// (maxUnavailable/maxSurge 25%) rather than app2kube forcing
				// maxUnavailable:0 — which, combined with an auto readiness probe,
				// could wedge the rollout of an app slow to accept connections.
				Strategy: app.Deployment.Strategy,
				Template: apiv1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels:      app.GetColorLabels(),
						Annotations: checksums,
					},
					Spec: podSpec,
				},
			},
		}

		if app.Deployment.BlueGreenColor != "" {
			deployment.Labels = app.GetColorLabels()
		}
	}
	return
}
