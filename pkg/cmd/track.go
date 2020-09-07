package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/werf/kubedog/pkg/kube"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/trackers/follow"
	"github.com/werf/kubedog/pkg/trackers/rollout"
)

var (
	logsFromTime = time.Now()
	logsSince    = "now"
	trackTimeout = 5
)

// NewCmdTrack return track command
func NewCmdTrack() *cobra.Command {
	trackCmd := &cobra.Command{
		Use:   "track",
		Short: "Track application deployment in kubernetes",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
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
		},
	}

	trackCmd.PersistentFlags().StringVarP(&logsSince, "logs-since", "l", logsSince, "A duration like 30s, 5m, or 2h to start log records from the past. 'all' to show all logs and 'now' to display only new records")
	trackCmd.PersistentFlags().IntVarP(&trackTimeout, "timeout", "t", trackTimeout, "Timeout of operation in minutes. 0 is wait forever")

	trackCmd.AddCommand(&cobra.Command{
		Use:   "follow",
		Short: "Follow Deployment",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := initApp()
			if err != nil {
				return err
			}
			cmd.SilenceUsage = true
			return trackFollow(app.GetDeploymentName(), app.Namespace)
		},
	})

	trackCmd.AddCommand(&cobra.Command{
		Use:   "ready",
		Short: "Track Deployment till ready",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := initApp()
			if err != nil {
				return err
			}
			cmd.SilenceUsage = true
			return trackReady(app.GetDeploymentName(), app.Namespace)
		},
	})

	for _, cmd := range trackCmd.Commands() {
		addAppFlags(cmd)
		addBlueGreenFlag(cmd)
		cmd.Flags().MarkHidden("include-namespace")
		cmd.Flags().MarkHidden("snapshot")
	}

	return trackCmd
}

func trackFollow(name, namespace string) error {
	err := kube.Init(kube.InitOptions{KubeConfigOptions: kube.KubeConfigOptions{
		Context:    *kubeConfigFlags.Context,
		ConfigPath: *kubeConfigFlags.KubeConfig,
	}})
	if err != nil {
		return fmt.Errorf("unable to initialize kubedog: %s", err)
	}

	return follow.TrackDeployment(
		name,
		namespace,
		kube.Kubernetes,
		tracker.Options{
			LogsFromTime: logsFromTime,
			Timeout:      time.Minute * time.Duration(trackTimeout),
		},
	)
}

func trackReady(name, namespace string) error {
	err := kube.Init(kube.InitOptions{KubeConfigOptions: kube.KubeConfigOptions{
		Context:    *kubeConfigFlags.Context,
		ConfigPath: *kubeConfigFlags.KubeConfig,
	}})
	if err != nil {
		return fmt.Errorf("unable to initialize kubedog: %s", err)
	}

	err = rollout.TrackDeploymentTillReady(
		name,
		namespace,
		kube.Kubernetes,
		tracker.Options{
			LogsFromTime: logsFromTime,
			Timeout:      time.Minute * time.Duration(trackTimeout),
		},
	)
	if err != nil {
		return err
	}

	return nil
}
