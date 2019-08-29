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
	oCmd := apply.NewApplyOptions(ioStreams)

	applyCmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply a configuration to a resource in kubernetes",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := initApp()
			cmdutil.CheckErr(err)

			applyManifest := func(manifest string, prune bool) error {
				o := apply.NewApplyOptions(ioStreams)
				o.DeleteFlags.FileNameFlags.Filenames = &[]string{"-"}
				o.Overwrite = true
				o.Prune = prune
				o.ServerDryRun = oCmd.ServerDryRun

				if o.Namespace != "" {
					o.EnforceNamespace = true
				}

				if o.Prune {
					o.Selector = getSelector(app.Labels)
				}

				if cmd.Flags().Lookup("experimental-server-side") == nil {
					cmdutil.AddServerSideApplyFlags(cmd)
				}
				if cmd.Flags().Lookup("validate") == nil {
					cmdutil.AddValidateFlags(cmd)
				}
				cmdutil.CheckErr(o.Complete(kubeFactory, cmd))

				fake := fakeio.StdinBytes([]byte{})
				defer fake.Restore()
				go func() {
					fake.StdinBytes([]byte(manifest))
					fake.CloseStdin()
				}()

				cmdutil.CheckErr(o.Run())

				return nil
			}

			getManifest := func(output app2kube.OutputResource) (string, error) {
				manifest, err := app.GetManifest("json", output)
				if err != nil {
					return "", err
				}
				if app.Namespace != app2kube.NamespaceDefault && flagIncludeNamespace {
					namespace, err := app.GetManifest("json", app2kube.OutputNamespace)
					if err != nil {
						return "", err
					}
					manifest = namespace + manifest
				}
				return manifest, nil
			}

			if blueGreenDeploy {
				manifest, err := getManifest(app2kube.OutputAllForDeployment)
				cmdutil.CheckErr(err)

				fmt.Printf("• Pre-deploy for [%s]:\n", colorize(app.Deployment.BlueGreenColor))

				cmdutil.CheckErr(applyManifest(manifest, false))

				err = trackDeploymentTillReady(app.GetDeploymentName(), app.Namespace)
				if err != nil {
					return err
				}

				fmt.Printf("• Final deploy for [%s]:\n", colorize(app.Deployment.BlueGreenColor))
			}

			manifest, err := getManifest(app2kube.OutputAll)
			cmdutil.CheckErr(err)

			cmdutil.CheckErr(applyManifest(manifest, oCmd.Prune))

			if applyWithTrack != "" {
				switch strings.ToLower(applyWithTrack) {
				case "follow":
					return trackFollow(app)
				case "ready":
					return trackReady(app)
				default:
					return fmt.Errorf("unknown track parameters: %s", applyWithTrack)
				}
			}
			return nil
		},
	}

	addAppFlags(applyCmd)
	addBlueGreenFlag(applyCmd)
	oCmd.PrintFlags.AddFlags(applyCmd)

	applyCmd.Flags().Bool("dry-run", false, "If true, only print the object that would be sent, without sending it. Warning: --dry-run cannot accurately output the result of merging the local manifest and the server-side data. Use --server-dry-run to get the merged result instead.")
	applyCmd.Flags().BoolVar(&oCmd.ServerDryRun, "server-dry-run", false, "If true, request will be sent to server with dry-run flag, which means the modifications won't be persisted.")
	applyCmd.Flags().BoolVar(&oCmd.Prune, "prune", false, "Automatically delete resource objects, including the uninitialized ones, that do not appear in the configs and are created by either apply.")
	applyCmd.Flags().StringVar(&applyWithTrack, "track", "", "Track Deployment (ready|follow)")

	return applyCmd
}
