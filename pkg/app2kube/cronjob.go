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
	for cronName, job := range app.Cronjob {
		cronJobName := app.GetReleaseName() + "-" + cronName

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
				job.Container.Name = cronName + "-job"
			}
			containers = append(containers, job.Container)
		}
		for name, container := range job.Containers {
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
								Labels: app.Labels,
							},
							Spec: apiv1.PodSpec{
								Affinity:                     affinity,
								AutomountServiceAccountToken: &app.Common.MountServiceAccountToken,
								Containers:                   containers,
								DNSPolicy:                    app.Common.DNSPolicy,
								RestartPolicy:                job.RestartPolicy,
								EnableServiceLinks:           &app.Common.EnableServiceLinks,
								NodeSelector:                 app.Common.NodeSelector,
								SecurityContext:              app.podSecurityContext(),
								Tolerations:                  app.Common.Tolerations,
							},
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

		if app.Common.Image.PullSecrets != "" {
			cron.Spec.JobTemplate.Spec.Template.Spec.ImagePullSecrets = []apiv1.LocalObjectReference{{
				Name: app.Common.Image.PullSecrets,
			}}
		}

		if app.Common.GracePeriod > 0 {
			cron.Spec.JobTemplate.Spec.Template.Spec.TerminationGracePeriodSeconds = ptr.To(app.Common.GracePeriod)
		}

		// The EmptyDir volume must exist whenever SharedData is set, to match the
		// mount processContainer adds to every app-image container — even a single
		// one — otherwise the pod spec references a missing volume (#18).
		if app.Common.SharedData != "" {
			cron.Spec.JobTemplate.Spec.Template.Spec.Volumes = append(cron.Spec.JobTemplate.Spec.Template.Spec.Volumes, apiv1.Volume{
				Name:         sharedDataVolumeName,
				VolumeSource: apiv1.VolumeSource{EmptyDir: &apiv1.EmptyDirVolumeSource{}},
			})
		}

		for volName := range app.Volumes {
			cron.Spec.JobTemplate.Spec.Template.Spec.Volumes = append(cron.Spec.JobTemplate.Spec.Template.Spec.Volumes, apiv1.Volume{
				Name: volName,
				VolumeSource: apiv1.VolumeSource{
					PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{
						ClaimName: app.GetReleaseName() + "-" + volName,
					},
				},
			})
		}

		crons = append(crons, cron)
	}
	return crons, nil
}
