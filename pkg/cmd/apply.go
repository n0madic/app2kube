package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/kubectl/pkg/cmd/apply"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

var (
	applyWithStatus bool
	applyWithTrack  string
)

// validateTrackValue checks the --track flag value. The valid set is known at
// parse time, so an invalid value (a typo like "redy") must be rejected in
// PreRunE before initApp/apply mutates the cluster (#26).
func validateTrackValue(v string) error {
	switch strings.ToLower(v) {
	case "", "ready", "follow":
		return nil
	default:
		return fmt.Errorf("invalid --track value %q (must be one of: ready, follow)", v)
	}
}

// NewCmdApply return apply command
func NewCmdApply() *cobra.Command {
	flags := apply.NewApplyFlags(ioStreams)
	flags.DeleteFlags.FileNameFlags.Filenames = &[]string{"-"}
	var opts *appOptions

	applyCmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply a configuration to a resource in kubernetes",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return validateTrackValue(applyWithTrack)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			app, err := opts.initApp(ctx)
			cmdutil.CheckErr(err)

			applyManifest := func(manifest string, prune bool) error {
				flags.Overwrite = true
				flags.Prune = prune
				flags.PruneWhitelist = []string{
					"/v1/ConfigMap",
					"/v1/PersistentVolumeClaim",
					"/v1/Secret",
					"/v1/Service",
					"/v1/ServiceAccount",
					"apps/v1/DaemonSet",
					"apps/v1/Deployment",
					"batch/v1/CronJob",
					"networking.k8s.io/v1/Ingress",
				}
				o, err := flags.ToOptions(kubeFactory, cmd, "app2kube", args)
				cmdutil.CheckErr(err)
				o.DryRunStrategy, err = cmdutil.GetDryRunStrategy(cmd)
				cmdutil.CheckErr(err)

				if o.Namespace != "" {
					o.EnforceNamespace = true
				}

				if o.Prune {
					o.Selector, err = scopedSelector(app.Labels)
					cmdutil.CheckErr(err)
				}

				cmdutil.CheckErr(o.Validate())

				wait, err := withStdin([]byte(manifest))
				cmdutil.CheckErr(err)
				runErr := o.Run()
				feedErr := wait()
				cmdutil.CheckErr(runErr)
				if feedErr != nil {
					return fmt.Errorf("feeding manifest to kubectl apply: %w", feedErr)
				}

				return nil
			}

			getManifest := func(output app2kube.OutputResource) (string, error) {
				manifest, err := app.GetManifest("json", output)
				if err != nil {
					return "", err
				}
				if app.Namespace != app2kube.NamespaceDefault && opts.includeNamespace {
					namespace, err := app.GetManifest("json", app2kube.OutputNamespace)
					if err != nil {
						return "", err
					}
					manifest = namespace + manifest
				}
				return manifest, nil
			}

			if blueGreenDeploy {
				if flags.Prune {
					return fmt.Errorf("cannot prune resources with blue-green deployment")
				}

				// Pre-delete the (stale) target-color deployment before recreating
				// it. A NotFound is the expected case on a normal rotation and is
				// ignored; only real errors (RBAC/connectivity) are surfaced.
				err := deleteDeployment(ctx, app.GetDeploymentName(), app.Namespace)
				if err != nil && !apierrors.IsNotFound(err) {
					fmt.Printf("Problem with deleting an old deployment: %s\n", err)
				}

				manifest, err := getManifest(app2kube.OutputAllForDeployment)
				cmdutil.CheckErr(err)

				fmt.Printf("• Pre-deploy for [%s]:\n", colorize(app.Deployment.BlueGreenColor))

				cmdutil.CheckErr(applyManifest(manifest, false))

				err = trackReady(ctx, app.GetDeploymentName(), app.Namespace, defaultTrackTimeout, time.Now())
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

				cmdutil.CheckErr(applyManifest(manifest, flags.Prune))
			}

			if applyWithTrack != "" && len(app.Deployment.Containers) > 0 {
				// Scope the tracking error to its own variable instead of reusing
				// the outer err, and CheckErr it inside the block. Reusing err meant
				// any future early path leaving err non-nil before here would be
				// re-surfaced via CheckErr's os.Exit even when tracking ran fine (#68).
				var trackErr error
				switch strings.ToLower(applyWithTrack) {
				case "follow":
					trackErr = trackFollow(ctx, app.GetDeploymentName(), app.Namespace, trackTimeout, time.Now())
				case "ready":
					trackErr = trackReady(ctx, app.GetDeploymentName(), app.Namespace, trackTimeout, time.Now())
				}
				cmdutil.CheckErr(trackErr)
			}

			if applyWithStatus {
				fmt.Println()
				cmdutil.CheckErr(status(ctx, app))
			}

			return nil
		},
	}

	opts = addAppFlags(applyCmd)
	addBlueGreenFlag(applyCmd)
	flags.PrintFlags.AddFlags(applyCmd)
	cmdutil.AddDryRunFlag(applyCmd)
	cmdutil.AddServerSideApplyFlags(applyCmd)
	cmdutil.AddValidateFlags(applyCmd)
	cmdutil.AddFieldManagerFlagVar(applyCmd, &flags.FieldManager, apply.FieldManagerClientSideApply)

	applyCmd.Flags().BoolVar(&flags.Prune, "prune", false, "Automatically delete resource objects, including the uninitialized ones, that do not appear in the configs and are created by either apply.")
	applyCmd.Flags().BoolVar(&applyWithStatus, "status", false, "Show application resources status in kubernetes after apply")
	applyCmd.Flags().StringVar(&applyWithTrack, "track", "", "Track Deployment (ready|follow)")

	return applyCmd
}
