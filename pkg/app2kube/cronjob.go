package app2kube

import (
	"fmt"
	"strings"

	batch "k8s.io/api/batch/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// GetCronJobs resource
func (app *App) GetCronJobs() (crons []*batch.CronJob, err error) {
	// Track the final object names so two distinct cron keys that collapse to the
	// same name — via the 52-char cap or lowercasing — surface as an error instead
	// of silently overwriting each other on `kubectl apply` (only the last applied
	// would survive). Maps original cron key -> emitted name for a clear message.
	usedNames := make(map[string]string, len(app.Cronjob))
	// Sorted iteration keeps the rendered cronjob list — and any collision error —
	// stable across runs instead of depending on Go's random map order.
	for _, cronName := range sortedKeys(app.Cronjob) {
		job := app.Cronjob[cronName]
		// The CronJob object name and the default container name must be valid
		// DNS-1123 names, so lowercase the (possibly mixed-case) map key — matching
		// how the sub-containers below are lowercased. A CronJob object name is
		// limited to 52 chars (the controller appends an ~11-char suffix to the
		// 63-char Job name it spawns), stricter than the 253-char subdomain limit
		// other objects use.
		lowerName := strings.ToLower(cronName)
		cronJobName := truncateNameTo(app.GetReleaseName()+"-"+lowerName, MaxCronJobNameLength)

		if other, ok := usedNames[cronJobName]; ok {
			return crons, fmt.Errorf("cronjob name collision: %q and %q both map to %q (shorten one of the cronjob names)", other, cronName, cronJobName)
		}
		usedNames[cronJobName] = cronName

		if job.Schedule == "" {
			return crons, fmt.Errorf("schedule required for cron: %s", cronName)
		}

		if job.FailedJobsHistoryLimit == nil {
			job.FailedJobsHistoryLimit = ptr.To(int32(2))
		}

		if job.SuccessfulJobsHistoryLimit == nil {
			job.SuccessfulJobsHistoryLimit = ptr.To(int32(2))
		}

		if job.ActiveDeadlineSeconds == nil {
			job.ActiveDeadlineSeconds = ptr.To(int64(86400)) // 1 day
		}

		if job.BackoffLimit == nil {
			job.BackoffLimit = ptr.To(int32(6))
		}

		var containers []apiv1.Container
		if len(job.Container.Command) > 0 {
			err := app.processContainer(&job.Container, false)
			if err != nil {
				return crons, err
			}
			if job.Container.Name == "" {
				job.Container.Name = lowerName + "-job"
			}
			containers = append(containers, job.Container)
		}
		// Sorted iteration keeps the rendered container list stable across runs;
		// a map-random order would reroll the cron's pod template on every apply.
		for _, name := range sortedKeys(job.Containers) {
			container := job.Containers[name]
			container.Name = strings.ToLower(name)
			err = app.processContainer(&container, false)
			if err != nil {
				return crons, err
			}
			containers = append(containers, container)
		}

		if job.RestartPolicy == "" {
			job.RestartPolicy = apiv1.RestartPolicyNever
		}

		affinity, err := app.getAffinity()
		if err != nil {
			return nil, err
		}

		// Roll cronjob pods when the config they consume changes (#22), mirroring
		// the Deployment; only the config actually wired into these containers is
		// hashed.
		checksums := app.configChecksumAnnotations(containers)

		// Shared pod-level settings, image pull secrets, grace period and the
		// shared-data/PVC volumes (built from the cron's containers). replicas=1
		// skips the Deployment-only ReadWriteOnce multi-attach warning.
		podSpec := app.commonPodSpec(affinity)
		podSpec.Containers = containers
		podSpec.RestartPolicy = job.RestartPolicy
		podSpec.Volumes = app.podVolumes(1, containers)

		cron := &batch.CronJob{
			ObjectMeta: app.GetObjectMeta(cronJobName),
			Spec: batch.CronJobSpec{
				ConcurrencyPolicy:          job.ConcurrencyPolicy,
				FailedJobsHistoryLimit:     job.FailedJobsHistoryLimit,
				Schedule:                   job.Schedule,
				SuccessfulJobsHistoryLimit: job.SuccessfulJobsHistoryLimit,
				Suspend:                    ptr.To(job.Suspend),
				JobTemplate: batch.JobTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: app.Labels,
					},
					Spec: batch.JobSpec{
						ActiveDeadlineSeconds: job.ActiveDeadlineSeconds,
						BackoffLimit:          job.BackoffLimit,
						Template: apiv1.PodTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{
								// Label cronjob-spawned pods so they match the prune
								// selector, status pod listing and tracking (#12).
								Labels:      app.Labels,
								Annotations: checksums,
							},
							Spec: podSpec,
						},
					},
				},
			},
		}

		if job.TimeZone != "" {
			cron.Spec.TimeZone = ptr.To(job.TimeZone)
		}

		if app.Common.CronjobSuspend {
			cron.Spec.Suspend = ptr.To(true)
		}

		crons = append(crons, cron)
	}
	return crons, nil
}
