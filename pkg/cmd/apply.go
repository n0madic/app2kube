package cmd

import (
	"fmt"
	"strings"

	"github.com/logrusorgru/aurora"
	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/rhysd/go-fakeio"
	"github.com/spf13/cobra"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubectl/pkg/cmd/apply"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

var (
	applyWithTrack  string
	blueGreenDeploy bool
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

			if blueGreenDeploy {
				app.Deployment.BlueGreenColor = "blue"
				kcs, err := kubeFactory.KubernetesClientSet()
				if err != nil {
					return err
				}
				svc, err := kcs.CoreV1().Services(app.Namespace).List(metav1.ListOptions{
					LabelSelector: getSelector(app.Labels),
				})
				if err == nil && len(svc.Items) > 0 {
					if currentColor, ok := svc.Items[0].Spec.Selector["app.kubernetes.io/color"]; ok {
						if currentColor == "blue" {
							app.Deployment.BlueGreenColor = "green"
						}
					}
				}

				manifest, err := app.GetManifest("json", app2kube.OutputAllForDeployment)
				cmdutil.CheckErr(err)

				if flagIncludeNamespace {
					namespace, err := app.GetManifest("json", app2kube.OutputNamespace)
					cmdutil.CheckErr(err)
					manifest = namespace + manifest
				}

				colourize := func(str string) string {
					if app.Deployment.BlueGreenColor == "blue" {
						return aurora.BrightBlue(str).String()
					}
					return aurora.Green(str).String()
				}

				fmt.Println(colourize(fmt.Sprintf("• Pre-deploy for [%s]:", app.Deployment.BlueGreenColor)))

				cmdutil.CheckErr(applyManifest(manifest, false))

				err = trackDeploymentTillReady(app.GetReleaseName()+"-"+app.Deployment.BlueGreenColor, app.Namespace)
				if err != nil {
					return err
				}

				fmt.Println(colourize(fmt.Sprintf("• Final deploy for [%s]:", app.Deployment.BlueGreenColor)))
			}

			manifest, err := app.GetManifest("json", app2kube.OutputAll)
			cmdutil.CheckErr(err)

			if flagIncludeNamespace {
				namespace, err := app.GetManifest("json", app2kube.OutputNamespace)
				cmdutil.CheckErr(err)
				manifest = namespace + manifest
			}

			cmdutil.CheckErr(applyManifest(manifest, oCmd.Prune))

			if applyWithTrack != "" {
				switch strings.ToLower(applyWithTrack) {
				case "follow":
					return trackFollow(cmd, args)
				case "ready":
					return trackReady(cmd, args)
				default:
					return fmt.Errorf("unknown track parameters: %s", applyWithTrack)
				}
			}
			return nil
		},
	}

	addAppFlags(applyCmd)
	oCmd.PrintFlags.AddFlags(applyCmd)

	applyCmd.Flags().Bool("dry-run", false, "If true, only print the object that would be sent, without sending it. Warning: --dry-run cannot accurately output the result of merging the local manifest and the server-side data. Use --server-dry-run to get the merged result instead.")
	applyCmd.Flags().BoolVar(&oCmd.ServerDryRun, "server-dry-run", false, "If true, request will be sent to server with dry-run flag, which means the modifications won't be persisted.")
	applyCmd.Flags().BoolVar(&oCmd.Prune, "prune", false, "Automatically delete resource objects, including the uninitialized ones, that do not appear in the configs and are created by either apply.")
	applyCmd.Flags().StringVar(&applyWithTrack, "track", "", "Track Deployment (ready|follow)")
	applyCmd.Flags().BoolVar(&blueGreenDeploy, "blue-green", false, "Enable blue-green deployment")

	return applyCmd
}
