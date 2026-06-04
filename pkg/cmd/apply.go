package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"

	"k8s.io/kubectl/pkg/cmd/apply"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

// blueGreenNotSwitchedMsg explains that a blue/green deploy created (or updated)
// the new color but did not switch live traffic to it, and that re-running is
// safe — the stale target-color Deployment is replaced on the next run (#60).
func blueGreenNotSwitchedMsg(color string) string {
	return fmt.Sprintf("[%s] was deployed but traffic was NOT switched; the live color is unchanged. Re-run the blue-green deploy to retry — the stale %s deployment will be replaced.", color, color)
}

// applyPruneWhitelist lists the GVKs `apply --prune` is allowed to delete. It
// must stay in sync with the resource generators in app2kube (output.go): every
// kind app2kube can emit must be prunable, otherwise a resource that drops out
// of the manifest is orphaned — e.g. the PodDisruptionBudget disappears when
// replicas scale back to 1, and a stale minAvailable PDB would then block every
// node drain.
var applyPruneWhitelist = []string{
	"/v1/ConfigMap",
	"/v1/PersistentVolumeClaim",
	"/v1/Secret",
	"/v1/Service",
	"/v1/ServiceAccount",
	"apps/v1/DaemonSet",
	"apps/v1/Deployment",
	"batch/v1/CronJob",
	"networking.k8s.io/v1/Ingress",
	"policy/v1/PodDisruptionBudget",
}

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
		Args:  cobra.NoArgs,
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
				flags.PruneWhitelist = applyPruneWhitelist
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
				// Return the apply error instead of cmdutil.CheckErr (which
				// os.Exit()s): blue/green callers must regain control on a real
				// apply failure to report that traffic was NOT switched before the
				// process exits (#60). The wait() error is secondary and only
				// surfaces when the apply itself succeeded.
				if runErr != nil {
					return runErr
				}
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

				kcs, err := kubeFactory.KubernetesClientSet()
				cmdutil.CheckErr(err)

				// Pre-delete the stale target-color deployment before recreating
				// it. A NotFound is expected on the first deploy (nothing to delete)
				// and is ignored; a real RBAC/connectivity error aborts the deploy
				// instead of being printed while the doomed apply proceeds (#65).
				cmdutil.CheckErr(preDeleteDeployment(ctx, kcs, app.GetDeploymentName(), app.Namespace))

				manifest, err := getManifest(app2kube.OutputAllForDeployment)
				cmdutil.CheckErr(err)

				// Progress/diagnostics go to stderr so piped manifest/data on stdout
				// stays clean (#61).
				fmt.Fprintf(os.Stderr, "• Pre-deploy for [%s]:\n", colorize(app.Deployment.BlueGreenColor))

				// Phase 1 deploys the new color; phase 2 (below) switches the
				// Service/Ingress once it is ready. If either step fails the live
				// color is unchanged — report that traffic was not switched and
				// return; re-running replaces the stale target-color deployment (#60).
				if err := applyManifest(manifest, false); err != nil {
					fmt.Fprintf(os.Stderr, "• %s\n", blueGreenNotSwitchedMsg(app.Deployment.BlueGreenColor))
					return err
				}

				if err := trackReady(ctx, app.GetDeploymentName(), app.Namespace, defaultTrackTimeout, time.Now()); err != nil {
					fmt.Fprintf(os.Stderr, "• %s\n", blueGreenNotSwitchedMsg(app.Deployment.BlueGreenColor))
					return err
				}

				manifest, err = app.GetManifest("json", app2kube.OutputAllOther)
				cmdutil.CheckErr(err)

				fmt.Fprintf(os.Stderr, "• Final deploy for [%s]:\n", colorize(app.Deployment.BlueGreenColor))

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
