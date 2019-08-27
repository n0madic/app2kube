package cmd

import (
	"fmt"
	"strings"

	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/rhysd/go-fakeio"
	"github.com/spf13/cobra"

	"k8s.io/kubectl/pkg/cmd/apply"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

var applyWithTrack string

// NewCmdApply return apply command
func NewCmdApply() *cobra.Command {
	o := apply.NewApplyOptions(ioStreams)

	applyCmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply a configuration to a resource in kubernetes",
		RunE: func(cmd *cobra.Command, args []string) error {
			o.DeleteFlags.FileNameFlags.Filenames = &[]string{"-"}
			o.Overwrite = true
			o.PruneWhitelist = []string{"/v1/Namespace"}

			if o.Namespace != "" {
				o.EnforceNamespace = true
			}

			cmdutil.AddServerSideApplyFlags(cmd)
			cmdutil.AddValidateFlags(cmd)
			cmdutil.CheckErr(o.Complete(kubeFactory, cmd))

			app, err := initApp()
			if err != nil {
				cmdutil.CheckErr(err)
			}
			if o.Prune {
				o.Selector = getSelector(app.Labels)
			}

			manifest, err := app.GetManifest("json", app2kube.OutputAll)
			cmdutil.CheckErr(err)

			if flagIncludeNamespace {
				namespace, err := app.GetManifest("json", app2kube.OutputNamespace)
				cmdutil.CheckErr(err)
				manifest = namespace + manifest
			}

			fake := fakeio.StdinBytes([]byte{})
			defer fake.Restore()
			go func() {
				fake.StdinBytes([]byte(manifest))
				fake.CloseStdin()
			}()

			cmdutil.CheckErr(o.Run())

			switch strings.ToLower(applyWithTrack) {
			case "follow":
				return trackFollow(cmd, args)
			case "ready":
				return trackReady(cmd, args)
			default:
				return fmt.Errorf("unknown track parameters: %s", applyWithTrack)
			}
		},
	}

	addAppFlags(applyCmd)
	o.PrintFlags.AddFlags(applyCmd)

	applyCmd.Flags().Bool("dry-run", false, "If true, only print the object that would be sent, without sending it. Warning: --dry-run cannot accurately output the result of merging the local manifest and the server-side data. Use --server-dry-run to get the merged result instead.")
	applyCmd.Flags().BoolVar(&o.ServerDryRun, "server-dry-run", o.ServerDryRun, "If true, request will be sent to server with dry-run flag, which means the modifications won't be persisted.")
	applyCmd.Flags().BoolVar(&o.Prune, "prune", o.Prune, "Automatically delete resource objects, including the uninitialized ones, that do not appear in the configs and are created by either apply.")
	applyCmd.Flags().StringVar(&applyWithTrack, "track", "", "Track Deployment (ready|follow)")

	return applyCmd
}
