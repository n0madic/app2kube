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

		if job.FailedJobsHistoryLimit == 0 {
			job.FailedJobsHistoryLimit = 2
		}

		if job.SuccessfulJobsHistoryLimit == 0 {
			job.SuccessfulJobsHistoryLimit = 2
		}

		if job.ActiveDeadlineSeconds == 0 {
			job.ActiveDeadlineSeconds = 86400 // 1 day
		}

		if job.BackoffLimit == 0 {
			job.BackoffLimit = 6
		}

		var containers []apiv1.Container
		if len(job.Container.Command) > 0 {
			err := app.processContainer(&job.Container)
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
			err = app.processContainer(&container)
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
				FailedJobsHistoryLimit:     ptr.To(job.FailedJobsHistoryLimit),
				Schedule:                   job.Schedule,
				SuccessfulJobsHistoryLimit: ptr.To(job.SuccessfulJobsHistoryLimit),
				Suspend:                    ptr.To(job.Suspend),
				JobTemplate: batch.JobTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: app.Labels,
					},
					Spec: batch.JobSpec{
						ActiveDeadlineSeconds: ptr.To(job.ActiveDeadlineSeconds),
						BackoffLimit:          ptr.To(job.BackoffLimit),
						Template: apiv1.PodTemplateSpec{
							Spec: apiv1.PodSpec{
								Affinity:                     affinity,
								AutomountServiceAccountToken: &app.Common.MountServiceAccountToken,
								Containers:                   containers,
								DNSPolicy:                    app.Common.DNSPolicy,
								RestartPolicy:                job.RestartPolicy,
								EnableServiceLinks:           &app.Common.EnableServiceLinks,
								NodeSelector:                 app.Common.NodeSelector,
								Tolerations:                  app.Common.Tolerations,
							},
						},
					},
				},
			},
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

		if app.Common.SharedData != "" && len(cron.Spec.JobTemplate.Spec.Template.Spec.Containers) > 1 {
			cron.Spec.JobTemplate.Spec.Template.Spec.Volumes = append(cron.Spec.JobTemplate.Spec.Template.Spec.Volumes, apiv1.Volume{
				Name:         "shared-data",
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
