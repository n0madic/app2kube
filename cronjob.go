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
		cronJobName := app.Name + "-" + cronName
		if job.Image == "" {
			job.Image = app.Common.Image.Repository + ":" + app.Common.Image.Tag
		}
		if job.ImagePullPolicy == "" {
			job.ImagePullPolicy = app.Common.Image.PullPolicy
		}
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
								Containers: []apiv1.Container{
									apiv1.Container{
										Name:            cronName + "-job",
										Command:         job.Command,
										Args:            job.Args,
										Image:           job.Image,
										ImagePullPolicy: job.ImagePullPolicy,
										Resources:       job.Resources,
									},
								},
								DNSPolicy:          app.Common.DNSPolicy,
								EnableServiceLinks: &app.Common.EnableServiceLinks,
								ImagePullSecrets: []apiv1.LocalObjectReference{
									apiv1.LocalObjectReference{
										Name: app.Common.Image.PullSecrets,
									},
								},
								NodeSelector:                  app.Common.NodeSelector,
								TerminationGracePeriodSeconds: &app.Common.GracePeriod,
								Tolerations:                   app.Common.Tolerations,
							},
						},
					},
				},
			},
		}
		for key, value := range app.Configmap {
			cron.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Env = append(
				cron.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Env,
				apiv1.EnvVar{Name: key, Value: value},
			)
		}
		if len(app.Secrets) > 0 {
			cron.Spec.JobTemplate.Spec.Template.Spec.Containers[0].EnvFrom = append(
				cron.Spec.JobTemplate.Spec.Template.Spec.Containers[0].EnvFrom,
				apiv1.EnvFromSource{SecretRef: &apiv1.SecretEnvSource{LocalObjectReference: apiv1.LocalObjectReference{
					Name: app.Name,
				}}},
			)
		}
		yaml = yaml + getYAML("CronJob: "+cronJobName, cron)
	}
	return
}
