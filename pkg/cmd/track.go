package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/werf/kubedog/pkg/kube"
	"github.com/werf/kubedog/pkg/tracker"
	"github.com/werf/kubedog/pkg/trackers/follow"
	"github.com/werf/kubedog/pkg/trackers/rollout/multitrack"
)

var (
	logsFromTime = time.Now()
	logsSince    = "now"
	trackTimeout = 15
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

	// addTrackSub wires a track subcommand with its own appOptions so no
	// state is shared between commands. run receives the resolved deployment
	// name and namespace (trackFollow/trackReady match this signature).
	addTrackSub := func(use, short string, run func(name, namespace string) error) {
		c := &cobra.Command{Use: use, Short: short}
		opts := addAppFlags(c)
		addBlueGreenFlag(c)
		_ = c.Flags().MarkHidden("include-namespace")
		_ = c.Flags().MarkHidden("snapshot")
		c.RunE = func(cmd *cobra.Command, args []string) error {
			app, err := opts.initApp()
			if err != nil {
				return err
			}
			cmd.SilenceUsage = true
			return run(app.GetDeploymentName(), app.Namespace)
		}
		trackCmd.AddCommand(c)
	}

	addTrackSub("follow", "Follow Deployment", trackFollow)
	addTrackSub("ready", "Track Deployment till ready", trackReady)

	return trackCmd
}

func kubedogInit() error {
	var kubeConfigPathMergeList []string
	if v := os.Getenv("KUBECONFIG"); v != "" {
		kubeConfigPathMergeList = append(kubeConfigPathMergeList, filepath.SplitList(v)...)
	}
	return kube.Init(kube.InitOptions{KubeConfigOptions: kube.KubeConfigOptions{
		Context:             *kubeConfigFlags.Context,
		ConfigPath:          *kubeConfigFlags.KubeConfig,
		ConfigPathMergeList: kubeConfigPathMergeList,
	}})
}

func trackFollow(name, namespace string) error {
	err := kubedogInit()
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
	err := kubedogInit()
	if err != nil {
		return fmt.Errorf("unable to initialize kubedog: %s", err)
	}

	return multitrack.Multitrack(kube.Kubernetes, multitrack.MultitrackSpecs{
		Deployments: []multitrack.MultitrackSpec{{
			ResourceName:         name,
			Namespace:            namespace,
			TrackTerminationMode: multitrack.WaitUntilResourceReady,
		}},
	}, multitrack.MultitrackOptions{
		Options: tracker.Options{
			LogsFromTime: logsFromTime,
			Timeout:      time.Minute * time.Duration(trackTimeout),
		},
	})
}
