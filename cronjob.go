package app2kube

import (
	batchv1 "k8s.io/api/batch/v1"
	batch "k8s.io/api/batch/v1beta1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetCronJobs YAML
func (app *App) GetCronJobs() (yaml string) {
	for cronName, job := range app.Cronjob {
		cronJobName := app.GetReleaseName() + "-" + cronName

		cron := &batch.CronJob{
			ObjectMeta: app.GetObjectMeta(cronJobName),
			Spec: batch.CronJobSpec{
				ConcurrencyPolicy:          job.ConcurrencyPolicy,
				FailedJobsHistoryLimit:     &job.FailedJobsHistoryLimit,
				Schedule:                   job.Schedule,
				SuccessfulJobsHistoryLimit: &job.SuccessfulJobsHistoryLimit,
				Suspend:                    &app.Common.CronjobSuspend,
				JobTemplate: batch.JobTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: app.Labels,
					},
					Spec: batchv1.JobSpec{
						Template: apiv1.PodTemplateSpec{
							Spec: apiv1.PodSpec{
								AutomountServiceAccountToken: &app.Common.MountServiceAccountToken,
								Containers:                   app.processContainers(job.Containers),
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

		if app.Common.Image.PullSecrets != "" {
			cron.Spec.JobTemplate.Spec.Template.Spec.ImagePullSecrets = []apiv1.LocalObjectReference{
				apiv1.LocalObjectReference{
					Name: app.Common.Image.PullSecrets,
				},
			}
		}

		if app.Common.GracePeriod > 0 {
			cron.Spec.JobTemplate.Spec.Template.Spec.TerminationGracePeriodSeconds = &app.Common.GracePeriod
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

		yaml = yaml + getYAML("CronJob: "+cronJobName, cron)
	}
	return
}
