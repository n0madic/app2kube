package cmd

import (
	"context"
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

// defaultTrackTimeout is the track timeout (in minutes) used by callers that do
// not expose a --timeout flag (apply, blue-green pre-deploy).
const defaultTrackTimeout = 15

var (
	logsSince    = "now"
	trackTimeout = defaultTrackTimeout
)

// resolveLogsFrom computes the kubedog "logs since" start time from the
// --logs-since flag at command execution time (not binary-init time): "now"
// shows only new records, "all" shows everything, and a duration like "5m"
// starts that far in the past. An unparseable value is a user error and is
// rejected rather than silently falling back to "now" (which would hide the
// requested past logs).
func resolveLogsFrom(logsSince string, now time.Time) (time.Time, error) {
	switch logsSince {
	case "now":
		return now, nil
	case "all":
		return time.Time{}, nil
	default:
		since, err := time.ParseDuration(logsSince)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid --logs-since %q (use a duration like 30s, 5m or 2h, or 'now'/'all'): %w", logsSince, err)
		}
		return now.Add(-since), nil
	}
}

// NewCmdTrack return track command
func NewCmdTrack() *cobra.Command {
	trackCmd := &cobra.Command{
		Use:   "track",
		Short: "Track application deployment in kubernetes",
	}

	trackCmd.PersistentFlags().StringVarP(&logsSince, "logs-since", "l", logsSince, "A duration like 30s, 5m, or 2h to start log records from the past. 'all' to show all logs and 'now' to display only new records")
	trackCmd.PersistentFlags().IntVarP(&trackTimeout, "timeout", "t", trackTimeout, "Timeout of operation in minutes. 0 is wait forever")

	// addTrackSub wires a track subcommand with its own appOptions so no state
	// is shared between commands. run receives the cancellable command context,
	// the resolved deployment name/namespace, the timeout and the log start time
	// computed at execution time (trackFollow/trackReady match this signature).
	addTrackSub := func(use, short string, run func(ctx context.Context, name, namespace string, timeout int, logsFrom time.Time) error) {
		c := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs}
		opts := addAppFlags(c)
		addBlueGreenFlag(c, opts)
		_ = c.Flags().MarkHidden("include-namespace")
		c.RunE = func(cmd *cobra.Command, args []string) error {
			app, err := opts.initApp(cmd.Context())
			if err != nil {
				return err
			}
			cmd.SilenceUsage = true
			logsFrom, err := resolveLogsFrom(logsSince, time.Now())
			if err != nil {
				return err
			}
			return run(cmd.Context(), app.GetDeploymentName(), app.Namespace, trackTimeout, logsFrom)
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

func trackFollow(ctx context.Context, name, namespace string, timeout int, logsFrom time.Time) error {
	err := kubedogInit()
	if err != nil {
		return fmt.Errorf("unable to initialize kubedog: %w", err)
	}

	return follow.TrackDeployment(
		name,
		namespace,
		kube.Kubernetes,
		tracker.Options{
			ParentContext: ctx,
			LogsFromTime:  logsFrom,
			Timeout:       time.Minute * time.Duration(timeout),
		},
	)
}

func trackReady(ctx context.Context, name, namespace string, timeout int, logsFrom time.Time) error {
	err := kubedogInit()
	if err != nil {
		return fmt.Errorf("unable to initialize kubedog: %w", err)
	}

	return multitrack.Multitrack(kube.Kubernetes, multitrack.MultitrackSpecs{
		Deployments: []multitrack.MultitrackSpec{{
			ResourceName:         name,
			Namespace:            namespace,
			TrackTerminationMode: multitrack.WaitUntilResourceReady,
		}},
	}, multitrack.MultitrackOptions{
		Options: tracker.Options{
			ParentContext: ctx,
			LogsFromTime:  logsFrom,
			Timeout:       time.Minute * time.Duration(timeout),
		},
	})
}
