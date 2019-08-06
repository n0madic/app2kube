package main

import (
	"fmt"
	"os"
	"time"

	"github.com/flant/kubedog/pkg/kube"
	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/trackers/follow"
	"github.com/flant/kubedog/pkg/trackers/rollout"
	"github.com/flant/kubedog/pkg/trackers/rollout/multitrack"
	"github.com/spf13/cobra"
	appsv1 "k8s.io/api/apps/v1"
	batch "k8s.io/api/batch/v1beta1"
)

var trackCmd = &cobra.Command{
	Use:               "track",
	Short:             "Track application resources in kubernetes",
	PersistentPreRunE: trackInit,
}

var (
	deployment  *appsv1.Deployment
	jobs        []*batch.CronJob
	kubeContext string
	kubeConfig  string
	timeout     int
)

func init() {
	trackCmd.PersistentFlags().StringVarP(&kubeConfig, "kube-config", "", os.Getenv("KUBECONFIG"), "Path to the kubeconfig file (can be set with $KUBECONFIG)")
	trackCmd.PersistentFlags().StringVarP(&kubeContext, "kube-context", "", os.Getenv("KUBECONTEXT"), "The name of the kubeconfig context to use (can be set with $KUBECONTEXT)")
	trackCmd.PersistentFlags().IntVarP(&timeout, "timeout", "t", 5, "Timeout of operation in minutes. 0 is wait forever")

	trackCmd.AddCommand(&cobra.Command{
		Use:   "follow",
		Short: "Follow Deployment",
		RunE:  trackFollow,
	})

	trackCmd.AddCommand(&cobra.Command{
		Use:   "ready",
		Short: "Track Deployment till ready",
		RunE:  trackReady,
	})

	trackCmd.AddCommand(&cobra.Command{
		Use:   "multiple",
		Short: "Track multiple resources (Deployment/CronJobs)",
		RunE:  trackMulti,
	})

	for _, cmd := range trackCmd.Commands() {
		initAppFlags(cmd)
	}

	rootCmd.AddCommand(trackCmd)
}

func trackInit(cmd *cobra.Command, args []string) error {
	err := initApp()
	if err != nil {
		return err
	}

	cmd.SilenceUsage = true

	err = kube.Init(kube.InitOptions{KubeContext: kubeContext, KubeConfig: kubeConfig})
	if err != nil {
		return fmt.Errorf("unable to initialize kube: %s", err)
	}
	return nil
}

func trackFollow(cmd *cobra.Command, args []string) error {
	err := follow.TrackDeployment(
		app.GetReleaseName(),
		app.Namespace,
		kube.Kubernetes,
		tracker.Options{
			Timeout: time.Minute * time.Duration(timeout),
		},
	)
	if err != nil {
		return err
	}

	return nil
}

func trackReady(cmd *cobra.Command, args []string) error {
	err := rollout.TrackDeploymentTillReady(
		app.GetReleaseName(),
		app.Namespace,
		kube.Kubernetes,
		tracker.Options{
			Timeout: time.Minute * time.Duration(timeout),
		},
	)
	if err != nil {
		return err
	}
	return nil
}

func trackMulti(cmd *cobra.Command, args []string) error {
	specs := multitrack.MultitrackSpecs{}

	jobs, err := app.GetCronJobs()
	if err != nil {
		return err
	}
	for _, cron := range jobs {
		specs.Jobs = append(specs.Jobs, multitrack.MultitrackSpec{
			ResourceName: cron.Name,
			Namespace:    cron.Namespace,
		})
	}

	deployment, err := app.GetDeployment()
	if err != nil {
		return err
	}
	if deployment != nil {
		specs.Deployments = []multitrack.MultitrackSpec{
			multitrack.MultitrackSpec{
				ResourceName: deployment.Name,
				Namespace:    deployment.Namespace},
		}
	} else {
		return fmt.Errorf("deployment not specified")
	}

	err = multitrack.Multitrack(
		kube.Kubernetes,
		specs,
		multitrack.MultitrackOptions{
			Options: tracker.Options{
				Timeout: time.Minute * time.Duration(timeout),
			},
		},
	)

	if err != nil {
		return fmt.Errorf("resources are not reached ready state: %s", err)
	}

	return nil
}