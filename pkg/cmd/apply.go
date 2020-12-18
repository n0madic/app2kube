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

var (
	applyWithStatus bool
	applyWithTrack  string
)

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
				o.PruneWhitelist = []string{
					"/v1/ConfigMap",
					"/v1/PersistentVolumeClaim",
					"/v1/Secret",
					"/v1/Service",
					"/v1/ServiceAccount",
					"apps/v1/DaemonSet",
					"apps/v1/Deployment",
					"batch/v1beta1/CronJob",
					// "networking/v1/Ingress",
				}
				o.DryRunStrategy, err = cmdutil.GetDryRunStrategy(cmd)
				cmdutil.CheckErr(err)

				if o.Namespace != "" {
					o.EnforceNamespace = true
				}

				if o.Prune {
					o.Selector = getSelector(app.Labels)
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
				if oCmd.Prune {
					return fmt.Errorf("cannot prune resources with blue-green deployment")
				}

				err := deleteDeployment(app.GetDeploymentName(), app.Namespace)
				if err != nil {
					fmt.Printf("Problem with deleting an old deployment: %s\n", err)
				}

				manifest, err := getManifest(app2kube.OutputAllForDeployment)
				cmdutil.CheckErr(err)

				fmt.Printf("• Pre-deploy for [%s]:\n", colorize(app.Deployment.BlueGreenColor))

				cmdutil.CheckErr(applyManifest(manifest, false))

				err = trackReady(app.GetDeploymentName(), app.Namespace)
				if err != nil {
					return err
				}

				manifest, err = app.GetManifest("json", (app2kube.OutputAllOther))
				cmdutil.CheckErr(err)

				fmt.Printf("• Final deploy for [%s]:\n", colorize(app.Deployment.BlueGreenColor))

				cmdutil.CheckErr(applyManifest(manifest, false))
			} else {
				manifest, err := getManifest(app2kube.OutputAll)
				cmdutil.CheckErr(err)

				cmdutil.CheckErr(applyManifest(manifest, oCmd.Prune))
			}

			if applyWithTrack != "" && len(app.Deployment.Containers) > 0 {
				switch strings.ToLower(applyWithTrack) {
				case "follow":
					err = trackFollow(app.GetDeploymentName(), app.Namespace)
				case "ready":
					err = trackReady(app.GetDeploymentName(), app.Namespace)
				default:
					err = fmt.Errorf("unknown track parameters: %s", applyWithTrack)
				}
			}
			cmdutil.CheckErr(err)

			if applyWithStatus {
				fmt.Println()
				status(app)
			}

			return nil
		},
	}

	addAppFlags(applyCmd)
	addBlueGreenFlag(applyCmd)
	oCmd.PrintFlags.AddFlags(applyCmd)
	cmdutil.AddDryRunFlag(applyCmd)
	cmdutil.AddServerSideApplyFlags(applyCmd)
	cmdutil.AddValidateFlags(applyCmd)
	cmdutil.AddFieldManagerFlagVar(applyCmd, &oCmd.FieldManager, apply.FieldManagerClientSideApply)

	applyCmd.Flags().BoolVar(&oCmd.Prune, "prune", false, "Automatically delete resource objects, including the uninitialized ones, that do not appear in the configs and are created by either apply.")
	applyCmd.Flags().BoolVar(&applyWithStatus, "status", false, "Show application resources status in kubernetes after apply")
	applyCmd.Flags().StringVar(&applyWithTrack, "track", "", "Track Deployment (ready|follow)")

	return applyCmd
}
