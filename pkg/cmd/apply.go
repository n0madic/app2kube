package cmd

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/n0madic/app2kube/pkg/app2kube"
	"github.com/spf13/cobra"

	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/kubectl/pkg/cmd/apply"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/validation"
)

// streamApplyObjects parses manifest into the resource.Infos that kubectl apply
// operates on, streaming the bytes through an in-memory reader instead of
// hijacking the process's global os.Stdin. The builder chain mirrors
// apply.ApplyOptions.GetObjects() (k8s.io/kubectl v0.29.0): the only change is
// Stream(...) in place of FilenameParam(..., "-"). Keep it in sync with that
// method on a kubectl bump so apply semantics (schema validation, namespace
// defaulting/enforcement, label selector, flatten) stay identical.
func streamApplyObjects(b *resource.Builder, validator validation.Schema, namespace string, enforceNamespace bool, selector, manifest string) ([]*resource.Info, error) {
	b = b.
		Unstructured().
		Schema(validator).
		ContinueOnError().
		NamespaceParam(namespace).DefaultNamespace().
		Stream(strings.NewReader(manifest), "app2kube").
		LabelSelectorParam(selector).
		Flatten()
	if enforceNamespace {
		b = b.RequireNamespace()
	}
	return b.Do().Infos()
}

// blueGreenNotSwitchedMsg explains that a blue/green deploy created (or updated)
// the new color but did not switch live traffic to it, and that re-running is
// safe — the target-color Deployment is updated on the next run.
func blueGreenNotSwitchedMsg(color string) string {
	return fmt.Sprintf("[%s] was deployed but traffic was NOT switched; the live color is unchanged. Re-run the blue-green deploy to retry — the %s deployment will be updated.", color, color)
}

var (
	applyWithStatus bool
	applyWithTrack  string
	applyTimeout    = defaultTrackTimeout
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
	var stability, observe time.Duration

	applyCmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply a configuration to a resource in kubernetes",
		Args:  cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if applyTimeout < 0 {
				return fmt.Errorf("timeout cannot be negative")
			}
			if stability <= 0 || observe <= 0 {
				return fmt.Errorf("blue-green stability and observation durations must be positive")
			}
			return validateTrackValue(applyWithTrack)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Every failure below is a runtime error, not a misuse of the CLI. Without
			// this cobra dumps the full usage text after the error, burying the
			// blue-green "traffic was NOT switched"/"PARTIALLY switched" guidance the
			// operator has to act on. Matches every other app2kube command.
			cmd.SilenceUsage = true

			ctx := cmd.Context()
			app, err := opts.initApp(ctx)
			if err != nil {
				return err
			}

			// The prune whitelist is derived per-app from the generator registry
			// (output.go) so it cannot drift: every kind app2kube can emit is
			// prunable (e.g. the PodDisruptionBudget that disappears when replicas
			// scale back to 1, whose stale minAvailable would otherwise block
			// every node drain), and nothing it never emits is listed (a stale
			// entry would let prune delete an unrelated object matching the
			// selector). It is app-aware so the cert-manager Certificate is only
			// pruned when this app actually uses letsencrypt.
			applyPruneWhitelist := app.PruneWhitelist()

			// Every failure in here is returned, never cmdutil.CheckErr'd: CheckErr
			// os.Exit()s, and the blue/green caller must regain control to report
			// whether live traffic was switched before the process exits (#60).
			applyManifest := func(manifest string, prune bool) error {
				flags.Overwrite = true
				flags.Prune = prune
				flags.PruneWhitelist = applyPruneWhitelist
				o, err := flags.ToOptions(kubeFactory, cmd, "app2kube", args)
				if err != nil {
					return err
				}
				o.DryRunStrategy, err = cmdutil.GetDryRunStrategy(cmd)
				if err != nil {
					return err
				}

				if o.Namespace != "" {
					o.EnforceNamespace = true
				}

				if o.Prune {
					o.Selector, err = scopedSelector(app.Labels)
					if err != nil {
						return err
					}
				}

				if err := o.Validate(); err != nil {
					return err
				}

				// Pre-build the objects from an in-memory reader and hand them to
				// apply via SetObjects, so Run() uses them directly and never reads
				// os.Stdin. This replaces the previous global os.Stdin hijack and its
				// pipe-buffer deadlock window.
				infos, err := streamApplyObjects(o.Builder, o.Validator, o.Namespace, o.EnforceNamespace, o.Selector, manifest)
				if err != nil {
					return err
				}
				// Mirror GetObjects(): apply ApplySet labels when one is configured.
				// app2kube never sets --applyset today, so o.ApplySet is nil and this
				// is a no-op, but it keeps parity with the builder path it replaces.
				if o.ApplySet != nil {
					if err := o.ApplySet.AddLabels(infos...); err != nil {
						return err
					}
				}
				o.SetObjects(infos)

				if runErr := o.Run(); runErr != nil {
					return runErr
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

			if opts.blueGreen {
				if flags.Prune {
					return fmt.Errorf("cannot prune resources with blue-green deployment")
				}

				kcs, err := kubeFactory.KubernetesClientSet()
				if err != nil {
					return err
				}

				dryRun, err := cmdutil.GetDryRunStrategy(cmd)
				if err != nil {
					return err
				}
				previous, err := blueGreenServices(ctx, kcs, app)
				if err != nil {
					return err
				}
				previousName := ""
				if len(previous) > 0 {
					previousColor := previous[0].Spec.Selector[app2kube.LabelColor]
					if previousColor == app.Deployment.BlueGreenColor {
						return fmt.Errorf("active color changed; re-run deploy")
					}
					previousApp := *app
					previousApp.Deployment.BlueGreenColor = previousColor
					previousName = previousApp.GetDeploymentName()
					ready, healthErr := deploymentIsAvailable(ctx, kcs, app.Namespace, previousName)
					if healthErr != nil || !ready {
						fmt.Fprintf(os.Stderr, "WARNING: active deployment %s is not confirmed ready (check error: %v); continuing recovery deploy of %s. No working rollback is currently confirmed.\n", previousName, healthErr, app.GetDeploymentName())
					}
				}
				succeeded := false
				if dryRun == cmdutil.DryRunNone {
					defer func() {
						if !succeeded {
							blueGreenDiagnostics(ctx, os.Stderr, kcs, app)
							ready, err := deploymentIsAvailable(ctx, kcs, app.Namespace, previousName)
							if err != nil || !ready {
								fmt.Fprintln(os.Stderr, "Recovery did not succeed; no working rollback is currently confirmed. Both deployments are retained.")
							}
						}
					}()
				}

				manifest, err := getManifest(app2kube.OutputAllForDeployment)
				if err != nil {
					return err
				}

				// Progress/diagnostics go to stderr so piped manifest/data on stdout
				// stays clean (#61).
				fmt.Fprintf(os.Stderr, "• Pre-deploy for [%s]:\n", colorize(app.Deployment.BlueGreenColor))

				// Phase 1 deploys the new color; phase 2 (below) switches the
				// Service/Ingress once it is ready. If either step fails the live
				// color is unchanged — report that traffic was not switched and
				// return; re-running updates the target-color deployment.
				if err := applyManifest(manifest, false); err != nil {
					fmt.Fprintf(os.Stderr, "• %s\n", blueGreenNotSwitchedMsg(app.Deployment.BlueGreenColor))
					return err
				}

				// Honor --timeout for the phase-1 readiness wait (applyTimeout),
				// not a hardcoded 15m: a long rollout must be able to raise the
				// window it is allowed before reporting "not switched".
				if dryRun == cmdutil.DryRunNone {
					if err := trackReady(ctx, app.GetDeploymentName(), app.Namespace, applyTimeout, time.Now()); err != nil {
						fmt.Fprintf(os.Stderr, "• %s\n", blueGreenNotSwitchedMsg(app.Deployment.BlueGreenColor))
						return err
					}
					if err := waitBlueGreenHealthy(ctx, blueGreenWaitBudget(applyTimeout, stability), stability, 2*time.Second, func(ctx context.Context) (bool, error) {
						return deploymentIsAvailable(ctx, kcs, app.Namespace, app.GetDeploymentName())
					}); err != nil {
						fmt.Fprintf(os.Stderr, "• %s\n", blueGreenNotSwitchedMsg(app.Deployment.BlueGreenColor))
						return fmt.Errorf("candidate did not stay ready: %w", err)
					}
					// Refuse to overwrite a competing deploy's selector change.
					for _, old := range previous {
						current, err := kcs.CoreV1().Services(app.Namespace).Get(ctx, old.Name, metav1.GetOptions{})
						if err != nil {
							return err
						}
						if current.UID != old.UID || !reflect.DeepEqual(current.Spec.Selector, old.Spec.Selector) {
							return fmt.Errorf("service %s changed during deployment; traffic was not switched", old.Name)
						}
					}
				}

				// The new color is deployed and ready, but the traffic-switching
				// resources have not been applied yet: a render failure here leaves the
				// live color untouched, same as a phase-1 failure.
				manifest, err = app.GetManifest("json", app2kube.OutputAllOther)
				if err != nil {
					fmt.Fprintf(os.Stderr, "• %s\n", blueGreenNotSwitchedMsg(app.Deployment.BlueGreenColor))
					return err
				}

				fmt.Fprintf(os.Stderr, "• Final deploy for [%s]:\n", colorize(app.Deployment.BlueGreenColor))

				// Unlike the pre-deploy phase, this final apply switches live
				// traffic (Service selector, Ingress) and is NOT atomic: kubectl
				// applies the objects sequentially with ContinueOnError, so a
				// mid-apply failure can leave traffic partially switched. Restore
				// Service selectors only after verifying the previous deployment.
				if dryRun == cmdutil.DryRunNone {
					ready, err := deploymentIsAvailable(ctx, kcs, app.Namespace, app.GetDeploymentName())
					if err != nil {
						return err
					}
					if !ready {
						return fmt.Errorf("candidate lost readiness before switch; Service selectors unchanged")
					}
				}
				switchErr := applyManifest(manifest, false)
				if dryRun == cmdutil.DryRunNone && switchErr == nil {
					switchErr = waitBlueGreenHealthy(ctx, blueGreenWaitBudget(applyTimeout, observe), observe, 2*time.Second, func(ctx context.Context) (bool, error) {
						ready, err := deploymentIsAvailable(ctx, kcs, app.Namespace, app.GetDeploymentName())
						if err != nil {
							return false, err
						}
						if !ready {
							return false, fmt.Errorf("candidate lost readiness after traffic switch")
						}
						return blueGreenEndpointsReady(ctx, kcs, app)
					})
				}
				if switchErr != nil {
					if dryRun != cmdutil.DryRunNone {
						return switchErr
					}
					fmt.Fprintln(os.Stderr, "Traffic switch or post-switch verification failed; checking previous deployment before rollback.")
					recoveryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
					defer cancel()
					if err := restoreBlueGreenServices(recoveryCtx, kcs, app.Namespace, previousName, app.Deployment.BlueGreenColor, previous); err != nil {
						return fmt.Errorf("deploy failed: %w; rollback not completed: %v; inspect Service selectors (traffic may be partially switched)", switchErr, err)
					}
					fmt.Fprintln(os.Stderr, "Previous deployment was ready; previous Service selectors restored. Both deployments retained.")
					return fmt.Errorf("deploy failed after switch; Service selectors rolled back: %w", switchErr)
				}
				succeeded = true
			} else {
				manifest, err := getManifest(app2kube.OutputAll)
				if err != nil {
					return err
				}

				if err := applyManifest(manifest, flags.Prune); err != nil {
					return err
				}
			}

			if applyWithTrack != "" && len(app.Deployment.Containers) > 0 {
				// Scope the tracking error to its own variable instead of reusing
				// the outer err. Reusing err meant any future early path leaving err
				// non-nil before here would be re-surfaced even when tracking ran
				// fine (#68).
				var trackErr error
				switch strings.ToLower(applyWithTrack) {
				case "follow":
					trackErr = trackFollow(ctx, app.GetDeploymentName(), app.Namespace, applyTimeout, time.Now())
				case "ready":
					trackErr = trackReady(ctx, app.GetDeploymentName(), app.Namespace, applyTimeout, time.Now())
				}
				if trackErr != nil {
					return trackErr
				}
			}

			if applyWithStatus {
				fmt.Println()
				if err := status(ctx, app); err != nil {
					return err
				}
			}

			return nil
		},
	}

	applyCmd.Flags().DurationVar(&stability, "blue-green-stability", 30*time.Second, "Continuous candidate readiness required before switching traffic")
	applyCmd.Flags().DurationVar(&observe, "blue-green-observe", 60*time.Second, "Continuous deployment and ready Service backend availability required after switching traffic")
	opts = addAppFlags(applyCmd)
	addBlueGreenFlag(applyCmd, opts)
	flags.PrintFlags.AddFlags(applyCmd)
	cmdutil.AddDryRunFlag(applyCmd)
	cmdutil.AddServerSideApplyFlags(applyCmd)
	cmdutil.AddValidateFlags(applyCmd)
	cmdutil.AddFieldManagerFlagVar(applyCmd, &flags.FieldManager, apply.FieldManagerClientSideApply)

	applyCmd.Flags().BoolVar(&flags.Prune, "prune", false, "Automatically delete resource objects, including the uninitialized ones, that do not appear in the configs and are created by either apply.")
	applyCmd.Flags().BoolVar(&applyWithStatus, "status", false, "Show application resources status in kubernetes after apply")
	applyCmd.Flags().StringVar(&applyWithTrack, "track", "", "Track Deployment (ready|follow)")
	applyCmd.Flags().IntVar(&applyTimeout, "timeout", defaultTrackTimeout, "Timeout in minutes for --track and the blue-green pre-deploy readiness wait. 0 is wait forever")

	return applyCmd
}
