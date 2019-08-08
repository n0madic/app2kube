package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	apiv1 "k8s.io/api/core/v1"

	"github.com/flant/kubedog/pkg/kube"
	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/trackers/follow"
	"github.com/flant/kubedog/pkg/trackers/rollout"
)

var (
	logsFromTime time.Time
	logsSince    string
	timeout      int
)

func init() {
	trackCmd := &cobra.Command{
		Use:               "track",
		Short:             "Track application deployment in kubernetes",
		PersistentPreRunE: trackInit,
	}

	trackCmd.PersistentFlags().StringVarP(&logsSince, "logs-since", "l", "now", "A duration like 30s, 5m, or 2h to start log records from the past. 'all' to show all logs and 'now' to display only new records")
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

	for _, cmd := range trackCmd.Commands() {
		addAppFlags(cmd)
	}

	rootCmd.AddCommand(trackCmd)
}

func trackInit(cmd *cobra.Command, args []string) error {
	err := initApp()
	if err != nil {
		return err
	}

	if app.Namespace == "" {
		app.Namespace = apiv1.NamespaceDefault
	}

	cmd.SilenceUsage = true

	err = kube.Init(kube.InitOptions{
		KubeContext: *kubeConfigFlags.Context,
		KubeConfig:  *kubeConfigFlags.KubeConfig,
	})
	if err != nil {
		return fmt.Errorf("unable to initialize kube: %s", err)
	}

	logsFromTime = time.Now()
	if logsSince != "now" {
		if logsSince == "all" {
			logsFromTime = time.Time{}
		} else {
			since, err := time.ParseDuration(logsSince)
			if err == nil {
				logsFromTime = time.Now().Add(-since)
			}
		}
	}

	return nil
}

func trackFollow(cmd *cobra.Command, args []string) error {
	return follow.TrackDeployment(
		app.GetReleaseName(),
		app.Namespace,
		kube.Kubernetes,
		tracker.Options{
			LogsFromTime: logsFromTime,
			Timeout:      time.Minute * time.Duration(timeout),
		},
	)
}

func trackReady(cmd *cobra.Command, args []string) error {
	err := rollout.TrackDeploymentTillReady(
		app.GetReleaseName(),
		app.Namespace,
		kube.Kubernetes,
		tracker.Options{
			LogsFromTime: logsFromTime,
			Timeout:      time.Minute * time.Duration(timeout),
		},
	)
	if err != nil {
		return err
	}

	if len(app.Deployment.Ingress) > 0 {
		fmt.Println()
		fmt.Println("Try the application URL:")

		for _, ingress := range app.Deployment.Ingress {
			getURL := func(host string) string {
				https := ""
				if ingress.Letsencrypt || ingress.TLSSecretName != "" {
					https = "s"
				}
				return fmt.Sprintf("http%s://%s", https, host)
			}

			fmt.Println("  ", getURL(ingress.Host))

			for _, alias := range ingress.Aliases {
				fmt.Println("  ", getURL(alias))
			}
		}
	}

	return nil
}
