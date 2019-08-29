package cmd

import (
	"fmt"
	"time"

	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"

	"github.com/flant/kubedog/pkg/kube"
	"github.com/flant/kubedog/pkg/tracker"
	"github.com/flant/kubedog/pkg/trackers/follow"
	"github.com/flant/kubedog/pkg/trackers/rollout"
)

var (
	logsFromTime = time.Now()
	logsSince    = "now"
	timeout      = 5
)

// NewCmdTrack return track command
func NewCmdTrack() *cobra.Command {
	trackCmd := &cobra.Command{
		Use:   "track",
		Short: "Track application deployment in kubernetes",
	}

	trackCmd.PersistentFlags().StringVarP(&logsSince, "logs-since", "l", logsSince, "A duration like 30s, 5m, or 2h to start log records from the past. 'all' to show all logs and 'now' to display only new records")
	trackCmd.PersistentFlags().IntVarP(&timeout, "timeout", "t", timeout, "Timeout of operation in minutes. 0 is wait forever")

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
		addBlueGreenFlag(cmd)
		cmd.Flags().MarkHidden("include-namespace")
		cmd.Flags().MarkHidden("snapshot")
	}

	return trackCmd
}

func initAppTrack() (*app2kube.App, error) {
	app, err := initApp()
	if err != nil {
		return nil, err
	}

	if app.Namespace == "" {
		app.Namespace = app2kube.NamespaceDefault
	}

	err = kube.Init(kube.InitOptions{
		KubeContext: *kubeConfigFlags.Context,
		KubeConfig:  *kubeConfigFlags.KubeConfig,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to initialize kube: %s", err)
	}

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

	return app, nil
}

func trackFollow(cmd *cobra.Command, args []string) error {
	app, err := initAppTrack()
	if err != nil {
		return err
	}

	cmd.SilenceUsage = true

	return follow.TrackDeployment(
		app.GetDeploymentName(),
		app.Namespace,
		kube.Kubernetes,
		tracker.Options{
			LogsFromTime: logsFromTime,
			Timeout:      time.Minute * time.Duration(timeout),
		},
	)
}

func trackReady(cmd *cobra.Command, args []string) error {
	app, err := initAppTrack()
	if err != nil {
		return err
	}

	cmd.SilenceUsage = true

	err = trackDeploymentTillReady(app.GetDeploymentName(), app.Namespace)
	if err != nil {
		return err
	}

	if len(app.Ingress) > 0 {
		fmt.Println()
		fmt.Println("Try the application URL:")

		for _, ingress := range app.Ingress {
			getURL := func(host, path string) string {
				https := ""
				if ingress.Letsencrypt || ingress.TLSSecretName != "" {
					https = "s"
				}
				return fmt.Sprintf("http%s://%s%s", https, host, path)
			}

			fmt.Println("  ", getURL(ingress.Host, ingress.Path))

			if app.Staging == "" {
				for _, alias := range ingress.Aliases {
					fmt.Println("  ", getURL(alias, ingress.Path))
				}
			}
		}
	}

	return nil
}

func trackDeploymentTillReady(name, namespace string) error {
	err = kube.Init(kube.InitOptions{
		KubeContext: *kubeConfigFlags.Context,
		KubeConfig:  *kubeConfigFlags.KubeConfig,
	})
	if err != nil {
		return fmt.Errorf("unable to initialize kube: %s", err)
	}

	if namespace == "" {
		namespace = app2kube.NamespaceDefault
	}

	err = rollout.TrackDeploymentTillReady(
		name,
		namespace,
		kube.Kubernetes,
		tracker.Options{
			LogsFromTime: logsFromTime,
			Timeout:      time.Minute * time.Duration(timeout),
		},
	)
	if err != nil {
		return err
	}

	return nil
}
